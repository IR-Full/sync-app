import SwiftUI
import SyncAppDomain

@MainActor
final class SecurityViewModel: ObservableObject {
    @Published var currentPassword = ""
    @Published var newPassword = ""
    @Published var confirmPassword = ""

    @Published private(set) var twoFactor = TwoFactorState()
    @Published private(set) var setup: TwoFactorSetup?
    /// Shown exactly once, after a successful confirmation.
    ///
    /// Held in the view model rather than re-read on demand, because there is nothing
    /// to re-read: the server stores argon2id hashes. If this is lost before the user
    /// writes them down, the codes are gone.
    @Published private(set) var recoveryCodes: [String] = []

    @Published var code = ""
    @Published var disablePassword = ""

    /// Every live session of the account, newest first — see `fetchSessions`.
    @Published private(set) var sessions: [DeviceSession] = []
    @Published var deletePassword = ""
    /// Set once this device has been signed out — by revoking its own session or by
    /// deleting the account. The view watches it and hands the root back to the login
    /// screen; the local wipe has already happened inside the repository.
    @Published private(set) var endedSession: EndedSession?

    @Published private(set) var isBusy = false
    @Published var errorMessage: String?
    @Published var noticeMessage: String?

    /// Why this device stopped being signed in, so the login screen can say which of
    /// the two happened — "you signed this device out" and "your account is gone" are
    /// not the same message to land on.
    enum EndedSession: Equatable {
        case revokedThisDevice
        case accountDeleted
    }

    private let security: any AccountSecurityRepository

    init(security: any AccountSecurityRepository) {
        self.security = security
    }

    /// Loads the two independent halves of this screen INDEPENDENTLY.
    ///
    /// Not one `perform` around both: they are separate server reads, and putting them
    /// in one block means a failure of either leaves the other blank. A second-factor
    /// read that times out would then hide the device list — which is the half someone
    /// opened this screen for after losing a phone.
    func load() async {
        await perform { self.twoFactor = try await self.security.twoFactorState() }
        await reloadSessions()
    }

    private func reloadSessions() async {
        await perform { self.sessions = try await self.fetchSessions() }
    }

    /// The device list, newest first.
    ///
    /// The order is imposed here rather than taken from the server, which does not
    /// promise one — and the four callers all want the same order, so they share this
    /// rather than each remembering to sort.
    private func fetchSessions() async throws -> [DeviceSession] {
        try await security.sessions().sorted { $0.createdAt > $1.createdAt }
    }

    func changePassword() async {
        guard newPassword == confirmPassword else {
            errorMessage = l("security.password.mismatch")
            return
        }
        await perform {
            let revoked = try await self.security.changePassword(
                current: self.currentPassword, new: self.newPassword
            )
            self.currentPassword = ""
            self.newPassword = ""
            self.confirmPassword = ""
            self.noticeMessage = l("security.password.changed", revoked)
            // A password change signs the other sessions out, so the list on screen is
            // now wrong in the one direction that matters — it still shows devices that
            // this action just ended.
            self.sessions = try await self.fetchSessions()
        }
    }

    func beginTwoFactor() async {
        await perform { self.setup = try await self.security.beginTwoFactor() }
    }

    func confirmTwoFactor() async {
        await perform {
            let state = try await self.security.confirmTwoFactor(code: self.code)
            self.twoFactor = state
            // Captured before `state` is overwritten by any later read: the codes only
            // ever appear in THIS reply.
            self.recoveryCodes = state.recoveryCodes
            self.setup = nil
            self.code = ""
        }
    }

    func disableTwoFactor() async {
        await perform {
            self.twoFactor = try await self.security.disableTwoFactor(
                password: self.disablePassword, code: self.code
            )
            self.disablePassword = ""
            self.code = ""
            self.recoveryCodes = []
        }
    }

    func dismissRecoveryCodes() { recoveryCodes = [] }

    // MARK: - Sessions

    func revoke(_ session: DeviceSession) async {
        await perform {
            let result = try await self.security.revokeSession(id: session.sessionID)
            if result.signedOutThisDevice {
                self.endedSession = .revokedThisDevice
                return
            }
            // Re-read rather than removing the row locally. The server is the authority
            // on what is still live, and a revoke that killed fewer sessions than asked
            // would otherwise leave the list claiming otherwise.
            self.sessions = try await self.fetchSessions()
            self.noticeMessage = l("security.sessions.revoked", result.revoked)
        }
    }

    func revokeOthers() async {
        await perform {
            let result = try await self.security.revokeOtherSessions(includingThisDevice: false)
            self.sessions = try await self.fetchSessions()
            self.noticeMessage = l("security.sessions.revoked", result.revoked)
        }
    }

    // MARK: - Erasure

    func deleteAccount() async {
        await perform {
            _ = try await self.security.deleteAccount(password: self.deletePassword, reason: "")
            self.deletePassword = ""
            self.endedSession = .accountDeleted
        }
    }

