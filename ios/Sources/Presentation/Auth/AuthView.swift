import SwiftUI
import SynapseDomain

@MainActor
final class AuthViewModel: ObservableObject {
    @Published var username = ""
    @Published var password = ""
    @Published var intent: AuthenticateUseCase.Intent = .login
    @Published private(set) var isBusy = false
    @Published var errorMessage: String?

    private let authenticate: AuthenticateUseCase

    init(auth: any AuthRepository) {
        self.authenticate = AuthenticateUseCase(auth: auth)
    }

    var canSubmit: Bool {
        !isBusy && !username.trimmingCharacters(in: .whitespaces).isEmpty && !password.isEmpty
    }

    func submit() async -> Account? {
        guard canSubmit else { return nil }
        isBusy = true
        errorMessage = nil
        defer { isBusy = false }
        do {
            return try await authenticate(intent: intent, username: username, password: password)
        } catch {
            errorMessage = Self.describe(error, intent: intent)
            return nil
        }
    }

    /// Turns an error into something a person can act on.
    ///
    /// Note what is *not* said: the gateway answers a wrong password and a
    /// nonexistent account with the same code, deliberately, so the protocol
    /// cannot be used to probe which usernames exist. Saying "no such user"
    /// here would leak exactly what the server refuses to.
    private static func describe(_ error: Error, intent: AuthenticateUseCase.Intent) -> String {
        if error is ValidationError { return l("auth.error.empty") }
        guard let error = error as? AppError else { return l("auth.error.network") }
        switch error {
        case .badCredentials, .unauthorized:
            return l("auth.error.credentials")
        case .usernameTaken:
            return l("auth.error.taken")
        case .rateLimited:
            return l("auth.error.throttled")
        case .invalidInput:
            return l("auth.error.invalid")
        case .offline:
            return l("auth.error.network")
        default:
            return l("auth.error.generic")
        }
    }
}

struct AuthView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model: AuthViewModel
    @FocusState private var focus: Field?

    private enum Field { case username, password }

    init(auth: any AuthRepository) {
        _model = StateObject(wrappedValue: AuthViewModel(auth: auth))
    }

    var body: some View {
        VStack(spacing: 24) {
            Spacer()

            VStack(spacing: 8) {
                Image(systemName: "bubble.left.and.bubble.right.fill")
                    .font(.system(size: 56))
                    .foregroundStyle(Theme.accent)
                Text(l("app.name")).font(.largeTitle.bold())
                Text(l("auth.subtitle"))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }

            // Register and login are separate intents on the wire — the gateway
            // never creates an account on a failed login — so the UI makes the
            // choice explicit instead of guessing.
            Picker("", selection: $model.intent) {
                Text(l("auth.login")).tag(AuthenticateUseCase.Intent.login)
                Text(l("auth.register")).tag(AuthenticateUseCase.Intent.register)
            }
            .pickerStyle(.segmented)

            VStack(spacing: 12) {
                TextField(l("auth.username"), text: $model.username)
                    .textContentType(.username)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focus, equals: .username)
                    .submitLabel(.next)
                    .onSubmit { focus = .password }

                SecureField(l("auth.password"), text: $model.password)
                    .textContentType(model.intent == .register ? .newPassword : .password)
                    .focused($focus, equals: .password)
                    .submitLabel(.go)
                    .onSubmit { Task { await submit() } }
            }
            .textFieldStyle(.roundedBorder)

            if let message = model.errorMessage {
                Text(message)
                    .font(.footnote)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
                    .transition(.opacity)
            }

            Button {
                Task { await submit() }
            } label: {
                if model.isBusy {
                    ProgressView().frame(maxWidth: .infinity)
                } else {
                    Text(model.intent == .login ? l("auth.login") : l("auth.register"))
                        .frame(maxWidth: .infinity)
                }
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .disabled(!model.canSubmit)

            Spacer()
        }
        .padding(24)
        .animation(.default, value: model.errorMessage)
    }

    private func submit() async {
        if let account = await model.submit() {
            app.didSignIn(account)
        }
    }
}
