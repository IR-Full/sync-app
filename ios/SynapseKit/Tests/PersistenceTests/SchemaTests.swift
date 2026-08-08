import XCTest
@testable import SynapsePersistence

/// Migration tests.
///
/// The interesting property is not "the schema is correct" — the queries prove
/// that. It is that an *existing* database reaches the same shape as a fresh
/// one, which is the difference nobody notices until the first upgrade in the
/// field.
final class SchemaTests: XCTestCase {
    private var url: URL!

    override func setUp() {
        url = FileManager.default.temporaryDirectory
            .appendingPathComponent("synapse-schema-\(UUID().uuidString).sqlite")
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: url)
    }

    func testFreshDatabaseLandsAtTheLatestVersion() async throws {
        let database = try Database(url: url)
        try await database.prepare()

        let version = try await database.userVersion()
        XCTAssertEqual(version, Schema.migrations.count)
    }

    /// Reopening must be a no-op. A migration loop that re-ran would fail on
    /// `CREATE TABLE` the second time — silently, if anyone wrapped it in `try?`.
    func testPrepareIsIdempotentAcrossOpens() async throws {
        let first = try Database(url: url)
        try await first.prepare()

        let second = try Database(url: url)
        try await second.prepare()

        let version = try await second.userVersion()
        XCTAssertEqual(version, Schema.migrations.count)
    }

    /// A database left at v1 must pick up v2 rather than being recreated — the
    /// outbox may be holding messages the user typed offline.
    func testUpgradeFromV1AddsTheV2ShapeWithoutLosingRows() async throws {
        // Apply v1 only, by hand, exactly as an older build would have left it.
        let old = try Database(url: url)
        try await old.execute(Schema.migrations[0])
        try await old.setUserVersion(1)
        try await old.run(
            """
            INSERT INTO outbox (dedup_key, chat_id, text, created_at) VALUES (?, ?, ?, ?)
            """,
            [.text("queued-1"), .text("10"), .text("typed while offline"), .integer(1_700_000_000_000)]
        )

        let upgraded = try Database(url: url)
        try await upgraded.prepare()

        let version = try await upgraded.userVersion()
        XCTAssertEqual(version, Schema.migrations.count)

        // The v2 column exists...
        let rows = try await upgraded.query("SELECT dedup_key, attachment FROM outbox")
        XCTAssertEqual(rows.count, 1, "the queued message survived the upgrade")
        XCTAssertEqual(rows.first?.string("dedup_key"), "queued-1")

        // ...and so does the v2 table.
        let drafts = try await upgraded.query("SELECT COUNT(*) AS n FROM drafts")
        XCTAssertEqual(drafts.first?.int("n"), 0)
    }

    func testRefusesADatabaseNewerThanThisBuild() async throws {
        let database = try Database(url: url)
        try await database.prepare()
        try await database.setUserVersion(Schema.migrations.count + 5)

        let reopened = try Database(url: url)
        do {
            try await reopened.prepare()
            XCTFail("expected the newer schema to be refused")
        } catch {
            guard let failure = error as? Database.DatabaseError,
                  case .migration = failure
            else {
                return XCTFail("wrong error: \(error)")
            }
        }
    }
}