    private func perform(_ work: @escaping () async throws -> Void) async {
        isBusy = true
        errorMessage = nil
        defer { isBusy = false }
        do {
            try await work()
        } catch let error as AppError {
            errorMessage = Self.describe(error)
        } catch {
            errorMessage = l("common.error")
        }
    }

    private static func describe(_ error: AppError) -> String {
        switch error {
        // Distinguished because they lead somewhere different: a wrong code is worth
        // retrying, a required one means the field has not been filled in yet, and
        // "bad credentials" here means the CURRENT password was wrong — not the new one.
        case .twoFactorInvalid: return l("security.code.invalid")
        case .twoFactorRequired: return l("security.code.required")
        case .badCredentials, .unauthorized: return l("auth.error.credentials")
        case .invalidInput(let detail): return detail
        case .offline: return l("connection.offline")
        default: return l("common.error")
        }
    }
}

/// Password and two-factor settings.
///
/// The password could not be changed by ANY path before this screen existed, which
/// meant a leaked one made the account permanently compromised rather than temporarily.
struct SecurityView: View {
    @Environment(\.dismiss) private var dismiss
    /// Reached through the environment because this screen is a sheet over
    /// `ProfileView`, which already has it. Needed for one thing: handing the root back
    /// to the login screen once this device has signed itself out.
    @EnvironmentObject private var app: AppModel
    @StateObject private var model: SecurityViewModel

    @State private var isConfirmingDelete = false
    @State private var isConfirmingRevokeOthers = false

    init(security: any AccountSecurityRepository) {
        _model = StateObject(wrappedValue: SecurityViewModel(security: security))
    }

    var body: some View {
        NavigationStack {
            Form {
                passwordSection
                twoFactorSection
                sessionsSection
                dangerSection
            }
            .navigationTitle(l("security.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(l("common.done")) { dismiss() }
                }
            }
            .overlay {
                if model.isBusy {
                    Color.black.opacity(0.05).overlay(ProgressView()).ignoresSafeArea()
                }
            }
            .alert(
                l("common.error"),
                isPresented: Binding(
                    get: { model.errorMessage != nil },
                    set: { if !$0 { model.errorMessage = nil } }
                ),
                actions: { Button(l("common.ok"), role: .cancel) {} },
                message: { Text(model.errorMessage ?? "") }
            )
            .alert(
                l("security.title"),
                isPresented: Binding(
                    get: { model.noticeMessage != nil },
                    set: { if !$0 { model.noticeMessage = nil } }
                ),
                actions: { Button(l("common.ok"), role: .cancel) {} },
                message: { Text(model.noticeMessage ?? "") }
            )
            .sheet(isPresented: Binding(
                get: { !model.recoveryCodes.isEmpty },
                set: { if !$0 { model.dismissRecoveryCodes() } }
            )) {
                RecoveryCodesView(codes: model.recoveryCodes) { model.dismissRecoveryCodes() }
            }
            .confirmationDialog(
                l("security.sessions.revokeOthers.confirm"),
                isPresented: $isConfirmingRevokeOthers,
                titleVisibility: .visible
            ) {
                Button(l("security.sessions.revokeOthers"), role: .destructive) {
                    Task { await model.revokeOthers() }
                }
                Button(l("common.cancel"), role: .cancel) {}
            }
            // A destructive, irreversible action gets a typed password AND a separate
            // confirmation. Two steps is not friction for its own sake: the password
            // field is authorisation and the dialog is intent, and a single tap that
            // happened to land on a filled form would be neither.
            .confirmationDialog(
                l("security.delete.confirm"),
                isPresented: $isConfirmingDelete,
                titleVisibility: .visible
            ) {
                Button(l("security.delete"), role: .destructive) {
                    Task { await model.deleteAccount() }
                }
                Button(l("common.cancel"), role: .cancel) {}
            } message: {
                Text(l("security.delete.detail"))
            }
            .task { await model.load() }
            // The local wipe already happened inside the repository, so this only
            // dismisses the sheet and moves the root. Done here rather than in the view
            // model to keep navigation in the layer that owns it.
            // The single-parameter `onChange`, because this package targets iOS 16 and
            // the two-parameter form (old, new) is iOS 17.
            .onChange(of: model.endedSession) { ended in
                guard let ended else { return }
                Task {
                    dismiss()
                    await app.accountSessionEnded(
                        message: ended == .accountDeleted
                            ? l("security.delete.done")
                            : l("security.sessions.signedOutHere")
                    )
                }
            }
        }
    }

    private var passwordSection: some View {
        Section {
            SecureField(l("security.password.current"), text: $model.currentPassword)
            SecureField(l("security.password.new"), text: $model.newPassword)
            SecureField(l("security.password.confirm"), text: $model.confirmPassword)
            Button(l("security.password")) {
                Task { await model.changePassword() }
            }
            .disabled(
                model.currentPassword.isEmpty
                    || model.newPassword.isEmpty
                    || model.confirmPassword.isEmpty
            )
        } header: {
            Text(l("security.password"))
        }
    }

