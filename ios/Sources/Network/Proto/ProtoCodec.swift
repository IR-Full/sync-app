import Foundation

// A minimal, hand-written proto3 codec.
//
// Why not SwiftProtobuf: the bodies we need are 40-odd flat messages of scalars
// and strings, and the alternative is a build-time protoc + plugin step that
// nobody can run from the repo as checked out. A reader/writer pair plus one
// conformance per body is less machinery than the generator it replaces, and it
// keeps the package dependency-free.
//
// The forward-compatibility rule that matters is implemented in `ProtoReader`:
// an unknown field number is *skipped by wire type*, never rejected. A server
// that adds a field to `NewMessage` tomorrow must not break a client shipped
// today.

/// Protobuf wire types. Only these three appear in `body.proto` — there are no
/// fixed32/fixed64 fields in the schema.
public enum ProtoWireType: Int, Sendable {
    case varint = 0
    case fixed64 = 1
    case lengthDelimited = 2
    case fixed32 = 5
}

// MARK: - Writer

/// Serializes proto3 bodies. Follows proto3's implicit-presence rule: a field
/// holding its default value is *not emitted*. That is not an optimization, it
/// is the encoding — a receiver materializes defaults for anything absent.
public struct ProtoWriter {
    public private(set) var data = Data()

    public init() {}

    private mutating func tag(_ field: Int, _ type: ProtoWireType) {
        Varint.encode(UInt64(field << 3 | type.rawValue), into: &data)
    }

    public mutating func string(_ field: Int, _ value: String) {
        guard !value.isEmpty else { return }
        let utf8 = Data(value.utf8)
        tag(field, .lengthDelimited)
        Varint.encode(UInt64(utf8.count), into: &data)
        data.append(utf8)
    }

    public mutating func bool(_ field: Int, _ value: Bool) {
        guard value else { return }
        tag(field, .varint)
        Varint.encode(1, into: &data)
    }

    public mutating func uint32(_ field: Int, _ value: UInt32) {
        guard value != 0 else { return }
        tag(field, .varint)
        Varint.encode(UInt64(value), into: &data)
    }

    public mutating func uint64(_ field: Int, _ value: UInt64) {
        guard value != 0 else { return }
        tag(field, .varint)
        Varint.encode(value, into: &data)
    }

    /// proto3 sign-extends a negative `int32` to 64 bits, so it costs 10 bytes.
    /// None of our int32 fields are ever negative, but encoding it correctly is
    /// cheaper than reasoning about whether that stays true.
    public mutating func int32(_ field: Int, _ value: Int32) {
        guard value != 0 else { return }
        tag(field, .varint)
        Varint.encode(UInt64(bitPattern: Int64(value)), into: &data)
    }

    public mutating func int64(_ field: Int, _ value: Int64) {
        guard value != 0 else { return }
        tag(field, .varint)
        Varint.encode(UInt64(bitPattern: value), into: &data)
    }

    public mutating func message<T: ProtoEncodable>(_ field: Int, _ value: T?) {
        guard let value else { return }
        let encoded = value.protoEncoded()
        tag(field, .lengthDelimited)
        Varint.encode(UInt64(encoded.count), into: &data)
        data.append(encoded)
    }

    public mutating func repeatedString(_ field: Int, _ values: [String]) {
        for value in values {
            // Unlike a singular field, an empty element in a repeated field is
            // meaningful, so it is written rather than skipped.
            let utf8 = Data(value.utf8)
            tag(field, .lengthDelimited)
            Varint.encode(UInt64(utf8.count), into: &data)
            data.append(utf8)
        }
    }

    public mutating func repeatedMessage<T: ProtoEncodable>(_ field: Int, _ values: [T]) {
        for value in values { message(field, value) }
    }

    /// proto3 packs repeated scalars by default (one length-delimited run).
    public mutating func packedInt32(_ field: Int, _ values: [Int32]) {
        guard !values.isEmpty else { return }
        var payload = Data()
        for value in values { Varint.encode(UInt64(bitPattern: Int64(value)), into: &payload) }
        tag(field, .lengthDelimited)
        Varint.encode(UInt64(payload.count), into: &data)
        data.append(payload)
    }

    /// `map<string, int32>` is sugar for a repeated message of {1: key, 2: value}.
    public mutating func stringInt32Map(_ field: Int, _ values: [String: Int32]) {
        // Sorted so the same map always produces identical bytes — golden-file
        // tests and request dedup both depend on that.
        for key in values.keys.sorted() {
            var entry = ProtoWriter()
            entry.string(1, key)
            entry.int32(2, values[key] ?? 0)
            tag(field, .lengthDelimited)
            Varint.encode(UInt64(entry.data.count), into: &data)
            data.append(entry.data)
        }
    }
}

// MARK: - Reader

/// One field header read off the wire.
public struct ProtoField: Sendable {
    public let number: Int
    public let wireType: ProtoWireType
}

/// Parses proto3 bodies. Callers loop `while let field = try reader.next()` and
/// switch on `field.number`, ending with `try reader.skip(field)` in `default`.
public struct ProtoReader {
    private let bytes: [UInt8]
    private var offset: Int
    private let end: Int

