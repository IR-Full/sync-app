import SwiftUI
import SyncAppDomain

@MainActor
final class ChatListViewModel: ObservableObject {
    @Published private(set) var chats: [ChatSummary] = []
    @Published private(set) var hasLoaded = false
    @Published var searchText = ""
    @Published private(set) var searchResults: [SearchResult] = []
    @Published private(set) var isSearching = false
    @Published var errorMessage: String?

    private let chatRepository: any ChatRepository
    private let searchRepository: any SearchRepository
    private let contacts: any ContactRepository
    private let tasks = TaskBag()
    private var hasStarted = false
    private var names: [String: String] = [:]

    init(chats: any ChatRepository, search: any SearchRepository, contacts: any ContactRepository) {
        self.chatRepository = chats
        self.searchRepository = search
        self.contacts = contacts
    }

    func start() {
        guard !hasStarted else { return }
        hasStarted = true
        tasks.add(Task { [weak self] in
            guard let self else { return }
            for await summaries in self.chatRepository.observeChats() {
                self.chats = summaries
                self.hasLoaded = true
                await self.resolveNames(for: summaries)
            }
        })
    }

    /// Search hits are messages, not chats, so this is a *separate* result list
    /// rather than a filter over `chats` — filtering would silently hide the
    /// fact that the server searched message text across every chat the user is
    /// in, including ones whose titles do not match at all.
    func search() {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard query.count >= 2 else {
            tasks.cancel("search")
            searchResults = []
            isSearching = false
            return
        }
        // Keyed, so each keystroke supersedes the previous request rather than
        // racing it.
        tasks.replace("search", with: Task { [weak self] in
            guard let self else { return }
            // Debounce: the server charges search per user and rate-limits it.
            try? await Task.sleep(for: .milliseconds(300))
            guard !Task.isCancelled else { return }
            self.isSearching = true
            defer { self.isSearching = false }
            do {
                self.searchResults = try await self.searchRepository.search(query: query)
            } catch {
                self.searchResults = []
            }
        })
    }

    func title(for summary: ChatSummary) -> String {
        if !summary.chat.title.isEmpty { return summary.chat.title }
        if let handle = summary.chat.username { return "@" + handle }
        if let peer = summary.chat.peerUserID, let name = names[peer] { return name }
        return l("chats.untitled")
    }

    func subtitle(for summary: ChatSummary) -> String {
        if !summary.typingUserIDs.isEmpty { return l("chat.typing") }
        if summary.chat.lastMessagePreview.isEmpty { return l("chats.no.messages") }
        return summary.chat.lastMessagePreview
    }

    func hide(_ summary: ChatSummary) {
        Task { await chatRepository.hideLocally(chatID: summary.chat.id) }
    }

    /// The presets a mute menu offers.
    ///
    /// Durations rather than a switch, because the server stores a DEADLINE and a
    /// boolean cannot express the thing people actually want. `nil` is "unmute".
    /// Computed, not stored. A stored `static let` would capture the strings at first
    /// access and keep them after the user switches language in settings — and the
    /// language override is the whole reason `l(_:)` exists rather than
    /// `String(localized:)`.
    static var mutePresets: [(label: String, duration: TimeInterval?)] { [
        (l("chats.mute.hour"), 60 * 60),
        (l("chats.mute.eightHours"), 8 * 60 * 60),
        (l("chats.mute.week"), 7 * 24 * 60 * 60),
        (l("chats.mute.forever"), 10 * 365 * 24 * 60 * 60),
    ] }

    /// Every flag write sends all three values, because the wire carries no field
    /// presence — so the two the user did not touch are read off the row they are
    /// looking at rather than defaulted, which would silently clear them.
    func mute(_ summary: ChatSummary, for duration: TimeInterval?) {
        let chat = summary.chat
        Task {
            await chatRepository.setFlags(
                chatID: chat.id,
                mutedUntil: duration.map { Date().addingTimeInterval($0) },
                pinned: chat.isPinned,
                archived: chat.isArchived
            )
        }
    }

