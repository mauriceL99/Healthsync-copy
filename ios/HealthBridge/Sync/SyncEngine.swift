import CryptoKit
import Foundation
import os

struct SyncProgress: Equatable, Sendable {
    var completed: Int
    var total: Int
    var message: String

    var fraction: Double {
        total > 0 ? min(Double(completed) / Double(total), 1) : 0
    }

    static func starting(_ message: String) -> SyncProgress {
        SyncProgress(completed: 0, total: 1, message: message)
    }
}

struct SyncReport: Equatable, Sendable {
    var kind: EnvelopeKind
    var envelopeCount: Int
    var ciphertextBytes: Int
    var dayCount: Int
    var finishedAt: Date
    /// HealthKit reads that failed and were left out of the upload (see `HealthBatchBuilder`).
    var skippedReads: Int = 0
}

/// What `SyncEngine.unpair()` managed to do.
struct UnpairResult: Equatable, Sendable {
    /// The relay confirmed the unlink (or already did not know the device).
    var reachedRelay: Bool
    /// Set when the keychain could not be wiped; the pairing is then kept so the user can retry.
    var localError: String?
}

enum SyncError: LocalizedError {
    case notPaired
    case alreadyRunning
    case healthUnavailable
    case healthLocked
    case deviceUnlinked
    case cancelled
    case keychain(String)
    case encoding
    case relay(RelayError)
    case envelope(EnvelopeError)
    case other(String)

    var errorDescription: String? {
        switch self {
        case .notPaired:
            return "HealthBridge is not paired with an agent."
        case .alreadyRunning:
            return "A sync is already running."
        case .healthUnavailable:
            return HealthAccessError.unavailable.errorDescription
        case .healthLocked:
            return HealthAccessError.locked.errorDescription
        case .deviceUnlinked:
            return RelayError.deviceUnlinked.errorDescription
        case .cancelled:
            return "The sync was interrupted. It will continue next time."
        case .keychain(let message):
            return message
        case .encoding:
            return "Preparing the health data failed."
        case .relay(let error):
            return error.errorDescription
        case .envelope(let error):
            return error.errorDescription
        case .other(let message):
            return message
        }
    }

    static func wrap(_ error: Error) -> SyncError {
        switch error {
        case let error as SyncError:
            return error
        case is CancellationError:
            return .cancelled
        case let error as RelayError:
            return error == .deviceUnlinked ? .deviceUnlinked : .relay(error)
        case let error as EnvelopeError:
            return .envelope(error)
        case let error as HealthAccessError:
            return error == .locked ? .healthLocked : .healthUnavailable
        case let error as KeychainError:
            return .keychain(error.errorDescription ?? "Keychain error.")
        case is EncodingError:
            return .encoding
        default:
            if HealthKitService.isDatabaseLocked(error) {
                return .healthLocked
            }
            return .other(error.localizedDescription)
        }
    }
}

/// What a sync run will send.
struct SyncPlan: Equatable, Sendable {
    var kind: EnvelopeKind
    var range: DayRange
    var chunks: [DayRange]

    /// Days per envelope. Keeps each plaintext small even with detailed workout data.
    static let chunkDays = 30
    /// Incremental syncs always re-send the last few days (the agent merges per day).
    static let incrementalDays = 3

    static func make(
        now: Date,
        calendar: Calendar,
        settings: SyncSettings,
        state: SyncState,
        isPro: Bool,
        forceBackfill: Bool
    ) -> SyncPlan {
        let window = settings.effectiveHistoryDays(isPro: isPro)
        if forceBackfill || state.backfilledDays < window || state.lastSuccessfulSync == nil {
            let range = DayRange.lastDays(window, endingOn: now, calendar: calendar)
            return SyncPlan(kind: .backfill, range: range, chunks: range.chunked(maxDays: chunkDays))
        }
        var days = incrementalDays
        if let last = state.lastSuccessfulSync {
            let lastDay = calendar.startOfDay(for: last)
            let today = calendar.startOfDay(for: now)
            let gap = (calendar.dateComponents([.day], from: lastDay, to: today).day ?? 0) + 1
            days = max(days, gap)
        }
        days = min(days, window)
        let range = DayRange.lastDays(days, endingOn: now, calendar: calendar)
        return SyncPlan(kind: .sync, range: range, chunks: range.chunked(maxDays: chunkDays))
    }
}

