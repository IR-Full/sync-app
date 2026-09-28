import Foundation
import SQLite3

/// A thin actor over the SQLite that ships with iOS.
///
/// **Why not Core Data, GRDB or Realm.** The access pattern here is not
/// object-graph-shaped, it is query-shaped: "the 50 messages of chat X with
/// `seq` below this cursor, newest first" and "every chat ordered by last
/// activity, with an unread count derived from `seq > last_read_seq`". Those are
/// two indexed SQL statements. Core Data would make me express them as fetch
/// requests over a model I cannot see in a diff, and its concurrency model
/// (contexts, merge policies) is a second concurrency system layered under
/// Swift's own. GRDB is the right library for this shape — but it buys type-safe
/// row mapping over ~250 lines, in exchange for a dependency the package would
/// have to fetch, pin, and keep in step with a hand-rolled wire codec that
/// already has no dependencies. So: SQLite directly, one actor for serialised
/// access, explicit migrations.
///
/// The actor is the concurrency story. SQLite's own threading modes are a way to
/// get subtle corruption for free; a single actor means exactly one statement
/// executes at a time and the compiler enforces it.
public actor Database {
    private var handle: OpaquePointer?
    private let url: URL

    /// SQLite copies the bound bytes rather than borrowing them. Without this
    /// (i.e. with `STATIC`) every `String`/`Data` bound from Swift would be a
    /// dangling pointer by the time `step` runs.
    private static let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

    public enum DatabaseError: Error, Equatable, Sendable {
        case open(String)
        case prepare(sql: String, message: String)
        case step(sql: String, message: String)
        case migration(String)
    }

    public init(url: URL) throws {
        self.url = url
        var handle: OpaquePointer?
        let flags = SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_FULLMUTEX
        guard sqlite3_open_v2(url.path, &handle, flags, nil) == SQLITE_OK, let handle else {
            // sqlite3_open_v2 allocates a handle even on most failures, and it is
            // the only thing that can report why. Read the message, then close it:
            // `deinit` never runs for a failed `init`, so leaving it open leaks the
            // connection — once per retry, and `AppContainer.prepare` does retry.
            let message = handle.map { String(cString: sqlite3_errmsg($0)) } ?? "cannot open database"
            if let handle { sqlite3_close_v2(handle) }
            throw DatabaseError.open(message)
        }
        self.handle = handle
    }

    deinit {
        if let handle { sqlite3_close_v2(handle) }
    }

    /// Applies the pragmas that matter on a phone and then the migrations.
    ///
    /// WAL is the important one: without it a write blocks every read, and the
    /// message ingest path writes on every inbound frame while the UI is reading
    /// on every scroll. `foreign_keys` is off by default in SQLite, which is a
    /// footgun rather than a default.
    public func prepare() throws {
        try execute("PRAGMA journal_mode = WAL")
        try execute("PRAGMA synchronous = NORMAL")
        try execute("PRAGMA foreign_keys = ON")
        try execute("PRAGMA busy_timeout = 3000")

        // Versioned, forward-only migrations. `user_version` is SQLite's own
        // 4-byte header slot, so the schema version travels inside the file it
        // describes — there is no way for the two to be separated.
        var version = try userVersion()
        guard version <= Schema.migrations.count else {
            throw DatabaseError.migration("database is newer (v\(version)) than this build")
        }
        while version < Schema.migrations.count {
            try execute("BEGIN IMMEDIATE")
            do {
                try execute(Schema.migrations[version])
                version += 1
                try setUserVersion(version)
                try execute("COMMIT")
            } catch {
                try? execute("ROLLBACK")
                throw error
            }
        }
    }

    // MARK: - Statements

    public func execute(_ sql: String) throws {
        var error: UnsafeMutablePointer<CChar>?
        guard sqlite3_exec(handle, sql, nil, nil, &error) == SQLITE_OK else {
            let message = error.map { String(cString: $0) } ?? "unknown"
            sqlite3_free(error)
            throw DatabaseError.step(sql: sql, message: message)
        }
    }

    @discardableResult
    public func run(_ sql: String, _ parameters: [SQLValue] = []) throws -> Int {
        let statement = try prepareStatement(sql, parameters)
        defer { sqlite3_finalize(statement) }
        let status = sqlite3_step(statement)
        guard status == SQLITE_DONE || status == SQLITE_ROW else {
            throw DatabaseError.step(sql: sql, message: lastMessage())
        }
        return Int(sqlite3_changes(handle))
    }

    public func query(_ sql: String, _ parameters: [SQLValue] = []) throws -> [Row] {
        let statement = try prepareStatement(sql, parameters)
        defer { sqlite3_finalize(statement) }

        var rows: [Row] = []
        while true {
            let status = sqlite3_step(statement)
            if status == SQLITE_DONE { break }
            guard status == SQLITE_ROW else {
                throw DatabaseError.step(sql: sql, message: lastMessage())
            }
            rows.append(Row(statement: statement))
        }
        return rows
    }

    /// One statement plus its bindings.
    public struct Statement: Sendable {
        public let sql: String
        public let parameters: [SQLValue]

        public init(_ sql: String, _ parameters: [SQLValue] = []) {
            self.sql = sql
            self.parameters = parameters
        }
    }

    /// Applies a batch atomically, rolling back if any statement fails.
    ///
    /// Ingest uses this per history page: a page that half-applies would leave a
    /// chat's `last_seq` describing messages the cache does not hold, and the
    /// next paging request would then skip them silently.
    ///
    /// The API takes a list rather than a closure on purpose — a closure would
    /// run inside this actor while its body wants to call back into it, which
    /// Swift's isolation rules (rightly) will not allow without reentrancy.
    public func transaction(_ statements: [Statement]) throws {
        guard !statements.isEmpty else { return }
        try execute("BEGIN IMMEDIATE")
        do {
            for statement in statements {
                try run(statement.sql, statement.parameters)
            }
            try execute("COMMIT")
        } catch {
            try? execute("ROLLBACK")
            throw error
        }
    }

    public func userVersion() throws -> Int {
        let rows = try query("PRAGMA user_version")
        return rows.first?.int("user_version").map(Int.init) ?? 0
    }

    public func setUserVersion(_ version: Int) throws {
        try execute("PRAGMA user_version = \(version)")
    }

    /// Drops every row. Used by logout: a stale cache under a different account
    /// is the one bug in a messenger nobody forgives.
    /// Empties every table in the database.
    ///
    /// Enumerated from `sqlite_master` rather than from a hardcoded list, and that is
    /// the point of this method existing at all. The list version silently stopped
    /// being complete the moment a migration added a table — which it did: the
    /// ratchet-session table would have survived a logout, leaving one user's
    /// secret-chat chain keys on disk for whoever signed in next. A list that has to be
    /// edited in step with the schema is a list that will not be.
    ///
    /// `sqlite_%` is excluded because those are SQLite's own (`sqlite_sequence` and
    /// friends) and deleting from them is either an error or a corruption.
    public func wipe() throws {
        let tables = try query(
            """
            SELECT name FROM sqlite_master
             WHERE type = 'table'
               AND name NOT LIKE 'sqlite_%'
            """
        ).compactMap { $0.string("name") }

        // Foreign keys point between these tables, so the deletes have to be able to
        // happen in any order. Deferring the constraints for the transaction is
        // cheaper and less fragile than topologically sorting the schema.
        try execute("PRAGMA defer_foreign_keys = ON")
        try transaction(tables.map { Statement("DELETE FROM \"\($0)\"") })
        try execute("VACUUM")
    }

    // MARK: - Internals

    private func prepareStatement(_ sql: String, _ parameters: [SQLValue]) throws -> OpaquePointer? {
        var statement: OpaquePointer?
        guard sqlite3_prepare_v2(handle, sql, -1, &statement, nil) == SQLITE_OK else {
            throw DatabaseError.prepare(sql: sql, message: lastMessage())
        }
        for (offset, value) in parameters.enumerated() {
            let index = Int32(offset + 1)
            switch value {
            case .null:
                sqlite3_bind_null(statement, index)
            case .integer(let number):
                sqlite3_bind_int64(statement, index, number)
            case .real(let number):
                sqlite3_bind_double(statement, index, number)
            case .text(let string):
                sqlite3_bind_text(statement, index, string, -1, Self.transient)
            case .blob(let data):
                if data.isEmpty {
                    sqlite3_bind_zeroblob(statement, index, 0)
                } else {
                    _ = data.withUnsafeBytes { buffer in
                        sqlite3_bind_blob(statement, index, buffer.baseAddress, Int32(buffer.count), Self.transient)
                    }
                }
            }
        }
        return statement
    }

    private func lastMessage() -> String {
        String(cString: sqlite3_errmsg(handle))
    }
}

