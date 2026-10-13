import Foundation

enum SyncTrigger: String, Codable, Sendable {
    case manual
    case scheduled
    case healthUpdate
    case onboarding
    case backgroundBackfill

    var title: String {
        switch self {
        case .manual: return "Manual"
        case .scheduled: return "Scheduled"
        case .healthUpdate: return "New Health data"
        case .onboarding: return "First sync"
        case .backgroundBackfill: return "Background history import"
        }
    }
}

/// One line of the activity log. Contains only metadata — never health values.
struct ActivityLogEntry: Codable, Identifiable, Equatable, Sendable {
    enum Event: String, Codable, Sendable {
        case sync
        case paired
        case unpaired
        case unlinkedByAgent
    }

    var id = UUID()
    var date: Date
    var event: Event
    var trigger: SyncTrigger?
    var kind: EnvelopeKind?
    var envelopeCount: Int = 0
    var ciphertextBytes: Int = 0
    var dayCount: Int = 0
    var firstDay: String?
    var lastDay: String?
    var duration: TimeInterval = 0
    var errorMessage: String?

    var succeeded: Bool {
        errorMessage == nil
    }
}

/// Persists the activity log as a small JSON file (protected until first unlock so that
/// background syncs can append to it).
final class ActivityLogStore: @unchecked Sendable {
    static let shared = ActivityLogStore()
    static let maxEntries = 200
    static let didChangeNotification = Notification.Name("HealthBridgeActivityLogDidChange")

    private let lock = NSLock()
    private let fileURL: URL

    init(fileURL: URL? = nil) {
        if let fileURL {
            self.fileURL = fileURL
        } else {
            let directory = (try? FileManager.default.url(
                for: .applicationSupportDirectory,
                in: .userDomainMask,
                appropriateFor: nil,
                create: true
            )) ?? FileManager.default.temporaryDirectory
            self.fileURL = directory.appendingPathComponent("activity-log.json")
        }
    }

    func entries() -> [ActivityLogEntry] {
        lock.lock()
        defer { lock.unlock() }
        return readUnlocked()
    }

    func append(_ entry: ActivityLogEntry) {
        lock.lock()
        var entries = readUnlocked()
        entries.insert(entry, at: 0)
        if entries.count > Self.maxEntries {
            entries.removeLast(entries.count - Self.maxEntries)
        }
        writeUnlocked(entries)
        lock.unlock()
        NotificationCenter.default.post(name: Self.didChangeNotification, object: nil)
    }

    func clear() {
        lock.lock()
        try? FileManager.default.removeItem(at: fileURL)
        lock.unlock()
        NotificationCenter.default.post(name: Self.didChangeNotification, object: nil)
    }

    private func readUnlocked() -> [ActivityLogEntry] {
        guard let data = try? Data(contentsOf: fileURL) else { return [] }
        return (try? JSONDecoder().decode([ActivityLogEntry].self, from: data)) ?? []
    }

    private func writeUnlocked(_ entries: [ActivityLogEntry]) {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        try? data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }
}
