import SwiftUI
import SyncAppDomain

/// Safety numbers for a secret chat, one per peer device.
///
/// The pin catches a key that changes after the first message; comparing this
/// number is what catches a key that was wrong from the start. So the screen shows
/// both: the number to compare out of band, and whether the key still matches the
/// pin — and when it does not, sending stays blocked until someone accepts here.
@MainActor
final class SafetyViewModel: ObservableObject {
    @Published private(set) var devices: [DeviceSafety] = []
    @Published private(set) var isLoading = false
    @Published var errorMessage: String?

    let peerUserID: String
    private let repository: any SecretSafetyRepository

    init(peerUserID: String, repository: any SecretSafetyRepository) {
        self.peerUserID = peerUserID
        self.repository = repository
    }

    var hasChangedKey: Bool { devices.contains { $0.status == .changed } }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            devices = try await repository.devices(peerUserID: peerUserID)
        } catch {
            errorMessage = Self.describe(error)
        }
    }

    /// Pins exactly the keys on screen, then reloads so the status reflects the pin.
    func accept(_ device: DeviceSafety) async {
        do {
            try await repository.accept(device)
            await load()
        } catch {
            errorMessage = Self.describe(error)
        }
    }

    /// Twelve five-digit groups as three rows of four, the layout both ends show.
    static func rows(of number: String) -> [String] {
        let groups = number.split(separator: " ").map(String.init)
        return stride(from: 0, to: groups.count, by: 4).map { start in
            groups[start..<min(start + 4, groups.count)].joined(separator: " ")
        }
    }

    private static func describe(_ error: Error) -> String {
        guard let error = error as? AppError else { return l("common.error") }
        switch error {
        case .offline: return l("connection.offline")
        case .invalidInput(let detail): return detail
        default: return l("common.error")
        }
    }
}

struct SafetyView: View {
    @StateObject private var model: SafetyViewModel
    @Environment(\.dismiss) private var dismiss

    init(peerUserID: String, repository: any SecretSafetyRepository) {
        _model = StateObject(wrappedValue: SafetyViewModel(peerUserID: peerUserID, repository: repository))
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Text(l("chat.secret.safety.hint"))
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                if model.hasChangedKey {
                    Section {
                        Label(l("chat.secret.safety.changed"), systemImage: "exclamationmark.shield.fill")
                            .foregroundStyle(Color.red)
                    }
                }
                if model.isLoading && model.devices.isEmpty {
                    Section { ProgressView() }
                }
                ForEach(model.devices) { device in
                    Section(l("chat.secret.safety.device", device.deviceID)) {
                        status(of: device)
                        VStack(alignment: .leading, spacing: 4) {
                            ForEach(SafetyViewModel.rows(of: device.safetyNumber), id: \.self) { row in
                                Text(row)
                            }
                        }
                        .font(.system(.body, design: .monospaced))
                        .textSelection(.enabled)
                        .accessibilityElement(children: .combine)
                        if device.status != .known {
                            Button(device.status == .changed
                                   ? l("chat.secret.safety.acceptChanged")
                                   : l("chat.secret.safety.markVerified")) {
                                Task { await model.accept(device) }
                            }
                        }
                    }
                }
            }
            .navigationTitle(l("chat.secret.safety"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(l("common.done")) { dismiss() }
                }
            }
            .task { await model.load() }
            .refreshable { await model.load() }
            .alert(
                l("common.error"),
                isPresented: Binding(
                    get: { model.errorMessage != nil },
                    set: { if !$0 { model.errorMessage = nil } }
                ),
                actions: { Button(l("common.ok"), role: .cancel) {} },
                message: { Text(model.errorMessage ?? "") }
            )
        }
    }

    @ViewBuilder
    private func status(of device: DeviceSafety) -> some View {
        switch device.status {
        case .known:
            Label(l("chat.secret.safety.known"), systemImage: "checkmark.shield")
                .foregroundStyle(Color.green)
        case .firstUse:
            Label(l("chat.secret.safety.firstUse"), systemImage: "shield")
                .foregroundStyle(.secondary)
        case .changed:
            Label(l("chat.secret.safety.keyChanged"), systemImage: "exclamationmark.shield.fill")
                .foregroundStyle(Color.red)
        }
    }
}
