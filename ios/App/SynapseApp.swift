import SwiftUI
import SynapseDI
import SynapsePresentation
import UIKit
import UserNotifications

/// The app shell. Deliberately thin: it wires the container, opens the cache,
/// and forwards the two things only an app delegate can receive — the APNs token
/// and a notification tap.
@main
struct SynapseApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var container: AppContainer

    init() {
        // A cache that cannot even be created is unrecoverable, and failing at
        // launch with a clear message beats running a messenger with no storage.
        guard let container = try? AppContainer() else {
            fatalError("cannot open the local database")
        }
        _container = StateObject(wrappedValue: container)
    }

    var body: some Scene {
        WindowGroup {
            RootView(factory: container.viewFactory)
                .environmentObject(container.appModel)
                .task {
                    delegate.appModel = container.appModel
                    await container.prepare()
                    await requestNotificationAuthorization()
                }
        }
    }

    /// Asked for once the user is past launch. The token that comes back is only
    /// useful if the gateway gets it — see `PushRepository.register`.
    private func requestNotificationAuthorization() async {
        guard container.appModel.settings.pushEnabled else { return }
        let center = UNUserNotificationCenter.current()
        guard let granted = try? await center.requestAuthorization(options: [.alert, .badge, .sound]),
              granted else { return }
        await MainActor.run { UIApplication.shared.registerForRemoteNotifications() }
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    weak var appModel: AppModel?

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        Task { @MainActor in
            await appModel?.registerForPush(deviceToken: deviceToken)
        }
    }

    func application(
        _ application: UIApplication,
        didFailToRegisterForRemoteNotificationsWithError error: Error
    ) {
        // Simulator and provisioning-less builds land here; not fatal.
        NSLog("APNs registration failed: \(error.localizedDescription)")
    }

    /// A tap on a notification. The server's push payload carries `chat_id`
    /// (see `server/internal/notify`), which is exactly what the deep link needs.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse
    ) async {
        let userInfo = response.notification.request.content.userInfo
        await MainActor.run { appModel?.handleNotification(userInfo: userInfo) }
    }

    /// Show the banner even in the foreground — but not for the chat that is
    /// already open. That check lives here rather than server-side because the
    /// server has no idea which screen is on top.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .sound]
    }
}
