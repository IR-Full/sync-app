import SwiftUI
import SynapseDomain

/// Profile and settings.
///
/// The honest part of this screen is what it does *not* claim. The gateway has
/// no profile API: `display_name` can only be supplied at registration (and the
/// gateway passes an empty one), there is no avatar anywhere in the protocol,
/// and no message type updates any of it afterwards. So the name and the symbol
/// here are device-local, and the screen says so rather than implying they will
/// appear to anyone else.
struct ProfileView: View {
    @Environment(\.dismiss) private var dismiss
    @EnvironmentObject private var app: AppModel
    @State private var isConfirmingSignOut = false

    private let account: Account

    private static let symbols = ["😀", "🐱", "🚀", "🎧", "🌿", "⚡️", "🎲", "🛠"]

    init(account: Account) {
        self.account = account
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
                if app.settings.avatarSymbol.isEmpty {
                    Avatar(title: account.username, seed: account.userID, size: 60)
                } else {
                    Text(app.settings.avatarSymbol)
                        .font(.system(size: 44))
                        .frame(width: 60, height: 60)
                        .background(Circle().fill(Color(uiColor: .secondarySystemBackground)))
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(displayName).font(.headline)
                    Text("@" + account.username).foregroundStyle(.secondary)
                }
            }
            .padding(.vertical, 4)

            TextField(
                l("profile.display.name"),
                text: Binding(
                    get: { app.settings.localDisplayName },
                    set: { value in app.updateSettings { $0.localDisplayName = value } }
                )
            )

            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 12) {
                    ForEach(Self.symbols, id: \.self) { symbol in
                        Button {
                            app.updateSettings {
                                $0.avatarSymbol = ($0.avatarSymbol == symbol) ? "" : symbol
                            }
                        } label: {
                            Text(symbol)
                                .font(.title2)
                                .padding(6)
                                .background(
                                    Circle().fill(
                                        app.settings.avatarSymbol == symbol
                                            ? Color.accentColor.opacity(0.25)
                                            : Color.clear
                                    )
                                )
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.vertical, 4)
            }

            LabeledContent(l("profile.user.id"), value: account.userID)
                .font(.footnote)
            LabeledContent(l("profile.device.id"), value: String(account.deviceID.prefix(8)))
                .font(.footnote)
        } header: {
            Text(l("profile.section.identity"))
        } footer: {
            // Saying this out loud is the point.
            Text(l("profile.local.only.footer"))
        }
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

    private var displayName: String {
        app.settings.localDisplayName.isEmpty ? account.username : app.settings.localDisplayName
    }
}
