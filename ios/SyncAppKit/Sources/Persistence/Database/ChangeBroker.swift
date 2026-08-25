import Foundation

/// Fan-out for "something you are watching changed".
///
/// Repositories expose `AsyncStream`s of whole snapshots, and a snapshot stream
/// needs a nudge to re-query. This is that nudge, and nothing more: it carries
/// no payload, so there is no way for a subscriber to act on a change without
/// re-reading the database — which is what keeps the cache the single source of
/// truth rather than one of two.
public actor ChangeBroker {
    public enum Topic: Hashable, Sendable {
        case chats
        case messages(chatID: String)
        case contacts
        case typing(chatID: String)
        case draft(chatID: String)
        case connection
    }

    private var subscribers: [Topic: [UUID: AsyncStream<Void>.Continuation]] = [:]

    public init() {}

    /// A stream that yields once immediately (so a consumer renders the current
    /// state without waiting for the first change) and then on every change.
    public func stream(_ topic: Topic) -> AsyncStream<Void> {
        let id = UUID()
        let (stream, continuation) = AsyncStream<Void>.makeStream()

        subscribers[topic, default: [:]][id] = continuation

        continuation.onTermination = { [weak self, topic, id] _ in
            Task { [weak self, topic, id] in
                await self?.remove(id, from: topic)
            }
        }

        continuation.yield(())

        return stream
    }

    public func notify(_ topic: Topic) {
        guard let listeners = subscribers[topic] else { return }
        for continuation in listeners.values { continuation.yield(()) }
    }

    public func notify(_ topics: [Topic]) {
        for topic in topics { notify(topic) }
    }

    private func remove(_ id: UUID, from topic: Topic) {
        subscribers[topic]?[id] = nil
        if subscribers[topic]?.isEmpty == true { subscribers[topic] = nil }
    }
}
