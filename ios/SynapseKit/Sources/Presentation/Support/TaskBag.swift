import Foundation

/// Holds the long-lived observation tasks of a view model and cancels them when
/// the view model goes away.
///
/// It exists because `deinit` is the wrong place to do this directly. A view
/// model is `@MainActor`-isolated, its `deinit` is not, and reaching into
/// isolated stored properties from there is not something the compiler allows.
/// Moving the tasks into a plain reference type sidesteps the question: the bag
/// is not isolated to anything, so *its* `deinit` may touch its own storage, and
/// the view model needs no `deinit` at all.
///
/// The lock is not decoration either — a bag can be added to from the main actor
/// while being released on whichever thread dropped the last reference.
final class TaskBag: @unchecked Sendable {
    private let lock = NSLock()
    private var tasks: [Task<Void, Never>] = []

    func add(_ task: Task<Void, Never>) {
        lock.lock()
        tasks.append(task)
        lock.unlock()
    }

    /// Replaces whatever was held under `key`, cancelling it first. Used where a
    /// task supersedes an earlier one (a debounce, a re-issued request) rather
    /// than accumulating alongside it.
    func replace(_ key: String, with task: Task<Void, Never>) {
        lock.lock()
        let previous = keyed[key]
        keyed[key] = task
        lock.unlock()
        previous?.cancel()
    }

    func cancel(_ key: String) {
        lock.lock()
        let previous = keyed[key]
        keyed[key] = nil
        lock.unlock()
        previous?.cancel()
    }

    private var keyed: [String: Task<Void, Never>] = [:]

    func cancelAll() {
        lock.lock()
        let pending = tasks + Array(keyed.values)
        tasks = []
        keyed = [:]
        lock.unlock()
        for task in pending { task.cancel() }
    }

    deinit { cancelAll() }
}
