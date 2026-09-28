import Foundation
import SwiftUI
import SyncAppDomain
import UIKit

/// Root state: who is signed in, whether the connection is up, and which chat
/// the app should be showing.
///
/// It owns exactly three things a screen cannot own for itself — the session,
/// the connection banner, and navigation — and delegates everything else. In
/// particular it is *not* a store: chat and message state lives in the cache and
/// reaches views through their own view models.
@MainActor
public final class AppModel: ObservableObject {

    public enum Phase: Equatable {
        case launching
        case signedOut
        case signedIn(Account)
    }

    @Published public private(set) var phase: Phase = .launching
    @Published public private(set) var connection: ConnectionStatus = .offline
    @Published public var settings = AppSettings()
    /// Set by a push tap or an invite link; the chat list navigates to it.
    @Published public var pendingChatID: String?
    @Published public var alertMessage: String?

    /// The conversation currently on screen, or nil. Set by `ChatView` while it is
    /// visible, and read when deciding whether a push deserves a banner.
    public private(set) var visibleChatID: String?

    private let auth: any AuthRepository
    private let settingsRepository: any SettingsRepository
    private let push: any PushRepository
    private let observers = TaskBag()

    public init(
        auth: any AuthRepository,
        settings settingsRepository: any SettingsRepository,
        push: any PushRepository
    ) {
        self.auth = auth
        self.settingsRepository = settingsRepository
        self.push = push
    }

    /// Decides the first screen: restore a session if we can, otherwise login.
    ///
    /// A failed restore is not an error to show. The token expired or the server
    /// is unreachable; either way the answer is the login screen, and an alert
    /// on top of it would only be noise.
    public func start() async {
        observeSettings()
        observeConnection()
        observeSessionExpiry()
        observeAccount()

        guard await auth.currentAccount() != nil else {
            phase = .signedOut
            return
        }
        do {
            let account = try await auth.restoreSession()
            phase = .signedIn(account)
        } catch {
            phase = .signedOut
        }
    }

    public func didSignIn(_ account: Account) {
        phase = .signedIn(account)
    }

    /// Reconnects when the app comes back to the foreground and we are signed in
    /// but offline.
    ///
    /// The client backs off and redials on its own, so this is not the primary
    /// path — it is the one that covers the cases backoff cannot: a connect that
    /// failed at launch (nothing schedules a retry, since `connect` reports the
    /// failure to its caller), and a return from a long suspension where the next
    /// backoff sleep may be minutes away while the user is looking at the screen
    /// now. Reconnecting is idempotent when the connection is already up, and the
    /// guard keeps it from firing on every incidental phase change.
    public func didEnterForeground() async {
        guard case .signedIn = phase, connection != .online else { return }
        do {
            let account = try await auth.restoreSession()
            phase = .signedIn(account)
        } catch {
            // A dead credential arrives separately as a session expiry, which
            // already routes to the login screen; anything else is a transient
            // network failure the client keeps retrying. Neither is worth an alert
            // the user did not ask for.
        }
    }

    /// Called by `ChatView` as it appears and disappears. Not `@Published`: only
    /// the notification decision reads it, and republishing it would redraw the
    /// whole tree every time the user opens a chat.
    public func setVisibleChat(_ chatID: String?) {
        visibleChatID = chatID
    }

    /// Whether a notification for this chat should be shown as a banner.
    ///
    /// False for the conversation already on screen: the message is about to
    /// appear in it, and a banner over the top of it is noise. The server cannot
    /// make this call — it has no idea which screen is open.
    public func shouldPresentNotification(forChatID chatID: String) -> Bool {
        guard case .signedIn = phase else { return true }
        return chatID.isEmpty || chatID != visibleChatID
    }

    public func signOut() async {
        await push.unregister()
        await auth.logout()
        pendingChatID = nil
        phase = .signedOut
    }

