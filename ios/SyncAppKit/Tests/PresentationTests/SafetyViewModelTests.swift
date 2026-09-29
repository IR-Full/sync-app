import XCTest

@testable import SyncAppDomain
@testable import SyncAppPresentation

/// A repository that holds pins in memory and records what was accepted.
private actor FakeSafetyRepository: SecretSafetyRepository {
    private var statuses: [String: IdentityStatus]
    private(set) var accepted: [String] = []

    init(statuses: [String: IdentityStatus]) {
        self.statuses = statuses
    }

    func devices(peerUserID: String) async throws -> [DeviceSafety] {
        statuses.keys.sorted().map { deviceID in
            DeviceSafety(
                userID: peerUserID,
                deviceID: deviceID,
                safetyNumber: (1...12).map { String(format: "%05d", $0) }.joined(separator: " "),
                status: statuses[deviceID] ?? .firstUse,
                identityKey: "ik-\(deviceID)",
                signingKey: "sk-\(deviceID)"
            )
        }
    }

    func accept(_ device: DeviceSafety) async throws {
        accepted.append(device.deviceID)
        statuses[device.deviceID] = .known
    }
}

@MainActor
final class SafetyViewModelTests: XCTestCase {

    func testAChangedKeyIsFlaggedUntilAccepted() async {
        let repository = FakeSafetyRepository(statuses: ["phone": .changed, "laptop": .known])
        let model = SafetyViewModel(peerUserID: "42", repository: repository)

        await model.load()
        XCTAssertTrue(model.hasChangedKey)
        XCTAssertEqual(model.devices.map(\.deviceID), ["laptop", "phone"])

        guard let phone = model.devices.first(where: { $0.deviceID == "phone" }) else {
            return XCTFail("no phone device")
        }
        await model.accept(phone)

        let accepted = await repository.accepted
        XCTAssertEqual(accepted, ["phone"])
        XCTAssertFalse(model.hasChangedKey, "accepting reloads, so the status reflects the new pin")
        XCTAssertNil(model.errorMessage)
    }

    /// Both ends draw the number in the same three rows of four groups, which is what
    /// makes reading it aloud line by line work.
    func testTheNumberIsLaidOutInThreeRowsOfFour() {
        let number = (1...12).map { String(format: "%05d", $0) }.joined(separator: " ")
        XCTAssertEqual(SafetyViewModel.rows(of: number), [
            "00001 00002 00003 00004",
            "00005 00006 00007 00008",
            "00009 00010 00011 00012",
        ])
    }
}