/// Builds batches, encrypts them on device and uploads only ciphertext.
/// An actor so foreground and background triggers never run concurrently.
actor SyncEngine {
    static let shared = SyncEngine()
    static let pairingDidChangeNotification = Notification.Name("HealthBridgePairingDidChange")

    private let health: HealthKitService
    private let log: ActivityLogStore
    private var isRunning = false

    init(health: HealthKitService = .shared, log: ActivityLogStore = .shared) {
        self.health = health
        self.log = log
    }

    @discardableResult
    func run(
        trigger: SyncTrigger,
        isPro: Bool,
        forceBackfill: Bool = false,
        progress: (@Sendable (SyncProgress) -> Void)? = nil
    ) async throws -> SyncReport {
        guard !isRunning else { throw SyncError.alreadyRunning }
        isRunning = true
        defer { isRunning = false }

        let startedAt = Date()
        SettingsStore.updateState { $0.lastAttempt = startedAt }
        do {
            let (report, plan) = try await perform(
                trigger: trigger,
                isPro: isPro,
                forceBackfill: forceBackfill,
                progress: progress
            )
            SettingsStore.updateState { state in
                state.lastSuccessfulSync = report.finishedAt
                state.lastError = nil
                if plan.kind == .backfill {
                    state.backfilledDays = max(state.backfilledDays, plan.range.dayCount)
                }
            }
            log.append(ActivityLogEntry(
                date: startedAt,
                event: .sync,
                trigger: trigger,
                kind: report.kind,
                envelopeCount: report.envelopeCount,
                ciphertextBytes: report.ciphertextBytes,
                dayCount: report.dayCount,
                firstDay: plan.range.dayKeys.first,
                lastDay: plan.range.dayKeys.last,
                duration: Date().timeIntervalSince(startedAt),
                warning: Self.skippedReadsWarning(report.skippedReads)
            ))
            return report
        } catch {
            let syncError = SyncError.wrap(error)
            if case .deviceUnlinked = syncError {
                // The agent unlinked this device: go back to unpaired.
                PairingLifecycle.markUnlinkedByAgent(log: log)
            } else if case .notPaired = syncError {
                // Nothing to record.
            } else {
                SettingsStore.updateState { $0.lastError = syncError.errorDescription }
                log.append(ActivityLogEntry(
                    date: startedAt,
                    event: .sync,
                    trigger: trigger,
                    duration: Date().timeIntervalSince(startedAt),
                    errorMessage: syncError.errorDescription ?? "Sync failed."
                ))
            }
            throw syncError
        }
    }

    /// Upper bound for the JSON around an envelope's ciphertext (id, account, kind, epk,
    /// nonce, kid, timestamps: about 350 bytes), used to keep uploads under the relay limit.
    static let envelopeOverheadBytes = 512

    /// Skipped reads are not retried automatically: they are almost always persistent
    /// (a type this iPhone cannot query), and retrying would turn every sync into a full
    /// history import. The user can re-send the history once the cause is fixed.
    static func skippedReadsWarning(_ count: Int) -> String? {
        guard count > 0 else { return nil }
        let reads = count == 1 ? "1 Health read" : "\(count) Health reads"
        return "\(reads) failed and were left out. Use \"Send full history again\" in Settings to fill the gap."
    }

    private func perform(
        trigger: SyncTrigger,
        isPro: Bool,
        forceBackfill: Bool,
        progress: (@Sendable (SyncProgress) -> Void)?
    ) async throws -> (SyncReport, SyncPlan) {
        guard let pairing = try PairingStore.loadRecord() else { throw SyncError.notPaired }
        guard let signingKey = try DeviceIdentityStore.loadSigningKey() else { throw SyncError.notPaired }
        guard health.isAvailable else { throw SyncError.healthUnavailable }
        let recipient = try pairing.recipientKey()
        let client = try RelayClient(relay: pairing.relay)

        let settings = SettingsStore.loadSettings()
        let calendar = DayKey.localCalendar
        let now = Date()
        let plan = SyncPlan.make(
            now: now,
            calendar: calendar,
            settings: settings,
            state: SettingsStore.loadState(),
            isPro: isPro,
            forceBackfill: forceBackfill
        )

        let builder = HealthBatchBuilder(health: health)
        let heartRateDetails = settings.includesHeartRateDetails(isPro: isPro)
        var options = BatchOptions(categories: settings.categories, heartRateDetails: heartRateDetails)
        if heartRateDetails {
            options.maxReferenceBPM = try await builder.maxReferenceBPM(now: now, calendar: calendar)
        }

        let totalSteps = plan.chunks.count + max(1, (plan.chunks.count + HB1.maxEnvelopesPerRequest - 1) / HB1.maxEnvelopesPerRequest)
        var completedSteps = 0
        func report(_ message: String) {
            progress?(SyncProgress(completed: completedSteps, total: totalSteps, message: message))
        }

        var pending: [Envelope] = []
        var pendingBytes = 0
        var envelopeCount = 0
        var ciphertextBytes = 0

        func flush() async throws {
            guard !pending.isEmpty else { return }
            report("Uploading encrypted data…")
            _ = try await client.upload(envelopes: pending, deviceID: pairing.deviceID, signingKey: signingKey)
            envelopeCount += pending.count
            ciphertextBytes += pending.reduce(0) { $0 + $1.ciphertextSize }
            pending.removeAll()
            pendingBytes = 0
            completedSteps += 1
        }

        for (index, chunk) in plan.chunks.enumerated() {
            try Task.checkCancellation()
            report(plan.chunks.count > 1
                ? "Reading Health data (\(index + 1) of \(plan.chunks.count))…"
                : "Reading Health data…")
            let batch = try await builder.build(range: chunk, options: options, now: now)
            completedSteps += 1
            // A chunk without any data carries nothing for the agent; skipping it saves an
            // envelope. Backfill progress is tracked by the plan, not by uploads.
            if batch.isEmpty { continue }
            let plaintext = try batch.encodedJSON()
            report("Encrypting on this iPhone…")
            let envelope = try EnvelopeCrypto.seal(
                plaintext,
                recipient: recipient,
                account: pairing.account,
                kind: plan.kind,
                compression: .deflate
            )
            let size = envelope.ct.utf8.count + Self.envelopeOverheadBytes
            if !pending.isEmpty
                && (pending.count >= HB1.maxEnvelopesPerRequest || pendingBytes + size > HB1.maxUploadBodyBytes) {
                try await flush()
            }
            pending.append(envelope)
            pendingBytes += size
        }
        try await flush()
        report("Done")

        let result = SyncReport(
            kind: plan.kind,
            envelopeCount: envelopeCount,
            ciphertextBytes: ciphertextBytes,
            dayCount: plan.range.dayCount,
            finishedAt: Date(),
            skippedReads: builder.skippedReads
        )
        return (result, plan)
    }

    /// Unlinks this device at the relay (best effort) and wipes local pairing data. If the
    /// relay is unreachable, local data is wiped anyway; if the wipe fails, nothing local is
    /// reset so the user can try again.
    @discardableResult
    func unpair() async -> UnpairResult {
        var result = UnpairResult(reachedRelay: false)
        do {
            if let pairing = try PairingStore.loadRecord(),
               let signingKey = try DeviceIdentityStore.loadSigningKey() {
                let client = try RelayClient(relay: pairing.relay)
                try await client.unlinkDevice(deviceID: pairing.deviceID, signingKey: signingKey)
                result.reachedRelay = true
            }
        } catch RelayError.deviceUnlinked, RelayError.deviceUnknown {
            result.reachedRelay = true
        } catch {
            Log.sync.error("Unlinking at the relay failed: \(error.localizedDescription, privacy: .public)")
        }
        do {
            try KeychainStore.deleteAll()
        } catch {
            Log.storage.error("Wiping the keychain failed: \(error.localizedDescription, privacy: .public)")
            result.localError = SyncError.wrap(error).errorDescription
            return result
        }
        SettingsStore.resetPairingState()
        log.append(ActivityLogEntry(date: Date(), event: .unpaired))
        NotificationCenter.default.post(name: Self.pairingDidChangeNotification, object: nil)
        return result
    }
}

/// Shared handling when the agent has unlinked this iPhone (`410 device_unlinked`).
enum PairingLifecycle {
    static func markUnlinkedByAgent(log: ActivityLogStore = .shared) {
        guard PairingStore.isPairedIfKnown() == true else { return }
        do {
            try PairingStore.delete()
        } catch {
            // Keep everything consistent and paired: the relay gives the same answer on the
            // next request, and this runs again.
            Log.storage.error("Removing the unlinked pairing failed: \(error.localizedDescription, privacy: .public)")
            return
        }
        SettingsStore.resetPairingState()
        log.append(ActivityLogEntry(
            date: Date(),
            event: .unlinkedByAgent,
            errorMessage: RelayError.deviceUnlinked.errorDescription
        ))
        NotificationCenter.default.post(name: SyncEngine.pairingDidChangeNotification, object: nil)
    }
}
