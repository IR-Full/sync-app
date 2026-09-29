import SwiftUI
import SyncAppDomain

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
    public let security: any AccountSecurityRepository
    /// Safety numbers for secret chats; nil in builds without the E2E module.
    public let safety: (any SecretSafetyRepository)?

    public init(
        chats: any ChatRepository,
        messages: any MessageRepository,
        contacts: any ContactRepository,
        search: any SearchRepository,
        media: any MediaRepository,
        auth: any AuthRepository,
        security: any AccountSecurityRepository,
        safety: (any SecretSafetyRepository)? = nil
    ) {
        self.chats = chats
        self.messages = messages
        self.contacts = contacts
        self.search = search
        self.media = media
        self.auth = auth
        self.security = security
        self.safety = safety
    }
}

/// The single entry point. Which of the two worlds is on screen is decided by
/// `AppModel.phase` and nothing else, so there is exactly one place where "am I
/// signed in" is answered.
public struct RootView: View {
    @EnvironmentObject private var app: AppModel
    @Environment(\.scenePhase) private var scenePhase
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
        .tint(app.settings.accent.color)
        .environment(\.appAccent, app.settings.accent.color)
        .animation(.default, value: app.phase)
        .task { await app.start() }
        // Coming back to the foreground is the moment a user expects to be online.
        // The client redials on its own with backoff, so this only covers what
        // backoff cannot: a connect that failed at launch, and a resume where the
        // next scheduled attempt is still minutes out.
        .onChange(of: scenePhase) { phase in
            guard phase == .active else { return }
            Task { await app.didEnterForeground() }
        }
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
