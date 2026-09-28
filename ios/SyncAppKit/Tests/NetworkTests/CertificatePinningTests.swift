import XCTest
@testable import SyncAppNetwork

/// What the pinning policy does with its CONFIGURATION.
///
/// The trust evaluation itself needs a live TLS handshake and a real certificate
/// chain, which belongs in an integration test against stage. Everything here is the
/// part that decides whether pinning runs at all — and that is where a pinning bug
/// actually hurts, because every mistake in it is silent: the app keeps connecting and
/// nothing reports that the pins are being ignored.
final class PinningPolicyTests: XCTestCase {

    private let pinA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
    private let pinB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="

    func testTwoPinsEnforce() {
        let policy = PinningPolicy.resolve(
            host: "syncapp.example",
            pins: [pinA, pinB],
            expiresAt: nil
        )
        XCTAssertEqual(policy?.host, "syncapp.example")
        XCTAssertEqual(policy?.pins, [pinA, pinB])
    }

    func testOnePinIsRefused() {
        // Not an oversight to tolerate. A single pin is a key that cannot be rotated:
        // the day it changes, every installed build is locked out until a new one
        // clears review. Treating it as "off" makes the misconfiguration visible as
        // "pinning is not running" rather than as a time bomb.
        XCTAssertNil(PinningPolicy.resolve(host: "syncapp.example", pins: [pinA], expiresAt: nil))
    }

    func testDuplicatePinsDoNotCountAsTwo() {
        // Copy-paste in an .xcconfig produces exactly this, and a set of size one
        // after deduplication is a single pin however many times it was written.
        XCTAssertNil(PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, pinA], expiresAt: nil))
    }

    func testBlankEntriesAreIgnored() {
        // A trailing comma in `SYNCAPP_TLS_PINS` yields an empty element. Counting it
        // would let one real pin look like two, which is the failure this guards.
        XCTAssertNil(
            PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, "", "   "], expiresAt: nil)
        )
    }

    func testWhitespaceAroundPinsIsTrimmed() {
        let policy = PinningPolicy.resolve(
            host: "syncapp.example",
            pins: ["  \(pinA)", "\(pinB)  "],
            expiresAt: nil
        )
        XCTAssertEqual(policy?.pins, [pinA, pinB])
    }

    func testMissingHostDisablesPinning() {
        // `URL.host` is nil for a malformed gateway URL. Pinning against no host would
        // either match nothing or everything, depending on how `governs` was written.
        XCTAssertNil(PinningPolicy.resolve(host: nil, pins: [pinA, pinB], expiresAt: nil))
        XCTAssertNil(PinningPolicy.resolve(host: "", pins: [pinA, pinB], expiresAt: nil))
    }

    func testExpiryFallsBackToCAValidation() {
        let past = Date(timeIntervalSince1970: 1_000)
        let now = Date(timeIntervalSince1970: 2_000)
        XCTAssertNil(
            PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, pinB], expiresAt: past, now: now)
        )
    }

    func testExpiryIsInclusive() {
        // At the boundary, off. An off-by-one here means the pins are enforced for one
        // extra moment on the day the key is expected to have rotated.
        let at = Date(timeIntervalSince1970: 2_000)
        XCTAssertNil(
            PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, pinB], expiresAt: at, now: at)
        )
    }

    func testUnexpiredPinsStillEnforce() {
        let future = Date(timeIntervalSince1970: 3_000)
        let now = Date(timeIntervalSince1970: 2_000)
        XCTAssertNotNil(
            PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, pinB], expiresAt: future, now: now)
        )
    }

    func testHostMatchIsCaseInsensitiveButNotAWildcard() {
        guard let policy = PinningPolicy.resolve(
            host: "syncapp.example",
            pins: [pinA, pinB],
            expiresAt: nil
        ) else { return XCTFail("policy should resolve") }

        XCTAssertTrue(policy.governs("SyncApp.Example"), "DNS is case-insensitive")
        // A subdomain is a DIFFERENT key. Accepting it would mean pinning promises
        // something about hosts that do not exist yet.
        XCTAssertFalse(policy.governs("cdn.syncapp.example"))
        XCTAssertFalse(policy.governs("syncapp.example.attacker.test"))
    }

    func testAnUnpinnedHostIsLeftToTheSystem() {
        let policy = PinningPolicy.resolve(host: "syncapp.example", pins: [pinA, pinB], expiresAt: nil)
        // Media may live on another host. The gateway's pins must not govern a CDN
        // nobody pinned, or every download fails the moment pinning is switched on.
        XCTAssertFalse(TrustEvaluator.appliesToHost("cdn.example", policy: policy))
        XCTAssertTrue(TrustEvaluator.appliesToHost("syncapp.example", policy: policy))
    }

    func testNoPolicyMeansNoPinCheck() {
        XCTAssertFalse(TrustEvaluator.appliesToHost("syncapp.example", policy: nil))
    }
}

// MARK: - Environment plumbing

/// The pins have to reach the socket, and the host they are keyed on differs per
/// transport. This is the wiring, tested separately from the policy.
final class ServerEnvironmentPinningTests: XCTestCase {

    private func environment(
        transport: TransportKind,
        pins: [String],
        expiresAt: Date? = nil
    ) -> ServerEnvironment {
        ServerEnvironment(
            name: .prod,
            gatewayURL: url("wss://gateway.syncapp.example/ws"),
            tcpHost: "tcp.syncapp.example",
            tcpPort: 7000,
            transport: transport,
            mediaBaseURL: nil,
            allowsInsecureTLS: false,
            tlsPins: pins,
            tlsPinsExpireAt: expiresAt
        )
    }

    func testPolicyIsKeyedOnTheHostActuallyDialled() {
        let env = environment(transport: .webSocket, pins: ["a=", "b="])
        // The two transports reach different names, and the WebSocket path uses the
        // gateway URL's host while the TCP path dials `tcpHost`. A policy keyed on the
        // wrong one of those enforces nothing at all — `governs` simply returns false
        // and every connection silently falls through to CA validation.
        XCTAssertEqual(env.pinningPolicy(for: env.gatewayURL.host)?.host, "gateway.syncapp.example")
        XCTAssertEqual(env.pinningPolicy(for: env.tcpHost)?.host, "tcp.syncapp.example")
    }

    func testAnEmptyPinListLeavesTheEnvironmentUnpinned() {
        XCTAssertNil(environment(transport: .webSocket, pins: []).pinningPolicy(for: "gateway.syncapp.example"))
    }

    func testInfoPlistParsingTakesCommaSeparatedPins() {
        // What `ServerEnvironment.current` does with `SYNCAPP_TLS_PINS`. Asserted
        // here because the split is the step between an .xcconfig line and a policy,
        // and a build variable that fails to substitute yields "$(SYNCAPP_TLS_PINS)"
        // — one non-empty element, which must not count as a pin set.
        let raw = "$(SYNCAPP_TLS_PINS)"
        let parsed = raw.split(separator: ",").map(String.init).filter { !$0.isEmpty }
        XCTAssertNil(PinningPolicy.resolve(host: "syncapp.example", pins: parsed, expiresAt: nil))
    }
}
