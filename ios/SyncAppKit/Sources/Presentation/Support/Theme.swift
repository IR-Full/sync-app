import SwiftUI
import SyncAppDomain
import UIKit

/// Colours and small shared view pieces.
///
/// Every colour is a system colour, resolves through the asset catalogue, or is a
/// dynamic colour with its own dark variant, so light and dark mode work by
/// construction rather than by a pile of `colorScheme ==` checks.
public enum Theme {
    // The accent (and the outgoing bubble, which is painted with it) is the
    // user's choice, read from the environment as `appAccent`.
    public static let incomingBubble = Color(uiColor: .secondarySystemBackground)
    public static let outgoingText = Color.white
    public static let incomingText = Color.primary

    /// A stable colour per user id, so an avatar keeps its colour between
    /// launches without anything being stored.
    public static func avatarColor(for id: String) -> Color {
        let palette: [Color] = [.blue, .purple, .pink, .orange, .green, .teal, .indigo, .brown]
        var hash: UInt64 = 5381
        for byte in id.utf8 { hash = hash &* 33 &+ UInt64(byte) }
        return palette[Int(hash % UInt64(palette.count))]
    }
}

extension AppSettings.Theme {
    public var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }
}

extension AppSettings.Accent {
    /// The palette's colour, with a lighter variant for dark mode — the same
    /// values as the web client's `data-accent` blocks, so an account looks the
    /// same on both. `standard` is the asset catalogue's accent.
    public var color: Color {
        guard let palette else { return .accentColor }
        return Self.dynamic(light: palette.light, dark: palette.dark)
    }

    /// The light- and dark-mode RGB values; nil for the asset catalogue's accent.
    var palette: (light: UInt32, dark: UInt32)? {
        switch self {
        case .standard: return nil
        case .violet: return (0x7C4DFF, 0x9D78FF)
        case .emerald: return (0x0F9D6E, 0x2EC38D)
        case .amber: return (0xC2700C, 0xE8A13C)
        case .rose: return (0xD6336C, 0xF06595)
        }
    }

    public var titleKey: String { "profile.accent." + rawValue }

    private static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(uiColor: UIColor { traits in
            Self.rgb(traits.userInterfaceStyle == .dark ? dark : light)
        })
    }

    private static func rgb(_ hex: UInt32) -> UIColor {
        UIColor(
            red: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: 1
        )
    }
}

private struct AccentKey: EnvironmentKey {
    static let defaultValue = Color.accentColor
}

extension EnvironmentValues {
    /// The accent the user picked. `RootView` sets it together with `.tint`:
    /// `.tint` recolours controls, and a view that paints with the accent (a
    /// bubble, a badge) reads it from here. `Color.accentColor` is the asset
    /// catalogue's colour, so a view painting with it would ignore the choice.
    public var appAccent: Color {
        get { self[AccentKey.self] }
        set { self[AccentKey.self] = newValue }
    }
}

/// The pill beside a paying account's name. Text rather than a bare star, so it
/// reads at a glance and says what it is to VoiceOver.
public struct PremiumBadge: View {
    @Environment(\.appAccent) private var accent

    public init() {}

    public var body: some View {
        Text(l("profile.premium.badge"))
            .font(.caption2.weight(.semibold))
            .textCase(.uppercase)
            .foregroundStyle(.white)
            .padding(.horizontal, 8)
            .padding(.vertical, 2)
            .background(Capsule().fill(accent))
    }
}

/// A circular avatar: the user's picture when one has been set and downloaded,
/// a coloured monogram otherwise.
///
/// The monogram is not a placeholder for a missing feature — most people never
/// set a picture, and a chat row knows an id long before any profile has been
/// fetched. Its colour is derived from a stable seed, so the same person is the
/// same colour everywhere, which is what makes a list scannable.
///
/// The picture is passed as a LOCAL file URL, already downloaded through the
/// media pipeline. Handing this view a signed remote URL would put an expiring
/// credential behind an image load that retries whenever SwiftUI feels like it.
public struct Avatar: View {
    private let title: String
    private let seed: String
    private let size: CGFloat
    private let isOnline: Bool
    private let imageURL: URL?

    public init(
        title: String,
        seed: String,
        size: CGFloat = 48,
        isOnline: Bool = false,
        imageURL: URL? = nil
    ) {
        self.title = title
        self.seed = seed
        self.size = size
        self.isOnline = isOnline
        self.imageURL = imageURL
    }

    public var body: some View {
        ZStack(alignment: .bottomTrailing) {
            Circle()
                .fill(Theme.avatarColor(for: seed).gradient)
                .frame(width: size, height: size)
                .overlay(
                    Text(monogram)
                        .font(.system(size: size * 0.4, weight: .semibold))
                        .foregroundStyle(.white)
                )
                .overlay {
                    if let imageURL, let image = UIImage(contentsOfFile: imageURL.path) {
                        Image(uiImage: image)
                            .resizable()
                            .scaledToFill()
                            .frame(width: size, height: size)
                            .clipShape(Circle())
                    }
                }
            if isOnline {
                Circle()
                    .fill(Color.green)
                    .frame(width: size * 0.28, height: size * 0.28)
                    .overlay(Circle().stroke(Color(uiColor: .systemBackground), lineWidth: 2))
            }
        }
        .accessibilityHidden(true)
    }

    private var monogram: String {
        let cleaned = title.trimmingCharacters(in: CharacterSet(charactersIn: "@ "))
        guard let first = cleaned.first else { return "?" }
        return String(first).uppercased()
    }
}

/// The connection banner. Shown only when something is wrong — a permanently
/// visible "connected" badge is noise, and its absence is the signal.
public struct ConnectionBanner: View {
    private let status: ConnectionStatus

    public init(status: ConnectionStatus) {
        self.status = status
    }

    public var body: some View {
        if status != .online {
            HStack(spacing: 8) {
                if status == .connecting {
                    ProgressView().controlSize(.mini)
                }
                Text(status == .connecting ? l("connection.connecting") : l("connection.offline"))
                    .font(.footnote)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
            .background(status == .connecting ? Color.orange.opacity(0.2) : Color.red.opacity(0.2))
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }
}

/// Empty / error / loading states, so every screen tells the same story the
/// same way.
public struct StateView: View {
    public enum Kind {
        case loading
        case empty(title: String, message: String, systemImage: String)
        case failure(message: String, retry: () -> Void)
    }

    private let kind: Kind

    public init(_ kind: Kind) {
        self.kind = kind
    }

    public var body: some View {
        VStack(spacing: 12) {
            switch kind {
            case .loading:
                ProgressView()
            case .empty(let title, let message, let systemImage):
                Image(systemName: systemImage)
                    .font(.largeTitle)
                    .foregroundStyle(.secondary)
                Text(title).font(.headline)
                Text(message)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            case .failure(let message, let retry):
                Image(systemName: "exclamationmark.triangle")
                    .font(.largeTitle)
                    .foregroundStyle(.orange)
                Text(message)
                    .font(.subheadline)
                    .multilineTextAlignment(.center)
                Button(l("common.retry"), action: retry)
                    .buttonStyle(.bordered)
            }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
