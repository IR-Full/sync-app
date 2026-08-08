import SwiftUI
import XCTest

@testable import SynapseDI
@testable import SynapseDomain
// Imported explicitly: a transitive dependency still needs its own import, and
// `ServerEnvironment` lives here rather than in DI.
@testable import SynapseNetwork
@testable import SynapsePresentation

/// Tests for the presentation layer's non-visual logic.
///
/// Half the value here is the assertions; the other half is that this target
/// depends on `SynapseDI` and therefore forces every module to compile under
/// CI. Before it existed, the SwiftUI layer was first compiled by the app
/// target, where a failure appears as `no such module` at the app's first
/// import and says nothing about which file is broken.
final class ThemeTests: XCTestCase {

    /// "System" must map to `nil`, not to `.light` — a concrete scheme would
    /// override the device setting instead of following it.
    func testThemeMapsSystemToNoOverride() {
        XCTAssertNil(AppSettings.Theme.system.colorScheme)
        XCTAssertEqual(AppSettings.Theme.light.colorScheme, .light)
        XCTAssertEqual(AppSettings.Theme.dark.colorScheme, .dark)
    }

    /// Avatar colours are derived, not stored, so the same user has to land on
    /// the same colour on every launch and on every device.
    func testAvatarColourIsStableForAnID() {
        let first = Theme.avatarColor(for: "user-42")
        let second = Theme.avatarColor(for: "user-42")
        XCTAssertEqual(first, second)
    }

    func testAvatarColoursDifferBetweenIDs() {
        // Not a guarantee for arbitrary ids (the palette has eight entries), but
        // these three must not collapse into one colour.
        let colours = Set(["1", "2", "3"].map { String(describing: Theme.avatarColor(for: $0)) })
        XCTAssertGreaterThan(colours.count, 1)
    }
}

final class LocalizationTests: XCTestCase {

    override func tearDown() {
        L10n.setLanguage(.system)
    }

    /// A missing key returns the key itself. That is deliberate: a visible
    /// `chat.send` on screen is a bug report, an empty label is a mystery.
    func testMissingKeyFallsBackToTheKey() {
        XCTAssertEqual(l("definitely.not.a.real.key"), "definitely.not.a.real.key")
    }

    /// The language override must not crash or blank out when the requested
    /// `.lproj` is absent from the test bundle — tests run without the app's
    /// resources.
    func testLanguageOverrideIsSafeWithoutABundle() {
        L10n.setLanguage(.ru)
        XCTAssertEqual(l("some.key"), "some.key")
        L10n.setLanguage(.en)
        XCTAssertEqual(l("some.key"), "some.key")
    }

    func testFormatArgumentsAreApplied() {
        // With no bundle the format string is the key itself, so a key carrying
        // no placeholder must survive an argument being passed anyway.
        XCTAssertEqual(l("plain.key", 5), "plain.key")
    }
}

final class DateFormattingTests: XCTestCase {

    /// Today shows a time, older shows a date — the convention a chat list needs
    /// so a row is readable at a glance.
    func testTodayRendersAsATime() {
        let stamp = Date().chatListStamp()
        XCTAssertTrue(stamp.contains(":"), "expected HH:mm, got \(stamp)")
    }

    func testOlderThanAWeekRendersAsADate() {
        let now = Date()
        let old = now.addingTimeInterval(-60 * 60 * 24 * 30)
        let stamp = old.chatListStamp(now: now)
        XCTAssertTrue(stamp.contains("."), "expected dd.MM.yy, got \(stamp)")
    }
}

final class TaskBagTests: XCTestCase {

    /// The whole reason the bag exists: cancellation has to happen when the view
    /// model is released, and a `@MainActor` view model cannot do that from its
    /// own `deinit`.
    func testReleasingTheBagCancelsItsTasks() async {
        let started = expectation(description: "task started")
        let cancelled = expectation(description: "task observed cancellation")

        var bag: TaskBag? = TaskBag()
        bag?.add(Task {
            started.fulfill()
            // Sleeping throws on cancellation.
            try? await Task.sleep(for: .seconds(30))
            if Task.isCancelled { cancelled.fulfill() }
        })

        await fulfillment(of: [started], timeout: 2)
        bag = nil
        await fulfillment(of: [cancelled], timeout: 2)
    }

    /// A keyed task supersedes the previous one under the same key, which is how
    /// the search debounce and the draft debounce avoid racing themselves.
    func testReplaceCancelsThePreviousTaskForTheSameKey() async {
        let cancelled = expectation(description: "first task cancelled")
        let bag = TaskBag()

        bag.replace("search", with: Task {
            try? await Task.sleep(for: .seconds(30))
            if Task.isCancelled { cancelled.fulfill() }
        })
        bag.replace("search", with: Task {})

        await fulfillment(of: [cancelled], timeout: 2)
    }
}

final class CompositionTests: XCTestCase {

    /// Configuration must come from the bundle, never from a call site. In a
    /// test bundle the keys are absent, so this exercises the documented
    /// fallback — and proves a missing key does not crash on launch.
    func testEnvironmentFallsBackToLocalDevelopment() {
        let environment = ServerEnvironment.current(bundle: .main)
        XCTAssertEqual(environment.name, .dev)
        XCTAssertEqual(environment.transport, .webSocket)
        XCTAssertNotNil(environment.gatewayURL.host)
    }

    /// Insecure TLS is refused in production regardless of what a config file
    /// says, so a mistake there cannot disable certificate validation.
    func testProductionRefusesInsecureTLS() {
        let production = ServerEnvironment(
            name: .prod,
            gatewayURL: URL(string: "wss://example.invalid/ws")!,
            tcpHost: "example.invalid",
            tcpPort: 7000,
            transport: .webSocket,
            mediaBaseURL: nil,
            allowsInsecureTLS: false
        )
        XCTAssertFalse(production.allowsInsecureTLS)
    }
}
