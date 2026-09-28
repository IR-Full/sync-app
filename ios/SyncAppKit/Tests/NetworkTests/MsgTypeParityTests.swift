import XCTest

@testable import SyncAppNetwork

/// Holds this client's message types against the server's.
///
/// The numbers here are not ours. They are `server/pkg/wire/constants.go`, and a
/// client that disagrees with it by one does not fail loudly — it decodes a
/// `PINNED` frame as a `DRAFTS` one, or drops a type it never learned about,
/// because the protocol's own extensibility rule is that an unknown type is
/// skipped in silence. That rule is what makes drift here expensive to notice and
/// cheap to introduce, which is the case for checking it mechanically.
///
/// Android has had this check for a while; this platform did not, and it drifted
/// exactly as far as an unchecked list does. It was missing seven types — account
/// deletion, all four session-management messages and the batched history page —
/// so a lost phone could not be signed out from here and an account could not be
/// deleted, while every other surface of the app looked complete. That is the
/// whole argument for the test: nothing else was ever going to say so.
///
/// The comparison is on (number → name) PAIRS, and both halves come from the
/// server: the number from `constants.go`, the name from `MsgType.String()` in
/// `types.go`. Deriving the name from the Go identifier instead would be a guess,
/// and a wrong one — `MsgTransportAck` renders as `T_ACK` — so both halves are
/// read. The files are read rather than copied: a copy would be one more thing to
/// keep in step, and it would agree with itself forever.
final class MsgTypeParityTests: XCTestCase {

    /// The server's wire package, found by walking up from this source file rather
    /// than from the working directory: `swift test` runs from `ios/SyncAppKit` in
    /// CI and from the repository root by hand, and a fixed relative path that
    /// works from one silently skips the whole test from the other.
    private static let serverWire: URL? = {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.pathComponents.count > 1 {
            let candidate = dir.appendingPathComponent("server/pkg/wire")
            if FileManager.default.fileExists(
                atPath: candidate.appendingPathComponent("constants.go").path
            ) {
                return candidate
            }
            let parent = dir.deletingLastPathComponent()
            if parent == dir { break }
            dir = parent
        }
        return nil
    }()

    private func read(_ file: String) throws -> String {
        let dir = try XCTUnwrap(Self.serverWire)
        return try String(contentsOf: dir.appendingPathComponent(file), encoding: .utf8)
    }

    private func captures(_ pattern: String, in text: String) throws -> [[String]] {
        let regex = try NSRegularExpression(pattern: pattern, options: [.anchorsMatchLines])
        return regex.matches(in: text, range: NSRange(text.startIndex..., in: text)).map { match in
            (1..<match.numberOfRanges).map { index in
                Range(match.range(at: index), in: text).map { String(text[$0]) } ?? ""
            }
        }
    }

    /// Go identifier → number. `Reserved` is 0, which is "no type at all".
    private func serverNumbers() throws -> [String: Int] {
        var result: [String: Int] = [:]
        for groups in try captures(#"^\s*Msg(\w+)\s+MsgType = (\d+)"#, in: try read("constants.go")) {
            guard groups[0] != "Reserved", let number = Int(groups[1]) else { continue }
            result[groups[0]] = number
        }
        return result
    }

    /// Go identifier → the name the server puts in its logs and metric labels.
    private func serverNames() throws -> [String: String] {
        var result: [String: String] = [:]
        for groups in try captures(#"case Msg(\w+):\s*\n\s*return "([A-Z0-9_]+)""#, in: try read("types.go")) {
            result[groups[0]] = groups[1]
        }
        return result
    }

    /// number → name, as the server defines the pair.
    private func serverTypes() throws -> [Int: String] {
        let names = try serverNames()
        var result: [Int: String] = [:]
        for (identifier, number) in try serverNumbers() {
            // A declared type with no branch in String() is the server's own problem,
            // and server/pkg/wire/names_test.go is what catches it. The guard test
            // below makes sure it cannot pass unnoticed here either.
            if let name = names[identifier] { result[number] = name }
        }
        return result
    }

