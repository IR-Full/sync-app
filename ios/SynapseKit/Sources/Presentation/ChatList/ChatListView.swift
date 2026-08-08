import SwiftUI
import SynapseDomain

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

    func toggleMute(_ summary: ChatSummary) {
        Task { await chatRepository.setMuted(chatID: summary.chat.id, muted: !summary.chat.isMuted) }
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
                ProfileView(account: account)
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
                        Button { model.toggleMute(summary) } label: {
                            Label(
                                summary.chat.isMuted ? l("chats.unmute") : l("chats.mute"),
                                systemImage: summary.chat.isMuted ? "bell" : "bell.slash"
                            )
                        }
                        .tint(.indigo)
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
                isOnline: summary.isPeerOnline && summary.chat.kind == .direct
            )
            VStack(alignment: .leading, spacing: 3) {
                HStack {
                    Text(title).font(.body.weight(.semibold)).lineLimit(1)
                    if summary.chat.isMuted {
                        Image(systemName: "bell.slash.fill")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
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
