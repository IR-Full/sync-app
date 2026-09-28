import Foundation

/// How many messages of each chat the UI is currently showing.
///
/// A chat opens on one screenful and grows as the user scrolls up, so the read
/// has to be bounded by something that grows with it. A constant `LIMIT` is the
/// bug this replaces: older pages landed in the cache and stayed invisible,
/// because the query kept returning the same newest rows.
///
/// An actor because the window is read from every observation task and written
/// from whichever task is paging.
actor HistoryWindows {
    private var limits: [String: Int] = [:]

    /// The window for a chat, defaulting to `initialPages` worth on first read.
    func limit(chatID: String, pageSize: Int) -> Int {
        if let existing = limits[chatID] { return existing }
        let initial = max(pageSize * Self.initialPages, pageSize)
        limits[chatID] = initial
        return initial
    }

    /// Widens the window by one page. Capped, because this bounds a query whose
    /// result is held in memory and diffed by SwiftUI: a chat someone scrolls
    /// through for long enough should stop growing the snapshot rather than load
    /// years of history into a single list.
    func grow(chatID: String, by amount: Int, pageSize: Int) {
        let current = limit(chatID: chatID, pageSize: pageSize)
        limits[chatID] = min(current + amount, Self.maxWindow)
    }

    /// Forgets a chat's window, so reopening it starts from one screenful again.
    func reset(chatID: String) {
        limits[chatID] = nil
    }

    /// Pages held when a chat is first opened. More than one, so the first scroll
    /// up does not immediately need the network.
    private static let initialPages = 4

    /// Ceiling on a single chat's in-memory snapshot.
    private static let maxWindow = 2000
}
