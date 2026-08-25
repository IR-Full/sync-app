import PhotosUI
import SwiftUI
import SyncAppDomain

/// Profile and settings.
///
/// The name and the picture live on the SERVER: `PROFILE_SET` writes them, the
/// gateway mirrors the change to this account's other devices, and `AUTH_OK`
/// returns them on every connect. What the screen holds is a copy for drawing,
/// not the source — which is why an edit is saved explicitly and the field
/// otherwise follows whatever the server last said.
///
/// A picture is an ordinary `media_ref`: uploaded through the media pipeline,
/// then published. The upload has to succeed first — a ref that never landed
/// must not end up on a profile.
struct ProfileView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var app: AppModel
    @State private var isConfirmingSignOut = false

    /// nil means "not edited": the field then follows the account, which is what
    /// lets a change made on another device land in it while it sits open.
    @State private var draftName: String?
    @State private var pickedPhoto: PhotosPickerItem?
    @State private var isSaving = false
    @State private var avatarFile: URL?
    @State private var errorMessage: String?

    /// The account is read from the app model rather than captured at init, so a
    /// change mirrored from another device redraws this screen while it is open
    /// — a captured copy would sit here going stale.
    private var account: Account { app.account ?? placeholder }
    private let placeholder: Account
    private let media: any MediaRepository

    init(account: Account, media: any MediaRepository) {
        self.placeholder = account
        self.media = media
    }

    var body: some View {
        NavigationStack {
            Form {
                identitySection
                appearanceSection
                notificationsSection
                connectionSection
                signOutSection
            }
            .navigationTitle(l("profile.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(l("common.done")) { dismiss() }
                }
            }
        }
    }

    private var identitySection: some View {
        Section {
            HStack(spacing: 16) {
                Avatar(
                    title: account.bestName,
                    seed: account.userID,
                    size: 60,
                    imageURL: avatarFile
                )
                VStack(alignment: .leading, spacing: 2) {
                    Text(account.bestName).font(.headline)
                    Text("@" + account.username).foregroundStyle(.secondary)
                }
            }
            .padding(.vertical, 4)

            HStack {
                PhotosPicker(
                    selection: $pickedPhoto,
                    matching: .images,
                    photoLibrary: .shared()
                ) {
                    Text(l("profile.avatar.change"))
                }
                .disabled(isSaving)

                if !account.avatarRef.isEmpty {
                    Spacer()
                    Button(l("profile.avatar.remove"), role: .destructive) {
                        Task { await save { try await app.updateProfile(clearAvatar: true) } }
                    }
                    .disabled(isSaving)
                }
            }

            TextField(
                l("profile.display.name"),
                text: Binding(
                    get: { draftName ?? account.displayName },
                    set: { draftName = $0 }
                )
            )
            .submitLabel(.done)
            .onSubmit { saveName() }

            Button(l("profile.save")) { saveName() }
                .disabled(isSaving || !nameChanged)

            if let errorMessage {
                Text(errorMessage).font(.footnote).foregroundStyle(.red)
            }

            LabeledContent(l("profile.user.id"), value: account.userID)
                .font(.footnote)
            LabeledContent(l("profile.device.id"), value: String(account.deviceID.prefix(8)))
                .font(.footnote)
        } header: {
            Text(l("profile.section.identity"))
        } footer: {
            Text(l("profile.visible.footer"))
        }
        // Resolving the picture is a download, so it belongs in a task rather
        // than in the body; it re-runs when the ref changes, including when the
        // change came from another device.
        .task(id: account.avatarRef) { await loadAvatar() }
        .onChange(of: pickedPhoto) { item in
            guard let item else { return }
            Task { await uploadAvatar(item) }
        }
    }

    private var nameChanged: Bool {
        guard let draftName else { return false }
        let trimmed = draftName.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed != account.displayName
    }

    private func saveName() {
        guard let draftName, nameChanged else { return }
        let trimmed = draftName.trimmingCharacters(in: .whitespacesAndNewlines)
        Task { await save { try await app.updateProfile(displayName: trimmed) } }
    }

    /// Runs one profile write, keeping the button disabled and surfacing a
    /// failure instead of leaving the screen looking like it saved.
    @MainActor
    private func save(_ work: @escaping () async throws -> Void) async {
        isSaving = true
        errorMessage = nil
        do {
            try await work()
            // Hand the field back to the server's copy now that they agree.
            draftName = nil
        } catch {
            errorMessage = l("profile.save.failed")
        }
        isSaving = false
    }

    @MainActor
    private func uploadAvatar(_ item: PhotosPickerItem) async {
        pickedPhoto = nil
        guard let data = try? await item.loadTransferable(type: Data.self) else {
            errorMessage = l("profile.avatar.failed")
            return
        }
        await save {
            let attachment = try await media.upload(
                data: data,
                filename: "avatar.jpg",
                mime: "image/jpeg",
                kind: .image,
                extra: MediaMetadata()
            )
            // Publish only after the bytes are actually stored.
            try await app.updateProfile(avatarRef: attachment.mediaRef)
        }
    }

    @MainActor
    private func loadAvatar() async {
        guard !account.avatarRef.isEmpty else {
            avatarFile = nil
            return
        }
        let attachment = Attachment(kind: .image, mediaRef: account.avatarRef)
        avatarFile = try? await media.fileURL(for: attachment)
    }

    private var appearanceSection: some View {
        Section(l("profile.section.appearance")) {
            Picker(l("profile.theme"), selection: Binding(
                get: { app.settings.theme },
                set: { value in app.updateSettings { $0.theme = value } }
            )) {
                Text(l("profile.theme.system")).tag(AppSettings.Theme.system)
                Text(l("profile.theme.light")).tag(AppSettings.Theme.light)
                Text(l("profile.theme.dark")).tag(AppSettings.Theme.dark)
            }

            Picker(l("profile.language"), selection: Binding(
                get: { app.settings.language },
                set: { value in app.updateSettings { $0.language = value } }
            )) {
                Text(l("profile.language.system")).tag(AppSettings.Language.system)
                Text("Русский").tag(AppSettings.Language.ru)
                Text("English").tag(AppSettings.Language.en)
            }
        }
    }

    private var notificationsSection: some View {
        Section {
            Toggle(l("profile.push"), isOn: Binding(
                get: { app.settings.pushEnabled },
                set: { value in app.updateSettings { $0.pushEnabled = value } }
            ))
            Toggle(l("profile.typing"), isOn: Binding(
                get: { app.settings.showTypingIndicators },
                set: { value in app.updateSettings { $0.showTypingIndicators = value } }
            ))
        } header: {
            Text(l("profile.section.notifications"))
        } footer: {
            Text(l("profile.push.footer"))
        }
    }

    private var connectionSection: some View {
        Section(l("profile.section.connection")) {
            LabeledContent(l("profile.status")) {
                switch app.connection {
                case .online:
                    Label(l("connection.online"), systemImage: "circle.fill")
                        .foregroundStyle(.green)
                case .connecting:
                    Label(l("connection.connecting"), systemImage: "circle.fill")
                        .foregroundStyle(.orange)
                case .offline:
                    Label(l("connection.offline"), systemImage: "circle.fill")
                        .foregroundStyle(.red)
                }
            }
            .font(.footnote)
        }
    }

    private var signOutSection: some View {
        Section {
            Button(role: .destructive) {
                isConfirmingSignOut = true
            } label: {
                Text(l("profile.sign.out")).frame(maxWidth: .infinity)
            }
            .confirmationDialog(
                l("profile.sign.out.confirm"),
                isPresented: $isConfirmingSignOut,
                titleVisibility: .visible
            ) {
                Button(l("profile.sign.out"), role: .destructive) {
                    Task {
                        await app.signOut()
                        dismiss()
                    }
                }
                Button(l("common.cancel"), role: .cancel) {}
            } message: {
                Text(l("profile.sign.out.message"))
            }
        }
    }

}
