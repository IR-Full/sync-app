import SwiftUI
import SynapseDomain

/// Everything a screen needs, handed down as one object rather than a dozen
/// `@EnvironmentObject`s. The DI module builds it; views only read from it.
@MainActor
public struct ViewFactory {
    public let chats: any ChatRepository
    public let messages: any MessageRepository
    public let contacts: any ContactRepository
    public let search: any SearchRepository
    public let media: any MediaRepository
    public let auth: any AuthRepository

    public init(
        chats: any ChatRepository,
        messages: any MessageRepository,
        contacts: any ContactRepository,
        search: any SearchRepository,
        media: any MediaRepository,
        auth: any AuthRepository
    ) {
        self.chats = chats
        self.messages = messages
        self.contacts = contacts
        self.search = search
        self.media = media
        self.auth = auth
    }
}

/// The single entry point. Which of the two worlds is on screen is decided by
/// `AppModel.phase` and nothing else, so there is exactly one place where "am I
/// signed in" is answered.
public struct RootView: View {
    @EnvironmentObject private var app: AppModel
    private let factory: ViewFactory

    public init(factory: ViewFactory) {
        self.factory = factory
    }

    public var body: some View {
        Group {
            switch app.phase {
            case .launching:
                StateView(.loading)
            case .signedOut:
                AuthView(auth: factory.auth)
            case .signedIn(let account):
                ChatListView(account: account, factory: factory)
            }
        }
        .preferredColorScheme(app.settings.theme.colorScheme)
        .animation(.default, value: app.phase)
        .task { await app.start() }
        .alert(
            l("common.error"),
            isPresented: Binding(
                get: { app.alertMessage != nil },
                set: { if !$0 { app.alertMessage = nil } }
            ),
            actions: { Button(l("common.ok"), role: .cancel) {} },
            message: { Text(app.alertMessage ?? "") }
        )
    }
}
