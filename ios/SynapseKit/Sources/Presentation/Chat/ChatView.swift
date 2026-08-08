import PhotosUI
import SwiftUI
import SynapseDomain
import UIKit
import UniformTypeIdentifiers

@MainActor
final class ChatViewModel: ObservableObject {
    @Published private(set) var messages: [Message] = []
    @Published private(set) var chat: Chat?
    @Published private(set) var peer: User?
    @Published private(set) var typingUserIDs: Set<String> = []
    @Published private(set) var isLoadingOlder = false
    @Published private(set) var hasMoreHistory = true
    @Published private(set) var isUploading = false
    @Published var draft = ""
    @Published var errorMessage: String?
    @Published var replyTo: Message?
    @Published var editing: Message?
    @Published var editedText = ""
    /// Local file URLs for attachments we have already downloaded.
    @Published private(set) var mediaURLs: [String: URL] = [:]

    let chatID: String
    let account: Account

    private let messageRepository: any MessageRepository
    private let chatRepository: any ChatRepository
    private let contacts: any ContactRepository
    private let media: any MediaRepository
    private let sendMessage: SendMessageUseCase
    private let markRead: MarkChatReadUseCase

    private var observers: [Task<Void, Never>] = []
    private var typingSignalTask: Task<Void, Never>?
    private var draftSaveTask: Task<Void, Never>?
    private var lastTypingSentAt: Date?
    private var names: [String: String] = [:]
    private var inFlightDownloads: Set<String> = []
    /// Suppresses the draft-save that a *remote* draft update would otherwise
    /// trigger by writing into `draft`, which would echo straight back.
    private var isApplyingRemoteDraft = false

    init(chatID: String, account: Account, factory: ViewFactory) {
        self.chatID = chatID
        self.account = account
        self.messageRepository = factory.messages
        self.chatRepository = factory.chats
        self.contacts = factory.contacts
        self.media = factory.media
        self.sendMessage = SendMessageUseCase(messages: factory.messages)
        self.markRead = MarkChatReadUseCase(messages: factory.messages)
    }

    func start() async {
        chat = await chatRepository.chat(id: chatID)
        await refreshPeer()

        observers.append(Task { [weak self] in
            guard let self else { return }
            for await messages in self.messageRepository.observeMessages(chatID: self.chatID) {
                self.messages = messages
                await self.refreshNames(in: messages)
                await self.adoptCachedMedia(in: messages)
                await self.markLatestRead()
            }
        })
        observers.append(Task { [weak self] in
            guard let self else { return }
            for await typists in self.messageRepository.observeTyping(chatID: self.chatID) {
                self.typingUserIDs = typists
            }
        })
        observers.append(Task { [weak self] in
            guard let self else { return }
            for await text in self.messageRepository.observeDraft(chatID: self.chatID) {
                // Only adopt a draft from elsewhere while this composer is idle:
                // overwriting what the user is typing because another device
                // typed something is worse than letting the two diverge.
                guard self.draft.isEmpty || self.draft != text else { continue }
                if self.draft.isEmpty {
                    self.isApplyingRemoteDraft = true
                    self.draft = text
                    self.isApplyingRemoteDraft = false
                }
            }
        })

        // Always pull the newest page on open. The cache may be missing whatever
        // arrived while the app was backgrounded past the replay buffer's
        // lifetime, and that gap is *forward* of everything we hold — paging
        // older would never reach it.
        await refreshLatest()
    }

    func refreshLatest() async {
        do {
            try await messageRepository.refreshLatest(chatID: chatID)
        } catch {
            if let error = error as? AppError, error != .offline {
                errorMessage = Self.describe(error)
            }
        }
    }

    func loadOlder(force: Bool = false) async {
        guard !isLoadingOlder, force || hasMoreHistory else { return }
        isLoadingOlder = true
        defer { isLoadingOlder = false }
        do {
            hasMoreHistory = try await messageRepository.loadOlder(chatID: chatID)
        } catch {
            // Offline is the expected case here, not an error worth an alert:
            // the cached page is still on screen.
            if let error = error as? AppError, error != .offline {
                errorMessage = Self.describe(error)
            }
        }
    }

    // MARK: - Sending

    func send() async {
        let text = draft
        let reply = replyTo
        draft = ""
        replyTo = nil
        do {
            try await sendMessage(chatID: chatID, text: text, replyTo: reply?.id)
        } catch let error as ValidationError {
            draft = text  // give the text back; losing it would be worse
            errorMessage = Self.describe(error)
        } catch {
            errorMessage = Self.describe(error)
        }
    }