/// A bound parameter.
public enum SQLValue: Equatable, Sendable {
    case null
    case integer(Int64)
    case real(Double)
    case text(String)
    case blob(Data)

    public static func int(_ value: Int) -> SQLValue { .integer(Int64(value)) }
    public static func uint(_ value: UInt64) -> SQLValue { .integer(Int64(bitPattern: value)) }
    public static func bool(_ value: Bool) -> SQLValue { .integer(value ? 1 : 0) }
    public static func date(_ value: Date?) -> SQLValue {
        guard let value else { return .null }
        return .integer(Int64(value.timeIntervalSince1970 * 1000))
    }
    /// A date for a NOT NULL millis column, where absence is `0` rather than NULL.
    ///
    /// `date(_:)` binds NULL for a missing value, which a `NOT NULL DEFAULT 0` column
    /// REJECTS — a default applies only when the column is left out of the statement
    /// entirely, not when NULL is bound to it. Using the wrong one of these two turns
    /// "unmute this chat" into a constraint failure.
    public static func millis(_ value: Date?) -> SQLValue {
        guard let value else { return .integer(0) }
        return .integer(Int64(value.timeIntervalSince1970 * 1000))
    }

    public static func optionalText(_ value: String?) -> SQLValue {
        guard let value, !value.isEmpty else { return .null }
        return .text(value)
    }
}

