import SwiftUI
import SynapseDomain

/// Colours and small shared view pieces.
///
/// Every colour is either a system colour or resolves through the asset
/// catalogue, so light and dark mode work by construction rather than by a pile
/// of `colorScheme ==` checks.
public enum Theme {
    public static let accent = Color.accentColor
    public static let outgoingBubble = Color.accentColor
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

/// A circular monogram. There are no avatar images in this protocol — no
/// profile API, no avatar media ref on a chat — so a coloured initial is the
/// honest maximum rather than a placeholder for something that will arrive later.
public struct Avatar: View {
    private let title: String
    private let seed: String
    private let size: CGFloat
    private let isOnline: Bool

    public init(title: String, seed: String, size: CGFloat = 48, isOnline: Bool = false) {
        self.title = title
        self.seed = seed
        self.size = size
        self.isOnline = isOnline
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
