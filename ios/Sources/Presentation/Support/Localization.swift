import Foundation
import SynapseDomain

/// Localized strings, with an in-app language override.
///
/// `String(localized:)` alone resolves against the *system* language, which is
/// right until the settings screen offers "Русский / English" — at that point
/// the app has to look up strings in a bundle the user chose, not the one iOS
/// chose. Hence the explicit `.lproj` lookup, with the system bundle as the
/// fallback whenever the preference is `.system` or the chosen bundle is missing.
public enum L10n {
    /// Set from the settings repository at launch and on every change.
    /// Guarded by `lock` because views read it from the main actor while the
    /// settings stream writes it from wherever the update arrived.
    private static var overrideBundle: Bundle?
    private static let lock = NSLock()

    public static func setLanguage(_ language: AppSettings.Language) {
        lock.lock()
        defer { lock.unlock() }
        switch language {
        case .system:
            overrideBundle = nil
        case .ru, .en:
            let path = Bundle.main.path(forResource: language.rawValue, ofType: "lproj")
            overrideBundle = path.flatMap(Bundle.init(path:))
        }
    }

    public static func string(_ key: String, _ arguments: [CVarArg] = []) -> String {
        lock.lock()
        let bundle = overrideBundle ?? .main
        lock.unlock()
        // A missing key returns the key itself, which is exactly what we want on
        // screen: a visible `chat.send` is a bug report, an empty label is not.
        let format = bundle.localizedString(forKey: key, value: key, table: nil)
        return arguments.isEmpty ? format : String(format: format, arguments: arguments)
    }
}

/// Shorthand so views read as `Text(l("chats.title"))`.
public func l(_ key: String, _ arguments: CVarArg...) -> String {
    L10n.string(key, arguments)
}

public extension Date {
    /// Time for today, weekday for this week, date otherwise — the convention a
    /// chat list needs so a row's timestamp is readable at a glance.
    func chatListStamp(now: Date = .init(), calendar: Calendar = .current) -> String {
        let formatter = DateFormatter()
        if calendar.isDateInToday(self) {
            formatter.dateFormat = "HH:mm"
        } else if let weekAgo = calendar.date(byAdding: .day, value: -6, to: now), self > weekAgo {
            formatter.dateFormat = "EEE"
        } else {
            formatter.dateFormat = "dd.MM.yy"
        }
        return formatter.string(from: self)
    }

    func messageStamp() -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm"
        return formatter.string(from: self)
    }
}