    @ViewBuilder
    private var twoFactorSection: some View {
        Section {
            HStack {
                Text(l("security.twoFactor"))
                Spacer()
                Text(model.twoFactor.isEnabled ? l("security.twoFactor.on") : l("security.twoFactor.off"))
                    .foregroundStyle(model.twoFactor.isEnabled ? Color.green : .secondary)
            }

            if model.twoFactor.isEnabled {
                Text(l("security.twoFactor.recoveryLeft", model.twoFactor.recoveryCodesLeft))
                    .font(.caption)
                    .foregroundStyle(model.twoFactor.recoveryCodesLeft <= 2 ? Color.orange : .secondary)

                SecureField(l("security.password.current"), text: $model.disablePassword)
                TextField(l("security.twoFactor.code"), text: $model.code)
                    .keyboardType(.numberPad)
                Button(l("security.twoFactor.disable"), role: .destructive) {
                    Task { await model.disableTwoFactor() }
                }
                .disabled(model.disablePassword.isEmpty || model.code.isEmpty)
            } else if let setup = model.setup {
                // The key as TEXT, not only as a QR code. A QR code needs a second
                // device with a camera pointed at this one, and the usual case is an
                // authenticator on the same phone — where typing the key is the only
                // thing that works.
                VStack(alignment: .leading, spacing: 6) {
                    Text(l("security.twoFactor.secret")).font(.caption).foregroundStyle(.secondary)
                    Text(setup.secret)
                        .font(.system(.body, design: .monospaced))
                        .textSelection(.enabled)
                }
                Text(l("security.twoFactor.scan")).font(.caption).foregroundStyle(.secondary)
                TextField(l("security.twoFactor.code"), text: $model.code)
                    .keyboardType(.numberPad)
                Button(l("common.ok")) {
                    Task { await model.confirmTwoFactor() }
                }
                .disabled(model.code.isEmpty)
            } else {
                Button(l("security.twoFactor.enable")) {
                    Task { await model.beginTwoFactor() }
                }
            }
        } header: {
            Text(l("security.twoFactor"))
        }
    }

    /// Every device signed into this account, and the means to end any of them.
    ///
    /// The whole point of this addition: before it existed, signing out was a local
    /// gesture on this platform — the app forgot its token while the session stayed
    /// valid on the server until it expired. A lost phone kept access for the full TTL,
    /// and nothing here could show that it had.
    @ViewBuilder
    private var sessionsSection: some View {
        Section {
            if model.sessions.isEmpty {
                Text(l("security.sessions.empty")).foregroundStyle(.secondary)
            }
            ForEach(model.sessions) { session in
                VStack(alignment: .leading, spacing: 2) {
                    HStack {
                        Text(platformName(session.platform))
                        if session.isCurrent {
                            Text(l("security.sessions.thisDevice"))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button(l("security.sessions.revoke"), role: .destructive) {
                            Task { await model.revoke(session) }
                        }
                        .font(.caption)
                    }
                    Text(session.createdAt.formatted(date: .abbreviated, time: .shortened))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 2)
            }
            if model.sessions.contains(where: { !$0.isCurrent }) {
                Button(l("security.sessions.revokeOthers"), role: .destructive) {
                    isConfirmingRevokeOthers = true
                }
            }
        } header: {
            Text(l("security.sessions"))
        } footer: {
            Text(l("security.sessions.hint"))
        }
    }

    /// Account deletion, kept at the bottom and behind a password.
    private var dangerSection: some View {
        Section {
            SecureField(l("security.password.current"), text: $model.deletePassword)
            Button(l("security.delete"), role: .destructive) {
                isConfirmingDelete = true
            }
            .disabled(model.deletePassword.isEmpty)
        } header: {
            Text(l("security.delete"))
        } footer: {
            Text(l("security.delete.detail"))
        }
    }

    /// A readable name for the platform string a device declared in its handshake.
    ///
    /// Falls back to the raw value rather than to "Unknown": the string is
    /// client-asserted, so an unrecognised one is more likely a newer client than a
    /// corrupt row, and showing it is what lets someone recognise the device.
    private func platformName(_ platform: String) -> String {
        switch platform.lowercased() {
        case "ios": return "iPhone / iPad"
        case "android": return "Android"
        case "web": return "Web"
        case "desktop": return "Desktop"
        case "cli": return "CLI"
        case "": return l("security.sessions.unknownDevice")
        default: return platform
        }
    }
}

/// The recovery codes, shown once.
///
/// A full-screen sheet with a deliberate confirmation rather than a line in a list,
/// because this is the only moment they exist in readable form — the server keeps
/// argon2id hashes. A user who taps past this has lost their way back in.
private struct RecoveryCodesView: View {
    let codes: [String]
    let onDone: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(codes, id: \.self) { code in
                        Text(code)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                    }
                } footer: {
                    Text(l("security.twoFactor.recovery.hint"))
                }
            }
            .navigationTitle(l("security.twoFactor.recovery"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(l("common.done")) { onDone() }
                }
            }
        }
        .interactiveDismissDisabled()
    }
}