    /// Uploads first, then queues the message carrying the resulting ref.
    ///
    /// The upload needs a connection — the ticket is minted over the protocol
    /// and its signed size is binding — so unlike a text message this cannot be
    /// composed offline. That is stated to the user rather than silently queued.
    func attach(data: Data, filename: String, mime: String, kind: Attachment.Kind, extra: MediaMetadata) async {
        isUploading = true
        defer { isUploading = false }
        do {
            let attachment = try await media.upload(
                data: data, filename: filename, mime: mime, kind: kind, extra: extra
            )
            if let cached = await media.cachedURL(for: attachment) {
                mediaURLs[attachment.mediaRef] = cached
            }
            let caption = draft
            draft = ""
            try await sendMessage(chatID: chatID, text: caption, replyTo: replyTo?.id, attachment: attachment)
            replyTo = nil
        } catch {
            errorMessage = Self.describe(error)
        }
    }

    func attach(photo: PhotosPickerItem) async {
        guard let data = try? await photo.loadTransferable(type: Data.self) else {
            errorMessage = l("chat.error.attachment")
            return
        }
        let size = Self.imageSize(of: data)
        await attach(
            data: data,
            filename: "photo.jpg",
            mime: "image/jpeg",
            kind: .image,
            extra: MediaMetadata(width: size.width, height: size.height)
        )
    }

    func attach(fileAt url: URL) async {
        // A file from the document picker lives outside our sandbox until we ask.
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let data = try? Data(contentsOf: url) else {
            errorMessage = l("chat.error.attachment")
            return
        }
        await attach(
            data: data,
            filename: url.lastPathComponent,
            mime: Self.mimeType(for: url),
            kind: Self.kind(for: url),
            extra: MediaMetadata()
        )
    }

    // MARK: - Message actions

    func beginEdit(_ message: Message) {
        editing = message
        editedText = message.text
    }

    func commitEdit() async {
        guard let message = editing else { return }
        let text = editedText.trimmingCharacters(in: .whitespacesAndNewlines)
        editing = nil
        guard !text.isEmpty, text != message.text else { return }
        do {
            try await messageRepository.edit(chatID: chatID, messageID: message.id, text: text)
        } catch {
            errorMessage = Self.describe(error)
        }
    }

    /// Only our own messages, and only once the server knows about them: an
    /// edit needs a real message id, and a queued message has only a dedup key.
    func canEdit(_ message: Message) -> Bool {
        message.senderID == account.userID && message.state != .sending
            && message.state != .failed && !message.isDeleted
    }

    func retry(_ message: Message) {
        Task {
            do { try await messageRepository.retry(messageID: message.id) }
            catch { errorMessage = Self.describe(error) }
        }
    }

    func delete(_ message: Message) {
        Task {
            do {
                try await messageRepository.delete(
                    chatID: chatID, messageID: message.id, forEveryone: message.senderID == account.userID
                )
            } catch { errorMessage = Self.describe(error) }
        }
    }

    func react(_ message: Message, emoji: String) {
        Task {
            do { try await messageRepository.toggleReaction(chatID: chatID, messageID: message.id, emoji: emoji) }
            catch { errorMessage = Self.describe(error) }
        }
    }

    /// Fetches an attachment's bytes on demand — never automatically while
    /// scrolling, because every download costs a signed-URL round trip first.
    func loadMedia(for message: Message) {
        guard let attachment = message.attachment,
              mediaURLs[attachment.mediaRef] == nil,
              !inFlightDownloads.contains(attachment.mediaRef)
        else { return }
        inFlightDownloads.insert(attachment.mediaRef)
        Task {
            defer { inFlightDownloads.remove(attachment.mediaRef) }
            do {
                mediaURLs[attachment.mediaRef] = try await media.fileURL(for: attachment)
            } catch {
                errorMessage = Self.describe(error)
            }
        }
    }

    // MARK: - Typing & drafts

    /// Typing is throttled locally before it is throttled remotely.
    ///
    /// The gateway allows about one typing frame every two seconds per chat and
    /// silently drops the rest, so sending one per keystroke would spend the
    /// connection's budget to no effect. Signalling at most every three seconds
    /// stays under that ceiling while still looking live.
    func draftChanged() {
        guard !isApplyingRemoteDraft else { return }
        scheduleDraftSave()

        guard !draft.isEmpty else {
            sendTyping(active: false)
            return
        }
        let now = Date()
        if let last = lastTypingSentAt, now.timeIntervalSince(last) < 3 { return }
        lastTypingSentAt = now
        sendTyping(active: true)
    }