    /// number → name, as this client defines the pair. `unknown` is excluded: it is
    /// this build's own sentinel for a number the server sent and it has never heard
    /// of, not a type the server declares.
    private func clientTypes() -> [Int: String] {
        var result: [Int: String] = [:]
        for type in MsgType.allCases where type != .unknown {
            result[Int(type.rawValue)] = type.name
        }
        return result
    }

    func testEveryServerMessageTypeExistsHereUnderTheSameNumberAndName() throws {
        // Skipped rather than failed when the server tree is absent: this package
        // must stay buildable on its own, and a test that cannot run is not the same
        // as one that found a problem.
        try XCTSkipIf(Self.serverWire == nil, "server/pkg/wire not present")

        let client = clientTypes()
        let disagreements = try serverTypes().compactMap { number, name -> String? in
            switch client[number] {
            case name: return nil
            case nil: return "\(number) (\(name)) is missing here"
            case let mine?: return "\(number) is \"\(mine)\" here, \"\(name)\" on the server"
            }
        }

        XCTAssertEqual(disagreements.sorted(), [], "message types drifted from the server")
    }

    /// Every type the server declares has a name in its own table. Asserted because
    /// the parity check above TRUSTS it: a type the name table forgot would drop
    /// silently out of the comparison rather than fail it.
    func testTheServerNamesEveryTypeItDeclares() throws {
        try XCTSkipIf(Self.serverWire == nil, "server/pkg/wire not present")

        let names = try serverNames()
        let unnamed = try serverNumbers().keys.filter { names[$0] == nil }

        XCTAssertEqual(
            unnamed.sorted(), [],
            "server types with no branch in MsgType.String(); this test cannot see them"
        )
    }

    /// A type without a name renders as `UNKNOWN` in a log, which is exactly the
    /// case where somebody is reading the log to find out what arrived.
    func testEveryTypeHasAName() {
        let unnamed = MsgType.allCases.filter { $0 != .unknown && $0.name == "UNKNOWN" }

        XCTAssertEqual(unnamed.map(\.rawValue).sorted(), [], "types falling through MsgType.name")
    }

    /// Two types with one name is worse than a missing one: a metric or a log filter
    /// keyed on the name silently merges two different messages.
    func testNoTwoTypesShareAName() {
        let names = MsgType.allCases.filter { $0 != .unknown }.map(\.name)

        XCTAssertEqual(names.count, Set(names).count, "one name used for two types")
    }

    /// The body table is derived from the gateway's handlers, and a type added to
    /// `MsgType` without one decodes to nothing — the frame arrives, is understood to
    /// be a `PINNED`, and carries no payload. Only the genuinely empty messages and
    /// the bodiless control frames are allowed to be absent.
    func testEveryTypeWithABodyIsRegisteredInTheCodec() {
        let bodiless: Set<String> = [
            // Control frames: the type is the whole message.
            "PING", "PONG", "T_ACK",
            // Empty protobuf messages: the request names itself and says nothing
            // more. SESSION_LIST asks for the caller's own sessions and PRIVACY_GET
            // for the caller's own settings — a field that could name a target would
            // leak exactly what the setting exists to protect.
            "SESSION_LIST", "PRIVACY_GET",
            // Same shape, three more: TOTP_SETUP asks the server to mint a secret and
            // names nothing; BILLING_STATUS and BILLING_CANCEL always concern the
            // caller's own subscription, and a field naming a target would be a field
            // for reading somebody else's.
            "TOTP_SETUP", "BILLING_STATUS", "BILLING_CANCEL",
        ]

        let missing = MsgType.allCases
            .filter { $0 != .unknown && !bodiless.contains($0.name) }
            .filter { !BodyRegistry.hasBody($0) }

        XCTAssertTrue(missing.isEmpty, "no body registered for: \(missing.map(\.name).sorted())")
    }
}
