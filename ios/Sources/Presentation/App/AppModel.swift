import Foundation
import SwiftUI
import SynapseDomain
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

    private let auth: any AuthRepository
    private let settingsRepository: any SettingsRepository
    private let push: any PushRepository
    private var observers: [Task<Void, Never>] = []

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

    public func signOut() async {
        await push.unregister()
        await auth.logout()
        pendingChatID = nil
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

    // MARK: - Observation

    private func observeSettings() {
        observers.append(Task { [weak self] in
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
        observers.append(Task { [weak self] in
            guard let self else { return }
            for await status in self.auth.connectionStatus() {
                withAnimation { self.connection = status }
            }
        })
    }

    private func observeSessionExpiry() {
        observers.append(Task { [weak self] in
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

    deinit {
        for observer in observers { observer.cancel() }
    }
}
