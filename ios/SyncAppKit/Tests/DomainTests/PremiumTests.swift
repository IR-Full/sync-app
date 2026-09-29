import XCTest
@testable import SyncAppDomain

final class PremiumActiveTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    func testFreeIsNotPremium() {
        XCTAssertFalse(Entitlements(plan: "free").isPremiumActive(now: now))
    }

    func testUnknownPlanNameIsNotProofOfPayment() {
        XCTAssertFalse(Entitlements(plan: "enterprise").isPremiumActive(now: now))
        XCTAssertFalse(Entitlements(plan: "").isPremiumActive(now: now))
    }

    func testPremiumWithinThePeriodIsActive() {
        let ent = Entitlements(plan: "premium", periodEnd: now.addingTimeInterval(3600))
        XCTAssertTrue(ent.isPremiumActive(now: now))
    }

    func testPremiumWithoutAPeriodEndIsActive() {
        XCTAssertTrue(Entitlements(plan: "premium").isPremiumActive(now: now))
    }

    func testCancelledSubscriptionKeepsAccessUntilThePeriodEnds() {
        let ent = Entitlements(
            plan: "premium",
            periodEnd: now.addingTimeInterval(3600),
            cancelAtPeriodEnd: true
        )
        XCTAssertTrue(ent.isPremiumActive(now: now))
    }

    func testLapsedPeriodIsNotActive() {
        let ent = Entitlements(plan: "premium", periodEnd: now.addingTimeInterval(-1))
        XCTAssertFalse(ent.isPremiumActive(now: now))
    }

    /// The deployment-without-billing case: every feature granted on `free`. The
    /// account is not paying, whatever it is allowed to do.
    func testGrantedFeaturesDoNotMakeAnAccountPaying() {
        let ent = Entitlements(plan: "free", secretChats: true, badge: true, customThemes: true)
        XCTAssertFalse(ent.isPremiumActive(now: now))
    }
}

final class AppSettingsCodingTests: XCTestCase {
    func testRoundTripKeepsEveryField() throws {
        let settings = AppSettings(
            theme: .dark,
            language: .ru,
            accent: .emerald,
            pushEnabled: false,
            showTypingIndicators: false,
            localDisplayName: "Alice",
            avatarSymbol: "star"
        )
        let data = try JSONEncoder().encode(settings)
        XCTAssertEqual(try JSONDecoder().decode(AppSettings.self, from: data), settings)
    }

    /// Settings written by a build without `accent` keep the user's other choices.
    func testSettingsFromBeforeAccentKeepTheirValues() throws {
        let stored = Data("""
        {"theme":"dark","language":"ru","pushEnabled":false,
         "showTypingIndicators":true,"localDisplayName":"","avatarSymbol":""}
        """.utf8)
        let settings = try JSONDecoder().decode(AppSettings.self, from: stored)
        XCTAssertEqual(settings.theme, .dark)
        XCTAssertEqual(settings.language, .ru)
        XCTAssertFalse(settings.pushEnabled)
        XCTAssertEqual(settings.accent, .standard)
    }

    /// A palette this build does not know (written by a newer one) falls back to
    /// the default instead of discarding the whole settings object.
    func testUnknownAccentFallsBackToTheDefault() throws {
        let stored = Data(#"{"theme":"light","accent":"ocean"}"#.utf8)
        let settings = try JSONDecoder().decode(AppSettings.self, from: stored)
        XCTAssertEqual(settings.theme, .light)
        XCTAssertEqual(settings.accent, .standard)
    }

    /// The raw values are the web client's palette names, so the two agree.
    func testAccentNamesMatchTheWebClient() {
        XCTAssertEqual(
            AppSettings.Accent.allCases.map(\.rawValue),
            ["default", "violet", "emerald", "amber", "rose"]
        )
    }
}
