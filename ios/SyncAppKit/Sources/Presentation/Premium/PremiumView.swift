import SwiftUI
import SyncAppDomain

@MainActor
final class PremiumViewModel: ObservableObject {
    @Published private(set) var entitlements = Entitlements.unknown
    @Published private(set) var offers: [PlanOffer] = []
    @Published private(set) var isBusy = false
    @Published private(set) var isUnavailable = false
    @Published var intent: PaymentIntent?
    @Published var errorMessage: String?

    /// The country the plans were priced for.
    ///
    /// From the device region rather than from the user, because it selects the
    /// PROVIDER as well as the currency — SBP in Russia, cards elsewhere — and a
    /// user-chosen country would let somebody pick the regulator that applies to their
    /// transaction. The server treats it as a hint and decides for itself.
    private let country: String

    private let security: any AccountSecurityRepository
    private let tasks = TaskBag()
    private var hasStarted = false

    init(
        security: any AccountSecurityRepository,
        // `regionCode` is deprecated from iOS 16, which is this target.
        country: String = Locale.current.region?.identifier ?? ""
    ) {
        self.security = security
        self.country = country
    }

    func start() {
        guard !hasStarted else { return }
        hasStarted = true
        tasks.add(Task { [weak self] in
            guard let self else { return }
            for await current in self.security.entitlements() {
                self.entitlements = current
            }
        })
        tasks.add(Task { [weak self] in await self?.loadPlans() })
    }

    func loadPlans() async {
        isBusy = true
        defer { isBusy = false }
        do {
            offers = try await security.plans(country: country)
            isUnavailable = offers.isEmpty
        } catch AppError.unsupported {
            // A build with no payment provider configured. Not an error to show as one:
            // such a deployment grants the features outright, so the screen says
            // payments are not set up rather than "something went wrong".
            isUnavailable = true
        } catch {
            errorMessage = l("common.error")
        }
    }

    func checkout(plan: String, method: PaymentMethod) async {
        isBusy = true
        errorMessage = nil
        defer { isBusy = false }
        do {
            intent = try await security.checkout(plan: plan, method: method, country: country)
        } catch {
            errorMessage = l("common.error")
        }
    }

    func cancel() async {
        isBusy = true
        defer { isBusy = false }
        do {
            entitlements = try await security.cancelSubscription()
        } catch {
            errorMessage = l("common.error")
        }
    }

    /// Whether the account is paying right now — for the plan label and the cancel
    /// button only. Features are gated on their entitlement instead, which a
    /// deployment without a payment provider grants on the `free` plan.
    var isPremium: Bool { entitlements.isPremiumActive() }

    func periodText() -> String? {
        guard let end = entitlements.periodEnd else { return nil }
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        return l("premium.until", formatter.string(from: end))
    }
}

/// The Premium screen: what the tier grants, what it costs, and how to pay.
struct PremiumView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.appAccent) private var accent
    @StateObject private var model: PremiumViewModel

    init(security: any AccountSecurityRepository) {
        _model = StateObject(wrappedValue: PremiumViewModel(security: security))
    }

    var body: some View {
        NavigationStack {
            Form {
                statusSection
                if model.isUnavailable {
                    Section { Text(l("premium.unavailable")).foregroundStyle(.secondary) }
                } else {
                    offersSection
                }
                if model.isPremium { cancelSection }
            }
            .navigationTitle(l("premium.title"))
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
            .sheet(item: $model.intent) { intent in
                PaymentView(intent: intent)
            }
            .task { model.start() }
        }
    }

    private var statusSection: some View {
        Section {
            HStack {
                Text(l("premium.current"))
                Spacer()
                Text(model.isPremium ? l("premium.plan.premium") : l("premium.plan.free"))
                    .foregroundStyle(model.isPremium ? accent : .secondary)
            }
            if let period = model.periodText() {
                Text(period).font(.caption).foregroundStyle(.secondary)
            }
            if model.entitlements.cancelAtPeriodEnd {
                Text(l("premium.cancelling")).font(.caption).foregroundStyle(Color.orange)
            }
        } footer: {
            Text(l("premium.subtitle"))
        }
    }

    @ViewBuilder
    private var offersSection: some View {
        ForEach(model.offers) { offer in
            Section {
                HStack {
                    Text(offer.priceText()).font(.title3.weight(.semibold))
                    Spacer()
                    Text("\(offer.periodDays) d").foregroundStyle(.secondary)
                }
                // One button per method the SERVER offered for this plan and country.
                // Not the full `PaymentMethod` list: a method that is not available
                // here fails after the user has committed to paying.
                ForEach(offer.methods, id: \.self) { method in
                    Button {
                        Task { await model.checkout(plan: offer.plan, method: method) }
                    } label: {
                        Label(Self.label(for: method), systemImage: Self.icon(for: method))
                    }
                }
            } header: {
                Text(offer.plan)
            }
        }
    }

    private var cancelSection: some View {
        Section {
            Button(l("premium.cancel"), role: .destructive) {
                Task { await model.cancel() }
            }
            .disabled(model.entitlements.cancelAtPeriodEnd)
        }
    }

    private static func label(for method: PaymentMethod) -> String {
        switch method {
        case .card: return l("premium.method.card")
        case .sbp: return l("premium.method.sbp")
        }
    }

    private static func icon(for method: PaymentMethod) -> String {
        switch method {
        case .card: return "creditcard"
        case .sbp: return "qrcode"
        }
    }
}

/// Where the user finishes paying.
///
/// The two shapes are not interchangeable and the difference is the whole content of
/// this view: `payURL` is FOLLOWED in a browser, while an SBP `qrPayload` is DISPLAYED
/// for a bank app to scan. Opening a QR payload as a URL produces a dead link, and
/// rendering a redirect URL as a QR code produces a code that goes to a web page
/// instead of to a payment.
private struct PaymentView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    let intent: PaymentIntent

    var body: some View {
        NavigationStack {
            VStack(spacing: 20) {
                if !intent.qrPayload.isEmpty {
                    Text(l("premium.qr")).font(.headline)
                    // The payload as selectable text, not as a rendered code. Rendering
                    // one needs CoreImage and a bitmap, and a payload the user can copy
                    // into their bank app works today rather than after that is built.
                    Text(intent.qrPayload)
                        .font(.system(.footnote, design: .monospaced))
                        .textSelection(.enabled)
                        .padding()
                        .background(RoundedRectangle(cornerRadius: 12).fill(Color.secondary.opacity(0.1)))
                }
                if let url = intent.payURL {
                    Button(l("premium.pay")) { openURL(url) }
                        .buttonStyle(.borderedProminent)
                }
                Spacer()
            }
            .padding()
            .navigationTitle(l("premium.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(l("common.done")) { dismiss() }
                }
            }
        }
    }
}