    /// Returns to the login screen after the session ended somewhere ELSE in the app,
    /// with the local wipe already done.
    ///
    /// Two callers, both from the security screen: revoking this device's own session,
    /// and deleting the account. `AccountSecurityRepository` performs the wipe itself in
    /// both cases — it has to, because forgetting it would leave a deleted account's
    /// chats on screen — so this must NOT repeat `auth.logout()`. What is left is the
    /// part only the root knows: unregister the push token and change the phase.
    ///
    /// Unregistering still happens here rather than in the repository, because a push
    /// token is a property of the app's relationship with APNs, not of the account row.
    public func accountSessionEnded(message: String?) async {
        await push.unregister()
        pendingChatID = nil
        alertMessage = message
        phase = .signedOut
    }

    /// Called from the app delegate when APNs hands over a token.
    public func registerForPush(deviceToken: Data) async {
        guard settings.pushEnabled else { return }
        await push.register(deviceToken: deviceToken)
    }

    /// A notification tap. The payload the server sends carries `chat_id`
    /// (see `internal/notify`), which is all a deep link needs.
    public func handleNotification(userInfo: [AnyHashable: Any]) {
        guard let chatID = userInfo["chat_id"] as? String, !chatID.isEmpty else { return }
        pendingChatID = chatID
    }

    public func updateSettings(_ transform: @escaping @Sendable (inout AppSettings) -> Void) {
        Task { await settingsRepository.update(transform) }
    }

    // MARK: - Profile

    /// The signed-in account, when there is one. Screens read the profile from
    /// here rather than holding their own copy, so a change mirrored from
    /// another device reaches all of them at once.
    public var account: Account? {
        if case .signedIn(let account) = phase { return account }
        return nil
    }

    /// Changes our own name and/or avatar. Omitted values are left alone;
    /// `clearAvatar` is the only way to remove a picture, because an empty ref
    /// means "no change" on the wire.
    public func updateProfile(
        displayName: String? = nil,
        avatarRef: String? = nil,
        clearAvatar: Bool = false
    ) async throws {
        let updated = try await auth.updateProfile(
            displayName: displayName,
            avatarRef: avatarRef,
            clearAvatar: clearAvatar
        )
        phase = .signedIn(updated)
    }

    // MARK: - Observation

    private func observeSettings() {
        observers.add(Task { [weak self] in
            guard let self else { return }
            for await settings in self.settingsRepository.settings() {
                self.settings = settings
                L10n.setLanguage(settings.language)
                // Turning notifications off clears the token server-side, which
                // is the only way to actually stop the push — a device that
                // simply ignores them still costs the server a delivery.
                if !settings.pushEnabled {
                    await self.push.unregister()
                }
            }
        })
    }

    private func observeConnection() {
        observers.add(Task { [weak self] in
            guard let self else { return }
            for await status in self.auth.connectionStatus() {
                withAnimation { self.connection = status }
            }
        })
    }

    private func observeSessionExpiry() {
        observers.add(Task { [weak self] in
            guard let self else { return }
            for await _ in self.auth.sessionExpirations() {
                // The server revoked us. Reconnecting cannot fix it, so drop
                // straight to the login screen and say why.
                await self.auth.logout()
                self.alertMessage = l("auth.session.expired")
                self.phase = .signedOut
            }
        })
    }

    /// Follows our own profile: the stored copy first, then every change the
    /// gateway mirrors from this account's other devices.
    ///
    /// It republishes the phase rather than keeping a second copy, so a screen
    /// showing `account` cannot disagree with the one the app signed in with.
    private func observeAccount() {
        observers.add(Task { [weak self] in
            guard let self else { return }
            for await account in self.auth.accountUpdates() {
                // Only while signed in: an update arriving during logout must
                // not resurrect the phase.
                if case .signedIn = self.phase { self.phase = .signedIn(account) }
            }
        })
    }

    // No `deinit`: the bag cancels its tasks when it is released with us.
}