    /// Debounced: a draft is worth syncing when the user pauses, not on every
    /// keystroke — the mirror goes to their other devices over the same
    /// flood-limited connection the messages use.
    private func scheduleDraftSave() {
        draftSaveTask?.cancel()
        let text = draft
        let reply = replyTo?.id
        draftSaveTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(800))
            guard !Task.isCancelled, let self else { return }
            await self.messageRepository.saveDraft(chatID: self.chatID, text: text, replyTo: reply)
        }
    }

    // MARK: - Titles & presence

    func title() -> String {
        guard let chat else { return l("chats.untitled") }
        if !chat.title.isEmpty { return chat.title }
        if let handle = chat.username { return "@" + handle }
        if let peerID = chat.peerUserID, let name = names[peerID] { return name }
        return l("chats.untitled")
    }

    /// Typing wins over presence: it is the more specific and more fleeting fact.
    func statusLine() -> String? {
        if !typingUserIDs.isEmpty { return l("chat.typing") }
        guard chat?.kind == .direct, let peer else { return nil }
        if peer.isOnline { return l("presence.online") }
        guard let lastSeen = peer.lastSeenAt else { return nil }
        return l("presence.last.seen", lastSeen.chatListStamp())
    }

    var isPeerOnline: Bool { peer?.isOnline ?? false }

    func senderName(_ userID: String) -> String {
        if userID == account.userID { return l("chat.you") }
        return names[userID] ?? userID
    }

    // MARK: - Private

    private func sendTyping(active: Bool) {
        typingSignalTask?.cancel()
        typingSignalTask = Task { [weak self] in
            guard let self else { return }
            await self.messageRepository.setTyping(chatID: self.chatID, active: active)
        }
    }

    private func markLatestRead() async {
        guard let chat, let newest = messages.last(where: { $0.seq > 0 }) else { return }
        await markRead(chat: chat, upToSeq: newest.seq)
        self.chat = await chatRepository.chat(id: chatID)
    }

    private func refreshPeer() async {
        guard let peerID = chat?.peerUserID else { return }
        peer = await contacts.user(id: peerID)
    }

    private func refreshNames(in messages: [Message]) async {
        for senderID in Set(messages.map(\.senderID)) where names[senderID] == nil {
            if let user = await contacts.user(id: senderID) {
                names[senderID] = user.bestName
            }
        }
        if chat?.peerUserID == nil {
            // A direct chat learns its peer from the first inbound message.
            chat = await chatRepository.chat(id: chatID)
        }
        await refreshPeer()
        if let peerID = chat?.peerUserID, names[peerID] == nil, let peer {
            names[peerID] = peer.bestName
        }
    }

    /// Adopts anything already on disk without starting a transfer — our own
    /// uploads, and attachments fetched in an earlier session.
    private func adoptCachedMedia(in messages: [Message]) async {
        for message in messages {
            guard let attachment = message.attachment,
                  mediaURLs[attachment.mediaRef] == nil,
                  let cached = await media.cachedURL(for: attachment)
            else { continue }
            mediaURLs[attachment.mediaRef] = cached
        }
    }

    private static func imageSize(of data: Data) -> (width: Int32, height: Int32) {
        guard let image = UIImage(data: data) else { return (0, 0) }
        return (Int32(image.size.width * image.scale), Int32(image.size.height * image.scale))
    }

    private static func mimeType(for url: URL) -> String {
        if let type = UTType(filenameExtension: url.pathExtension),
           let mime = type.preferredMIMEType {
            return mime
        }
        return "application/octet-stream"
    }

    private static func kind(for url: URL) -> Attachment.Kind {
        guard let type = UTType(filenameExtension: url.pathExtension) else { return .file }
        if type.conforms(to: .image) { return .image }
        if type.conforms(to: .movie) { return .video }
        if type.conforms(to: .audio) { return .voice }
        return .file
    }

    private static func describe(_ error: Error) -> String {
        if let error = error as? ValidationError {
            switch error {
            case .emptyMessage: return l("chat.error.empty")
            case .messageTooLong(_, let limit): return l("chat.error.too.long", limit)
            default: return l("common.error")
            }
        }
        guard let error = error as? AppError else { return l("common.error") }
        switch error {
        case .blocked: return l("chat.error.blocked")
        case .forbidden: return l("chat.error.forbidden")
        case .notFound: return l("chat.error.not.found")
        case .rateLimited: return l("chat.error.rate.limited")
        case .offline: return l("connection.offline")
        case .mediaTooLarge(let limit):
            return l("chat.error.media.too.large", ByteCountFormatter.string(fromByteCount: limit, countStyle: .file))
        case .mediaFailed: return l("chat.error.attachment")
        case .invalidInput(let detail): return detail
        default: return l("common.error")
        }
    }

    deinit {
        for observer in observers { observer.cancel() }
        typingSignalTask?.cancel()
        draftSaveTask?.cancel()
    }
}

