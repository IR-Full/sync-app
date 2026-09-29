import XCTest

/// Holds the hand-written bodies against `server/proto/syncapp/v1/body.proto`.
///
/// Protobuf puts only field NUMBERS and wire types on the wire. Swap two numbers,
/// or read a varint where the schema says bytes, and our own round-trip still
/// passes — both halves are wrong the same way — while every real server frame
/// decodes into the wrong fields. So this reads the Swift sources and the schema
/// and compares them, in both directions:
///
///   - every number a body writes or reads exists in its message, with the same
///     type;
///   - a body writes exactly the numbers it reads;
///   - every field of a message we carry is handled — a field the schema grew and
///     this client ignores decodes to its default and reads as "not sent".
///
/// The Android client has the same check (`BodySchemaParityTest.kt`).
final class BodySchemaParityTests: XCTestCase {

    private typealias Fields = [Int: (name: String, kind: String)]

    /// Swift writer methods, as the proto type they encode.
    private static let writerKinds: [String: String] = [
        "string": "string", "bytes": "bytes", "bool": "bool",
        "uint32": "uint32", "uint64": "uint64", "int32": "int32", "int64": "int64",
        "message": "message", "repeatedMessage": "repeated message",
        "repeatedString": "repeated string", "packedInt32": "repeated int32",
        "stringInt32Map": "map",
    ]

    private static let scalars: Set<String> = [
        "string", "bytes", "bool", "int32", "int64", "uint32", "uint64",
        "sint32", "sint64", "double", "float", "fixed32", "fixed64",
    ]

    /// Finds `relative` by walking up from this file, so the test runs from
    /// `ios/SyncAppKit` in CI and from the repository root by hand.
    private func locate(_ relative: String) throws -> URL {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.pathComponents.count > 1 {
            let candidate = dir.appendingPathComponent(relative)
            if FileManager.default.fileExists(atPath: candidate.path) { return candidate }
            dir = dir.deletingLastPathComponent()
        }
        XCTFail("\(relative) not found above \(#filePath)")
        throw CocoaError(.fileNoSuchFile)
    }

    private func matches(_ pattern: String, in text: String, multiline: Bool = true) throws -> [[String]] {
        var options: NSRegularExpression.Options = [.anchorsMatchLines]
        if multiline { options.insert(.dotMatchesLineSeparators) }
        let regex = try NSRegularExpression(pattern: pattern, options: options)
        return regex.matches(in: text, range: NSRange(text.startIndex..., in: text)).map { match in
            (0..<match.numberOfRanges).map { index in
                Range(match.range(at: index), in: text).map { String(text[$0]) } ?? ""
            }
        }
    }

    private func parseProto() throws -> [String: Fields] {
        let text = try String(contentsOf: locate("server/proto/syncapp/v1/body.proto"), encoding: .utf8)
        var messages: [String: Fields] = [:]
        for message in try matches(#"\bmessage (\w+)\s*\{([^}]*)\}"#, in: text) {
            var fields: Fields = [:]
            let pattern = #"^\s*(repeated\s+|optional\s+)?(map<[^>]+>|[\w.]+)\s+(\w+)\s*=\s*(\d+)"#
            for field in try matches(pattern, in: message[2], multiline: false) {
                let repeated = field[1].trimmingCharacters(in: .whitespaces) == "repeated" ? "repeated " : ""
                let type = field[2]
                let kind: String
                if type.hasPrefix("map<") {
                    kind = "map"
                } else if Self.scalars.contains(type) {
                    kind = repeated + type
                } else {
                    kind = repeated + "message"
                }
                if let number = Int(field[4]) { fields[number] = (field[3], kind) }
            }
            messages[message[1]] = fields
        }
        return messages
    }

    private struct SwiftBody {
        var writes: [Int: String] = [:]
        var reads: Set<Int> = []
    }

    private func parseSwift() throws -> [String: SwiftBody] {
        let dir = try locate("ios/SyncAppKit/Sources/Network/Proto")
        let files = try FileManager.default.contentsOfDirectory(atPath: dir.path)
            .filter { $0.hasPrefix("Bodies") && $0.hasSuffix(".swift") }
            .sorted()
        var bodies: [String: SwiftBody] = [:]
        for file in files {
            let text = try String(contentsOf: dir.appendingPathComponent(file), encoding: .utf8)
            for type in try matches(#"^public struct (\w+):[^\n]*ProtoMessage[^\n]*\{(.*?)^\}"#, in: text) {
                var body = SwiftBody()
                let block = type[2]
                if let encode = try matches(#"func encode\(to w: inout ProtoWriter\) \{(.*?)^    \}"#, in: block).first {
                    for write in try matches(#"\bw\.(\w+)\((\d+),"#, in: encode[1], multiline: false) {
                        if let number = Int(write[2]) {
                            body.writes[number] = Self.writerKinds[write[1]] ?? "unknown writer \(write[1])"
                        }
                    }
                }
                if let decode = try matches(#"init\(from r: inout ProtoReader\) throws \{(.*?)^    \}"#, in: block).first {
                    for clause in try matches(#"^\s*case ([\d,\s]+):"#, in: decode[1], multiline: false) {
                        for number in clause[1].split(separator: ",") {
                            if let n = Int(number.trimmingCharacters(in: .whitespaces)) { body.reads.insert(n) }
                        }
                    }
                }
                bodies[type[1]] = body
            }
        }
        return bodies
    }

    /// `HelloBody` carries `Hello`; nested types like `Attachment` share the name.
    private func messageName(for swiftType: String, in proto: [String: Fields]) -> String? {
        if swiftType.hasSuffix("Body"), proto[String(swiftType.dropLast(4))] != nil {
            return String(swiftType.dropLast(4))
        }
        return proto[swiftType] != nil ? swiftType : nil
    }

    func testTheParsersFoundSomethingToCheck() throws {
        // A pattern that silently matched nothing would make every check below
        // pass for the wrong reason.
        XCTAssertGreaterThan(try parseSwift().count, 80)
        XCTAssertGreaterThan(try parseProto().count, 100)
    }

    func testEveryBodyMatchesTheSchema() throws {
        let proto = try parseProto()
        var problems: [String] = []
        for (swiftType, body) in try parseSwift().sorted(by: { $0.key < $1.key }) {
            guard let name = messageName(for: swiftType, in: proto), let fields = proto[name] else {
                problems.append("\(swiftType) has no message in body.proto")
                continue
            }
            let written = Set(body.writes.keys)
            if written != body.reads {
                problems.append("\(swiftType) writes \(written.subtracting(body.reads).sorted()) "
                    + "without reading them, reads \(body.reads.subtracting(written).sorted()) without writing them")
            }
            for (number, kind) in body.writes.sorted(by: { $0.key < $1.key }) {
                guard let field = fields[number] else {
                    problems.append("\(swiftType) writes field \(number), which \(name) does not have")
                    continue
                }
                if field.kind != kind {
                    problems.append("\(name).\(field.name) (\(number)) is \(field.kind) in body.proto, \(kind) here")
                }
            }
            for (number, field) in fields.sorted(by: { $0.key < $1.key })
            where body.writes[number] == nil && !body.reads.contains(number) {
                problems.append("\(name).\(field.name) (\(number)) is in body.proto and not in \(swiftType)")
            }
        }
        XCTAssertEqual(problems, [], "bodies drifted from body.proto")
    }
}