    public init(_ data: Data) {
        self.bytes = [UInt8](data)
        self.offset = 0
        self.end = self.bytes.count
    }

    private init(bytes: [UInt8], offset: Int, end: Int) {
        self.bytes = bytes
        self.offset = offset
        self.end = end
    }

    public mutating func next() throws -> ProtoField? {
        guard offset < end else { return nil }
        let key = try Varint.decode(bytes, &offset)
        guard let wireType = ProtoWireType(rawValue: Int(key & 0x07)) else {
            throw WireError.malformedBody("unknown wire type \(key & 0x07)")
        }
        let number = Int(key >> 3)
        guard number > 0 else { throw WireError.malformedBody("field number 0") }
        return ProtoField(number: number, wireType: wireType)
    }

    // MARK: scalars

    public mutating func uint64() throws -> UInt64 { try Varint.decode(bytes, &offset) }
    public mutating func uint32() throws -> UInt32 { UInt32(truncatingIfNeeded: try Varint.decode(bytes, &offset)) }
    public mutating func int64() throws -> Int64 { Int64(bitPattern: try Varint.decode(bytes, &offset)) }
    public mutating func int32() throws -> Int32 { Int32(truncatingIfNeeded: Int64(bitPattern: try Varint.decode(bytes, &offset))) }
    public mutating func bool() throws -> Bool { try Varint.decode(bytes, &offset) != 0 }

    public mutating func string() throws -> String {
        let slice = try lengthDelimited()
        // Invalid UTF-8 from a peer is data corruption, not a reason to drop the
        // whole message: replace the bad scalars and keep going.
        return String(decoding: slice, as: UTF8.self)
    }

    public mutating func bytesField() throws -> Data {
        Data(try lengthDelimited())
    }

    public mutating func message<T: ProtoDecodable>(_ type: T.Type = T.self) throws -> T {
        let range = try lengthDelimitedRange()
        var sub = ProtoReader(bytes: bytes, offset: range.lowerBound, end: range.upperBound)
        return try T(from: &sub)
    }

    /// Reads a repeated int32 element, accepting both the packed encoding
    /// (proto3's default) and the unpacked one an older writer might emit.
    public mutating func repeatedInt32(_ field: ProtoField, into out: inout [Int32]) throws {
        switch field.wireType {
        case .varint:
            out.append(try int32())
        case .lengthDelimited:
            let range = try lengthDelimitedRange()
            var cursor = range.lowerBound
            while cursor < range.upperBound {
                let raw = try Varint.decode(bytes, &cursor)
                out.append(Int32(truncatingIfNeeded: Int64(bitPattern: raw)))
            }
        default:
            throw WireError.malformedBody("bad wire type for repeated int32")
        }
    }

    /// Reads one `map<string, int32>` entry into `out`.
    public mutating func stringInt32MapEntry(into out: inout [String: Int32]) throws {
        let range = try lengthDelimitedRange()
        var sub = ProtoReader(bytes: bytes, offset: range.lowerBound, end: range.upperBound)
        var key = ""
        var value: Int32 = 0
        while let field = try sub.next() {
            switch field.number {
            case 1: key = try sub.string()
            case 2: value = try sub.int32()
            default: try sub.skip(field)
            }
        }
        out[key] = value
    }

    /// Skips a field we do not model. This is the forward-compatibility hinge:
    /// every unknown field must be skippable from its wire type alone.
    public mutating func skip(_ field: ProtoField) throws {
        switch field.wireType {
        case .varint:
            _ = try Varint.decode(bytes, &offset)
        case .fixed64:
            try advance(8)
        case .fixed32:
            try advance(4)
        case .lengthDelimited:
            _ = try lengthDelimitedRange()
        }
    }

    // MARK: internals

    private mutating func advance(_ n: Int) throws {
        guard offset + n <= end else { throw WireError.malformedBody("truncated field") }
        offset += n
    }

    private mutating func lengthDelimitedRange() throws -> Range<Int> {
        let length = Int(try Varint.decode(bytes, &offset))
        guard length >= 0, offset + length <= end else {
            throw WireError.malformedBody("truncated length-delimited field")
        }
        let range = offset..<(offset + length)
        offset += length
        return range
    }

    private mutating func lengthDelimited() throws -> ArraySlice<UInt8> {
        bytes[try lengthDelimitedRange()]
    }
}

// MARK: - Conformances

public protocol ProtoEncodable {
    func encode(to writer: inout ProtoWriter)
}

public protocol ProtoDecodable {
    init(from reader: inout ProtoReader) throws
}

/// A body that travels in an envelope in both directions.
public typealias ProtoMessage = ProtoEncodable & ProtoDecodable

extension ProtoEncodable {
    public func protoEncoded() -> Data {
        var writer = ProtoWriter()
        encode(to: &writer)
        return writer.data
    }
}

extension ProtoDecodable {
    public static func protoDecoded(from data: Data) throws -> Self {
        var reader = ProtoReader(data)
        return try Self(from: &reader)
    }
}