struct ChatView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model: ChatViewModel
    @FocusState private var isComposerFocused: Bool
    @State private var photoItem: PhotosPickerItem?
    @State private var isImportingFile = false

    init(chatID: String, account: Account, factory: ViewFactory) {
        _model = StateObject(wrappedValue: ChatViewModel(chatID: chatID, account: account, factory: factory))
    }

    var body: some View {
        VStack(spacing: 0) {
            ConnectionBanner(status: app.connection)
            transcript
            composer
        }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 0) {
                    Text(model.title()).font(.headline)
                    if let status = model.statusLine() {
                        Text(status)
                            .font(.caption2)
                            .foregroundStyle(model.typingUserIDs.isEmpty ? .secondary : Color.accentColor)
                    }
                }
            }
        }
        .task { await model.start() }
        .onChange(of: photoItem) { item in
            guard let item else { return }
            Task {
                await model.attach(photo: item)
                photoItem = nil
            }
        }
        .fileImporter(isPresented: $isImportingFile, allowedContentTypes: [.item]) { result in
            guard case .success(let url) = result else { return }
            Task { await model.attach(fileAt: url) }
        }
        .alert(
            l("chat.edit"),
            isPresented: Binding(
                get: { model.editing != nil },
                set: { if !$0 { model.editing = nil } }
            )
        ) {
            TextField(l("chat.placeholder"), text: $model.editedText)
            Button(l("common.cancel"), role: .cancel) { model.editing = nil }
            Button(l("common.done")) { Task { await model.commitEdit() } }
        }
        .alert(
            l("common.error"),
            isPresented: Binding(
                get: { model.errorMessage != nil },
                set: { if !$0 { model.errorMessage = nil } }
            ),
            actions: { Button(l("common.ok"), role: .cancel) {} },
            message: { Text(model.errorMessage ?? "") }
        )
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 6) {
                    if model.hasMoreHistory {
                        // Paging trigger: the spinner appears when the user
                        // scrolls into it, which is also when we ask for the next
                        // page. No scroll-offset arithmetic needed.
                        ProgressView()
                            .padding()
                            .task { await model.loadOlder() }
                    }
                    ForEach(model.messages) { message in
                        MessageRow(
                            message: message,
                            isOutgoing: message.senderID == model.account.userID,
                            senderName: model.senderName(message.senderID),
                            showSender: model.chat?.kind != .direct,
                            canEdit: model.canEdit(message),
                            mediaURL: message.attachment.flatMap { model.mediaURLs[$0.mediaRef] },
                            onLoadMedia: { model.loadMedia(for: message) },
                            onRetry: { model.retry(message) },
                            onEdit: { model.beginEdit(message) },
                            onDelete: { model.delete(message) },
                            onReact: { model.react(message, emoji: $0) },
                            onReply: { model.replyTo = message }
                        )
                        .id(message.id)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
            }
            .onChange(of: model.messages.count) { _ in
                guard let last = model.messages.last else { return }
                withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
            }
        }
    }

    private var composer: some View {
        VStack(spacing: 0) {
            if let reply = model.replyTo {
                HStack {
                    Image(systemName: "arrowshape.turn.up.left")
                    Text(reply.text.isEmpty ? l("attachment.file") : reply.text)
                        .lineLimit(1)
                        .font(.footnote)
                    Spacer()
                    Button { model.replyTo = nil } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Color(uiColor: .secondarySystemBackground))
            }

            HStack(alignment: .bottom, spacing: 8) {
                Menu {
                    // PhotosPicker inside a Menu keeps the two sources in one
                    // affordance instead of crowding the composer with buttons.
                    PhotosPicker(selection: $photoItem, matching: .images) {
                        Label(l("chat.attach.photo"), systemImage: "photo")
                    }
                    Button { isImportingFile = true } label: {
                        Label(l("chat.attach.file"), systemImage: "doc")
                    }
                } label: {
                    Image(systemName: "paperclip")
                        .font(.system(size: 22))
                }
                .disabled(model.isUploading)
                .accessibilityLabel(l("chat.attach"))

                TextField(l("chat.placeholder"), text: $model.draft, axis: .vertical)
                    .lineLimit(1...5)
                    .textFieldStyle(.plain)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Capsule().fill(Color(uiColor: .secondarySystemBackground)))
                    .focused($isComposerFocused)
                    .onChange(of: model.draft) { _ in model.draftChanged() }

                if model.isUploading {
                    ProgressView().frame(width: 30, height: 30)
                } else {
                    Button {
                        Task { await model.send() }
                    } label: {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.system(size: 30))
                    }
                    .disabled(model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityLabel(l("chat.send"))
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        }
        .background(.bar)
    }
}
