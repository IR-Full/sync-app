import SwiftUI
import SynapseDomain

@MainActor
final class NewChatViewModel: ObservableObject {
    @Published var handle = ""
    @Published var groupTitle = ""
    @Published var groupMembers: [String] = []
    @Published var memberDraft = ""
    @Published var inviteCode = ""
    @Published private(set) var contacts: [Contact] = []
    @Published private(set) var isBusy = false
    @Published var errorMessage: String?

    private let chatRepository: any ChatRepository
    private let contactRepository: any ContactRepository
    // Suffixed so they do not collide with the methods of the same intent below.
    private let openDirectUseCase: OpenDirectChatUseCase
    private let createGroupUseCase: CreateGroupUseCase
    private var observer: Task<Void, Never>?

    init(factory: ViewFactory) {
        self.chatRepository = factory.chats
        self.contactRepository = factory.contacts
        self.openDirectUseCase = OpenDirectChatUseCase(chats: factory.chats)
        self.createGroupUseCase = CreateGroupUseCase(chats: factory.chats)
    }

    func start() {
        guard observer == nil else { return }
        observer = Task { [weak self] in
            guard let self else { return }
            for await contacts in self.contactRepository.observeContacts() {
                self.contacts = contacts
            }
        }
        Task { try? await contactRepository.sync() }
    }

    /// Opening a chat with `@handle` is the only "user search" the protocol
    /// supports: there is no directory to browse, and no prefix query. So the
    /// screen asks for an exact handle and reports plainly when there is no such
    /// user, rather than pretending to search.
    func openDirect() async -> String? {
        await perform {
            try await self.openDirectUseCase(handle: self.handle).id
        }
    }

    func open(contact: Contact) async -> String? {
        await perform {
            // A contact gives us a user id, and `resolveChat` accepts an id as
            // readily as a handle — but only through a chat-scoped command, so
            // the repository still does the resolve.
            try await self.chatRepository.openDirectChat(username: contact.userID).id
        }
    }

    func addMember() {
        let normalized = OpenDirectChatUseCase.normalize(memberDraft)
        guard !normalized.isEmpty, !groupMembers.contains(normalized) else { return }
        groupMembers.append(normalized)
        memberDraft = ""
    }

    func createGroupChat(isChannel: Bool) async -> String? {
        await perform {
            try await self.createGroupUseCase(
                title: self.groupTitle, memberHandles: self.groupMembers, isChannel: isChannel
            ).id
        }
    }

    /// Join accepts either an invite code or a public `@handle`, and the two are
    /// distinguishable by the `@`.
    func join() async -> String? {
        let trimmed = inviteCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        return await perform {
            if trimmed.hasPrefix("@") {
                return try await self.chatRepository.join(
                    code: nil, handle: OpenDirectChatUseCase.normalize(trimmed)
                )
            }
            // A link is accepted as well as a bare code — people paste links.
            let code = trimmed.split(separator: "/").last.map(String.init) ?? trimmed
            return try await self.chatRepository.join(code: code, handle: nil)
        }
    }

    private func perform(_ work: @escaping () async throws -> String) async -> String? {
        isBusy = true
        errorMessage = nil
        defer { isBusy = false }
        do {
            return try await work()
        } catch {
            errorMessage = Self.describe(error)
            return nil
        }
    }

    private static func describe(_ error: Error) -> String {
        if let error = error as? ValidationError {
            switch error {
            case .usernameInvalid: return l("new.error.handle")
            case .emptyTitle: return l("new.error.title")
            case .titleTooLong(let limit): return l("new.error.title.long", limit)
            case .tooManyMembers(let limit): return l("new.error.members", limit)
            default: return l("common.error")
            }
        }
        guard let error = error as? AppError else { return l("common.error") }
        switch error {
        case .notFound: return l("new.error.not.found")
        case .blocked: return l("new.error.blocked")
        case .rateLimited: return l("new.error.throttled")
        case .offline: return l("connection.offline")
        case .forbidden: return l("chat.error.forbidden")
        default: return l("common.error")
        }
    }

    deinit { observer?.cancel() }
}

struct NewChatView: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: NewChatViewModel
    private let onOpen: (String) -> Void

    init(factory: ViewFactory, onOpen: @escaping (String) -> Void) {
        _model = StateObject(wrappedValue: NewChatViewModel(factory: factory))
        self.onOpen = onOpen
    }

    var body: some View {
        NavigationStack {
            Form {
                directSection
                contactsSection
                groupSection
                joinSection
            }
            .navigationTitle(l("new.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(l("common.cancel")) { dismiss() }
                }
            }
            .overlay {
                if model.isBusy {
                    Color.black.opacity(0.05).overlay(ProgressView()).ignoresSafeArea()
                }
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
            .task { model.start() }
        }
    }

    private var directSection: some View {
        Section {
            HStack {
                Text("@").foregroundStyle(.secondary)
                TextField(l("new.handle.placeholder"), text: $model.handle)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.go)
                    .onSubmit { open { await model.openDirect() } }
            }
            Button(l("new.direct.action")) {
                open { await model.openDirect() }
            }
            .disabled(model.handle.trimmingCharacters(in: .whitespaces).isEmpty)
        } header: {
            Text(l("new.direct.title"))
        } footer: {
            Text(l("new.direct.footer"))
        }
    }

    @ViewBuilder
    private var contactsSection: some View {
        if !model.contacts.isEmpty {
            Section(l("new.contacts.title")) {
                ForEach(model.contacts.filter { !$0.isBlocked }) { contact in
                    Button {
                        open { await model.open(contact: contact) }
                    } label: {
                        HStack(spacing: 12) {
                            Avatar(
                                title: contact.name.isEmpty ? contact.userID : contact.name,
                                seed: contact.userID,
                                size: 34
                            )
                            Text(contact.name.isEmpty ? contact.userID : contact.name)
                        }
                    }
                }
            }
        }
    }

    private var groupSection: some View {
        Section(l("new.group.title")) {
            TextField(l("new.group.name"), text: $model.groupTitle)

            HStack {
                TextField(l("new.group.member"), text: $model.memberDraft)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .onSubmit { model.addMember() }
                Button(l("new.group.add")) { model.addMember() }
                    .disabled(model.memberDraft.trimmingCharacters(in: .whitespaces).isEmpty)
            }

            if !model.groupMembers.isEmpty {
                ForEach(model.groupMembers, id: \.self) { member in
                    Text("@" + member).foregroundStyle(.secondary)
                }
                .onDelete { offsets in model.groupMembers.remove(atOffsets: offsets) }
            }

            Button(l("new.group.create")) {
                open { await model.createGroupChat(isChannel: false) }
            }
            .disabled(model.groupTitle.trimmingCharacters(in: .whitespaces).isEmpty)

            Button(l("new.channel.create")) {
                open { await model.createGroupChat(isChannel: true) }
            }
            .disabled(model.groupTitle.trimmingCharacters(in: .whitespaces).isEmpty)
        }
    }

    private var joinSection: some View {
        Section {
            TextField(l("new.join.placeholder"), text: $model.inviteCode)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button(l("new.join.action")) {
                open { await model.join() }
            }
            .disabled(model.inviteCode.trimmingCharacters(in: .whitespaces).isEmpty)
        } header: {
            Text(l("new.join.title"))
        } footer: {
            Text(l("new.join.footer"))
        }
    }

    private func open(_ work: @escaping () async -> String?) {
        Task {
            if let chatID = await work() { onOpen(chatID) }
        }
    }
}
