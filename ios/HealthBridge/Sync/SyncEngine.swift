import CryptoKit
import Foundation

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

    var running: Bool {
        isRunning
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
        var plannedKind: EnvelopeKind?
        do {
            let (report, plan) = try await perform(
                trigger: trigger,
                isPro: isPro,
                forceBackfill: forceBackfill,
                progress: progress
            )
            plannedKind = plan.kind
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
                duration: Date().timeIntervalSince(startedAt)
            ))
            return report
        } catch {
            let syncError = SyncError.wrap(error)
            if case .deviceUnlinked = syncError {
                handleUnlinked()
            } else if case .notPaired = syncError {
                // Nothing to record.
            } else {
                SettingsStore.updateState { $0.lastError = syncError.errorDescription }
                log.append(ActivityLogEntry(
                    date: startedAt,
                    event: .sync,
                    trigger: trigger,
                    kind: plannedKind,
                    duration: Date().timeIntervalSince(startedAt),
                    errorMessage: syncError.errorDescription ?? "Sync failed."
                ))
            }
            throw syncError
        }
    }

    /// The relay reported that the agent unlinked this device: go back to unpaired.
    private func handleUnlinked() {
        PairingStore.delete()
        SettingsStore.resetPairingState()
        log.append(ActivityLogEntry(
            date: Date(),
            event: .unlinkedByAgent,
            errorMessage: RelayError.deviceUnlinked.errorDescription
        ))
        NotificationCenter.default.post(name: Self.pairingDidChangeNotification, object: nil)
    }

    private func perform(
        trigger: SyncTrigger,
        isPro: Bool,
        forceBackfill: Bool,
        progress: (@Sendable (SyncProgress) -> Void)?
    ) async throws -> (SyncReport, SyncPlan) {
        guard let pairing = PairingStore.load() else { throw SyncError.notPaired }
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

        let heartRateDetails = settings.includesHeartRateDetails(isPro: isPro)
        var options = BatchOptions(categories: settings.categories, heartRateDetails: heartRateDetails)
        if heartRateDetails {
            options.maxReferenceBPM = await maxReferenceBPM(now: now, calendar: calendar)
        }

        let builder = HealthBatchBuilder(health: health)
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
            let plaintext = try batch.encodedJSON()
            report("Encrypting on this iPhone…")
            let envelope = try EnvelopeCrypto.seal(
                plaintext,
                recipient: recipient,
                account: pairing.account,
                kind: plan.kind,
                compression: .deflate
            )
            completedSteps += 1
            let size = envelope.ct.utf8.count + 512
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
            finishedAt: Date()
        )
        return (result, plan)
    }

    /// `220 − age`, else the highest heart rate of the last 365 days, else 190.
    private func maxReferenceBPM(now: Date, calendar: Calendar) async -> Double {
        let age = health.age(on: now)
        var observedMax: Double?
        if age == nil, let yearAgo = calendar.date(byAdding: .day, value: -365, to: now) {
            observedMax = try? await health.maximumHeartRate(since: yearAgo)
        }
        return HeartRateAnalyzer.maxReference(age: age, observedMax: observedMax)
    }

    /// Unlinks this device at the relay (best effort) and wipes local pairing data.
    func unpair() async {
        if let pairing = PairingStore.load(),
           let signingKey = try? DeviceIdentityStore.loadSigningKey(),
           let client = try? RelayClient(relay: pairing.relay) {
            try? await client.unlinkDevice(deviceID: pairing.deviceID, signingKey: signingKey)
        }
        KeychainStore.deleteAll()
        SettingsStore.resetPairingState()
        log.append(ActivityLogEntry(date: Date(), event: .unpaired))
        NotificationCenter.default.post(name: Self.pairingDidChangeNotification, object: nil)
    }
}