    func togglePin(_ summary: ChatSummary) {
        let chat = summary.chat
        Task {
            await chatRepository.setFlags(
                chatID: chat.id,
                mutedUntil: chat.mutedUntil,
                pinned: !chat.isPinned,
                archived: chat.isArchived
            )
        }
    }

    func setArchived(_ summary: ChatSummary, _ archived: Bool) {
        let chat = summary.chat
        Task {
            await chatRepository.setFlags(
                chatID: chat.id,
                mutedUntil: chat.mutedUntil,
                // Archiving an unread pinned chat and leaving it pinned puts it at the
                // top of a list the user just said they do not want to look at.
                pinned: archived ? false : chat.isPinned,
                archived: archived
            )
        }
    }

    /// Fills in what little the app can know about a peer's name. The protocol
    /// has no user directory, so this is the address book plus whatever handle
    /// we happened to resolve the chat from.
    private func resolveNames(for summaries: [ChatSummary]) async {
        for summary in summaries {
            guard let peer = summary.chat.peerUserID, names[peer] == nil else { continue }
            if let user = await contacts.user(id: peer) {
                names[peer] = user.bestName
            }
        }
    }

    // No `deinit`: the bag cancels its tasks when it is released with us.
}

struct ChatListView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model: ChatListViewModel
    @State private var isPresentingNewChat = false
    @State private var isPresentingProfile = false
    @State private var isPresentingArchive = false
    @State private var path: [String] = []

    private let account: Account
    private let factory: ViewFactory

    init(account: Account, factory: ViewFactory) {
        self.account = account
        self.factory = factory
        _model = StateObject(wrappedValue: ChatListViewModel(
            chats: factory.chats, search: factory.search, contacts: factory.contacts
        ))
    }

    var body: some View {
        NavigationStack(path: $path) {
            VStack(spacing: 0) {
                ConnectionBanner(status: app.connection)
                content
            }
            .navigationTitle(l("chats.title"))
            .toolbar {
                // `.navigationBarLeading`, not `.topBarLeading` — the latter is
                // iOS 17+ and the deployment target is 16.
                ToolbarItem(placement: .navigationBarLeading) {
                    Button { isPresentingProfile = true } label: {
                        Avatar(title: account.username, seed: account.userID, size: 30)
                    }
                    .accessibilityLabel(l("profile.title"))
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { isPresentingNewChat = true } label: {
                        Image(systemName: "square.and.pencil")
                    }
                    .accessibilityLabel(l("chats.new"))
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { isPresentingArchive = true } label: {
                        Image(systemName: "archivebox")
                    }
                    .accessibilityLabel(l("chats.archive.title"))
                }
            }
            .searchable(text: $model.searchText, prompt: l("chats.search"))
            .onChange(of: model.searchText) { _ in model.search() }
            .navigationDestination(for: String.self) { chatID in
                ChatView(chatID: chatID, account: account, factory: factory)
            }
            .sheet(isPresented: $isPresentingNewChat) {
                NewChatView(factory: factory) { chatID in
                    isPresentingNewChat = false
                    path.append(chatID)
                }
            }
            .sheet(isPresented: $isPresentingProfile) {
                ProfileView(account: account, media: factory.media, security: factory.security)
            }
            .sheet(isPresented: $isPresentingArchive) {
                ArchivedChatsView(factory: factory) { chatID in
                    isPresentingArchive = false
                    path.append(chatID)
                }
            }
        }
        .task { model.start() }
        // A push tap sets `pendingChatID`; consuming it here (rather than in the
        // app model) keeps navigation state in the one view that owns a stack.
        .onChange(of: app.pendingChatID) { chatID in
            guard let chatID else { return }
            path = [chatID]
            app.pendingChatID = nil
        }
    }

    @ViewBuilder
    private var content: some View {
        if !model.searchText.isEmpty {
            searchResults
        } else if !model.hasLoaded {
            StateView(.loading)
        } else if model.chats.isEmpty {
            StateView(.empty(
                title: l("chats.empty.title"),
                message: l("chats.empty.message"),
                systemImage: "bubble.left.and.bubble.right"
            ))
        } else {
            List {
                ForEach(model.chats) { summary in
                    NavigationLink(value: summary.chat.id) {
                        ChatRow(summary: summary, title: model.title(for: summary), subtitle: model.subtitle(for: summary))
                    }
                    .swipeActions(edge: .trailing) {
                        Button(role: .destructive) { model.hide(summary) } label: {
                            Label(l("chats.hide"), systemImage: "eye.slash")
                        }
                        Button { model.setArchived(summary, true) } label: {
                            Label(l("chats.archive"), systemImage: "archivebox")
                        }
                        .tint(.gray)
                    }
                    .swipeActions(edge: .leading) {
                        Button { model.togglePin(summary) } label: {
                            Label(
                                summary.chat.isPinned ? l("chats.unpin") : l("chats.pin"),
                                systemImage: summary.chat.isPinned ? "pin.slash" : "pin"
                            )
                        }
                        .tint(.orange)
                    }
                    .contextMenu {
                        Button { model.togglePin(summary) } label: {
                            Label(
                                summary.chat.isPinned ? l("chats.unpin") : l("chats.pin"),
                                systemImage: summary.chat.isPinned ? "pin.slash" : "pin"
                            )
                        }
                        if summary.chat.isMuted {
                            Button { model.mute(summary, for: nil) } label: {
                                Label(l("chats.unmute"), systemImage: "bell")
                            }
                        } else {
                            Menu {
                                ForEach(ChatListViewModel.mutePresets, id: \.label) { preset in
                                    Button(preset.label) { model.mute(summary, for: preset.duration) }
                                }
                            } label: {
                                Label(l("chats.mute"), systemImage: "bell.slash")
                            }
                        }
                        Button { model.setArchived(summary, true) } label: {
                            Label(l("chats.archive"), systemImage: "archivebox")
                        }
                    }
                }
            }
            .listStyle(.plain)
        }
    }

    @ViewBuilder
    private var searchResults: some View {
        if model.isSearching {
            StateView(.loading)
        } else if model.searchResults.isEmpty {
            StateView(.empty(
                title: l("search.empty.title"),
                message: l("search.empty.message"),
                systemImage: "magnifyingglass"
            ))
        } else {
            List(model.searchResults) { hit in
                Button {
                    path = [hit.chatID]
                    model.searchText = ""
                } label: {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(hit.text).lineLimit(2)
                        Text(hit.chatID).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .listStyle(.plain)
        }
    }
}

private struct ChatRow: View {
    let summary: ChatSummary
    let title: String
    let subtitle: String

    var body: some View {
        HStack(spacing: 12) {
            Avatar(
                title: title,
                seed: summary.chat.id,
                // Two-party, not just `.direct`: a secret chat has a peer whose
                // presence is just as meaningful, and the narrower check left the dot
                // permanently off there.
                isOnline: summary.isPeerOnline && summary.chat.kind.isTwoParty
            )
            VStack(alignment: .leading, spacing: 3) {
                HStack {
                    // The lock is the whole visual difference a secret chat gets.
                    // Deliberately: it is a chat, and a row that looked like a
                    // different kind of object is what made the previous design feel
                    // like a separate app nobody opened.
                    if summary.chat.kind.isEndToEnd {
                        Image(systemName: "lock.fill")
                            .font(.caption2)
                            .foregroundStyle(Color.green)
                            .accessibilityLabel(l("chat.secret.badge"))
                    }
                    Text(title).font(.body.weight(.semibold)).lineLimit(1)
                    if summary.chat.isMuted {
                        Image(systemName: "bell.slash.fill")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if summary.chat.isPinned {
                        Image(systemName: "pin.fill")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .accessibilityLabel(l("chats.pinned"))
                    }
                    if let stamp = summary.chat.lastMessageAt {
                        Text(stamp.chatListStamp())
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                HStack {
                    Text(subtitle)
                        .font(.subheadline)
                        .foregroundStyle(summary.typingUserIDs.isEmpty ? .secondary : Color.accentColor)
                        .lineLimit(1)
                    Spacer()
                    if summary.unreadCount > 0 {
                        Text("\(summary.unreadCount)")
                            .font(.caption2.bold())
                            .foregroundStyle(.white)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2)
                            .background(Capsule().fill(summary.chat.isMuted ? Color.secondary : Color.accentColor))
                    }
                }
            }
        }
        .padding(.vertical, 4)
    }
}


/// The archived pile.
///
/// Its own screen rather than a filter on the main list, because the point of
/// archiving is that the rows are NOT in the way. Reads from the same cache through
/// `observeArchivedChats`, so it needs no fetch of its own and works offline.
@MainActor
private final class ArchivedChatsViewModel: ObservableObject {
    @Published private(set) var chats: [ChatSummary] = []
    @Published private(set) var hasLoaded = false

    private let chatRepository: any ChatRepository
    private let tasks = TaskBag()
    private var hasStarted = false

    init(chats: any ChatRepository) {
        self.chatRepository = chats
    }

    func start() {
        guard !hasStarted else { return }
        hasStarted = true
        tasks.add(Task { [weak self] in
            guard let self else { return }
            for await summaries in self.chatRepository.observeArchivedChats() {
                self.chats = summaries
                self.hasLoaded = true
            }
        })
    }

    func unarchive(_ summary: ChatSummary) {
        let chat = summary.chat
        Task {
            await chatRepository.setFlags(
                chatID: chat.id,
                mutedUntil: chat.mutedUntil,
                pinned: chat.isPinned,
                archived: false
            )
        }
    }
}

struct ArchivedChatsView: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: ArchivedChatsViewModel
    private let onOpen: (String) -> Void

    init(factory: ViewFactory, onOpen: @escaping (String) -> Void) {
        self.onOpen = onOpen
        _model = StateObject(wrappedValue: ArchivedChatsViewModel(chats: factory.chats))
    }

    var body: some View {
        NavigationStack {
            Group {
                if !model.hasLoaded {
                    StateView(.loading)
                } else if model.chats.isEmpty {
                    StateView(.empty(
                        title: l("chats.archive.empty.title"),
                        message: l("chats.archive.empty.message"),
                        systemImage: "archivebox"
                    ))
                } else {
                    List(model.chats) { summary in
                        Button { onOpen(summary.chat.id) } label: {
                            ArchivedRow(summary: summary)
                        }
                        .swipeActions(edge: .trailing) {
                            Button { model.unarchive(summary) } label: {
                                Label(l("chats.unarchive"), systemImage: "tray.and.arrow.up")
                            }
                            .tint(.accentColor)
                        }
                    }
                    .listStyle(.plain)
                }
            }
            .navigationTitle(l("chats.archive.title"))
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button(l("common.done")) { dismiss() }
                }
            }
        }
        .task { model.start() }
    }
}

private struct ArchivedRow: View {
    let summary: ChatSummary

    var body: some View {
        HStack(spacing: 12) {
            Avatar(title: summary.chat.title, seed: summary.chat.id)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 4) {
                    if summary.chat.kind.isEndToEnd {
                        Image(systemName: "lock.fill").font(.caption2).foregroundStyle(Color.green)
                    }
                    Text(summary.chat.title.isEmpty ? l("chats.untitled") : summary.chat.title)
                        .font(.body.weight(.semibold))
                        .lineLimit(1)
                }
                if !summary.chat.lastMessagePreview.isEmpty {
                    Text(summary.chat.lastMessagePreview)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer()
            if summary.unreadCount > 0 {
                Text("\(summary.unreadCount)")
                    .font(.caption2.bold())
                    .foregroundStyle(.white)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(Capsule().fill(Color.secondary))
            }
        }
        .padding(.vertical, 4)
    }
}
