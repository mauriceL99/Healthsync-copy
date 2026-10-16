import Foundation
import os

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
    /// A sync that succeeded but left something out (optional, so older logs still decode).
    var warning: String?

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
        do {
            try FileManager.default.removeItem(at: fileURL)
        } catch CocoaError.fileNoSuchFile {
            // Already empty.
        } catch {
            Log.storage.error("Clearing the activity log failed: \(error.localizedDescription, privacy: .public)")
        }
        lock.unlock()
        NotificationCenter.default.post(name: Self.didChangeNotification, object: nil)
    }

    private func readUnlocked() -> [ActivityLogEntry] {
        let data: Data
        do {
            data = try Data(contentsOf: fileURL)
        } catch CocoaError.fileReadNoSuchFile {
            return []
        } catch {
            Log.storage.error("Activity log unreadable: \(error.localizedDescription, privacy: .public)")
            return []
        }
        do {
            return try JSONDecoder().decode([ActivityLogEntry].self, from: data)
        } catch {
            // Keep the undecodable file (e.g. written by a newer app version) instead of
            // letting the next append overwrite it.
            let backup = fileURL.deletingPathExtension()
                .appendingPathExtension("unreadable-\(Int(Date().timeIntervalSince1970)).json")
            do {
                try FileManager.default.moveItem(at: fileURL, to: backup)
                Log.storage.error("Activity log could not be decoded; kept it as \(backup.lastPathComponent, privacy: .public)")
            } catch {
                Log.storage.error("Activity log could not be decoded or moved aside: \(error.localizedDescription, privacy: .public)")
            }
            return []
        }
    }

    private func writeUnlocked(_ entries: [ActivityLogEntry]) {
        // The log is diagnostics only: a failed write must never fail a sync, but it is
        // recorded in the unified log.
        do {
            let data = try JSONEncoder().encode(entries)
            try data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        } catch {
            Log.storage.error("Activity log could not be written: \(error.localizedDescription, privacy: .public)")
        }
    }
}