/// One result row, addressed by column name.
public struct Row: Sendable {
    private var values: [String: SQLValue]

    init(statement: OpaquePointer?) {
        var values: [String: SQLValue] = [:]
        let count = sqlite3_column_count(statement)
        for index in 0..<count {
            let name = String(cString: sqlite3_column_name(statement, index))
            switch sqlite3_column_type(statement, index) {
            case SQLITE_INTEGER:
                values[name] = .integer(sqlite3_column_int64(statement, index))
            case SQLITE_FLOAT:
                values[name] = .real(sqlite3_column_double(statement, index))
            case SQLITE_TEXT:
                values[name] = .text(String(cString: sqlite3_column_text(statement, index)))
            case SQLITE_BLOB:
                if let pointer = sqlite3_column_blob(statement, index) {
                    let length = Int(sqlite3_column_bytes(statement, index))
                    values[name] = .blob(Data(bytes: pointer, count: length))
                } else {
                    values[name] = .blob(Data())
                }
            default:
                values[name] = .null
            }
        }
        self.values = values
    }

    public func int(_ column: String) -> Int64? {
        if case .integer(let value) = values[column] { return value }
        return nil
    }

    public func uint(_ column: String) -> UInt64 {
        UInt64(bitPattern: int(column) ?? 0)
    }

    public func string(_ column: String) -> String? {
        if case .text(let value) = values[column] { return value }
        return nil
    }

    public func data(_ column: String) -> Data? {
        if case .blob(let value) = values[column] { return value }
        return nil
    }

    public func bool(_ column: String) -> Bool {
        (int(column) ?? 0) != 0
    }

    public func date(_ column: String) -> Date? {
        guard let millis = int(column) else { return nil }
        return Date(timeIntervalSince1970: Double(millis) / 1000)
    }

    /// The read half of `SQLValue.millis`: `0` means absent, not 1 January 1970.
    ///
    /// Without this a chat that has never been muted reads back as "muted until 1970"
    /// — which happens to behave correctly wherever the value is only compared against
    /// now, and renders as a date from before the app existed anywhere it is shown.
    public func millisDate(_ column: String) -> Date? {
        guard let millis = int(column), millis > 0 else { return nil }
        return Date(timeIntervalSince1970: Double(millis) / 1000)
    }
}
