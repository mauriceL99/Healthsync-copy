import Combine
import Foundation
import os
import UIKit

/// Central UI state. All mutations happen on the main actor; heavy work runs in
/// `SyncEngine` (an actor) and HealthKit's own queues.
@MainActor
final class AppModel: ObservableObject {
    enum SyncStatus: Equatable {
        case idle
        case running(SyncProgress)
        case succeeded(SyncReport)
        case failed(String)
    }

    @Published private(set) var pairing: PairingRecord?
    @Published private(set) var onboardingComplete: Bool
    @Published private(set) var settings: SyncSettings
    @Published private(set) var syncState: SyncState
    @Published private(set) var status: SyncStatus = .idle
    @Published private(set) var logEntries: [ActivityLogEntry] = []
    @Published private(set) var remoteStatus: DeviceStatusResponse?
    @Published private(set) var needsHealthAuthorization = false
    /// One-off message shown as an alert (e.g. "unlinked by your agent").
    @Published var notice: String?

    let purchases: PurchaseManager
    let health: HealthKitService = .shared

    private var cancellables = Set<AnyCancellable>()

    init() {
        // Also called in AppDelegate. SwiftUI may create this model before
        // didFinishLaunching runs, and it must never load a pairing left by a previous
        // install; the call is idempotent, so whichever runs first does the work.
        SettingsStore.prepareFirstLaunchIfNeeded()
        purchases = PurchaseManager()
        pairing = PairingStore.load()
        onboardingComplete = SettingsStore.onboardingComplete
        settings = SettingsStore.loadSettings()
        syncState = SettingsStore.loadState()
        logEntries = ActivityLogStore.shared.entries()

        NotificationCenter.default.publisher(for: SyncEngine.pairingDidChangeNotification)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in self?.pairingChangedExternally() }
            .store(in: &cancellables)

        NotificationCenter.default.publisher(for: ActivityLogStore.didChangeNotification)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in self?.reloadLog() }
            .store(in: &cancellables)

        purchases.$isPro
            .removeDuplicates()
            .dropFirst()
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in self?.entitlementChanged() }
            .store(in: &cancellables)
    }

    // MARK: - Derived state

    var isPro: Bool {
        purchases.isPro
    }

    var isPaired: Bool {
        pairing != nil
    }

    var isSyncing: Bool {
        if case .running = status {
            return true
        }
        return false
    }

    var effectiveMode: SyncMode {
        settings.effectiveMode(isPro: isPro)
    }

    var effectiveHistoryDays: Int {
        settings.effectiveHistoryDays(isPro: isPro)
    }

    var backfillPending: Bool {
        isPaired && syncState.backfilledDays < effectiveHistoryDays
    }

    // MARK: - Lifecycle

    func reload() {
        pairing = PairingStore.load()
        onboardingComplete = SettingsStore.onboardingComplete
        settings = SettingsStore.loadSettings()
        syncState = SettingsStore.loadState()
        reloadLog()
    }

    private func reloadLog() {
        logEntries = ActivityLogStore.shared.entries()
    }

    func didBecomeActive() async {
        reload()
        await purchases.refreshEntitlements()
        BackgroundSync.shared.applyConfiguration()
        syncState = SettingsStore.loadState()
        guard isPaired else { return }
        await refreshHealthAuthorizationStatus()
        await refreshRemoteStatus()
    }

    func didEnterBackground() {
        BackgroundSync.shared.scheduleBackfillIfNeeded()
    }

    private func pairingChangedExternally() {
        let wasPaired = pairing != nil
        reload()
        if wasPaired && pairing == nil {
            remoteStatus = nil
            status = .idle
            if logEntries.first?.event == .unlinkedByAgent {
                notice = RelayError.deviceUnlinked.errorDescription
            }
        }
        BackgroundSync.shared.applyConfiguration()
    }

    private func entitlementChanged() {
        BackgroundSync.shared.applyConfiguration()
        Task { await refreshHealthAuthorizationStatus() }
    }

    // MARK: - Settings

    func updateSettings(_ change: (inout SyncSettings) -> Void) {
        var updated = settings
        change(&updated)
        guard updated != settings else { return }
        let categoriesChanged = updated.categories != settings.categories
            || updated.workoutHeartRateDetails != settings.workoutHeartRateDetails
        settings = updated
        SettingsStore.saveSettings(updated)
        BackgroundSync.shared.applyConfiguration()
        if categoriesChanged {
            Task { await refreshHealthAuthorizationStatus() }
        }
    }

    // MARK: - Pairing

    /// Registers this iPhone with the relay (`POST /v1/devices`) after the user confirmed
    /// the safety code.
    func link(_ payload: PairingPayload) async throws {
        let client = try RelayClient(relay: payload.relay)
        let signingKey = try DeviceIdentityStore.loadOrCreateSigningKey()
        let response = try await client.registerDevice(
            pairing: payload,
            signingKey: signingKey,
            deviceName: Self.deviceName,
            appVersion: AppInfo.version
        )
        guard response.account == payload.account, HB1.isValidDeviceID(response.deviceID) else {
            throw RelayError.invalidResponse
        }
        let record = PairingRecord(
            relay: payload.relay,
            account: payload.account,
            agentSigningKey: B64.encode(payload.agentSigningKey),
            agentEncryptionKey: B64.encode(payload.agentEncryptionKey),
            agentName: payload.name,
            deviceID: response.deviceID,
            linkedAt: response.linkedAt.flatMap { RFC3339.date(from: $0) } ?? Date(),
            safetyCode: payload.safetyCode,
            keyID: payload.keyID
        )
        try PairingStore.save(record)
        SettingsStore.markInstalled()
        SettingsStore.saveState(SyncState())
        SettingsStore.onboardingComplete = false
        ActivityLogStore.shared.append(ActivityLogEntry(date: Date(), event: .paired))
        reload()
    }

    func completeOnboarding() {
        SettingsStore.onboardingComplete = true
        onboardingComplete = true
        BackgroundSync.shared.applyConfiguration()
    }

    /// Unlinks at the relay (`DELETE /v1/devices/self`) and wipes the keychain.
    @discardableResult
    func unpair() async -> UnpairResult {
        BackgroundSync.shared.cancelAll()
        let result = await SyncEngine.shared.unpair()
        remoteStatus = nil
        status = .idle
        reload()
        BackgroundSync.shared.applyConfiguration()
        return result
    }

    func refreshRemoteStatus() async {
        guard let pairing else { return }
        do {
            guard let signingKey = try DeviceIdentityStore.loadSigningKey() else { return }
            let client = try RelayClient(relay: pairing.relay)
            remoteStatus = try await client.deviceStatus(deviceID: pairing.deviceID, signingKey: signingKey)
        } catch RelayError.deviceUnlinked {
            PairingLifecycle.markUnlinkedByAgent()
        } catch {
            // Keep the last known status until the next refresh succeeds.
            Log.sync.error("Device status refresh failed: \(error.localizedDescription, privacy: .public)")
        }
    }

    private static var deviceName: String {
        let name = HB1.cleanLabel(UIDevice.current.name)
        return name.isEmpty ? "iPhone" : name
    }

    // MARK: - Health

    var heartRateDetailsActive: Bool {
        settings.includesHeartRateDetails(isPro: isPro)
    }

    func requestHealthAuthorization() async throws {
        try await health.requestAuthorization(
            categories: settings.categories,
            heartRateDetails: heartRateDetailsActive
        )
        await refreshHealthAuthorizationStatus()
    }

    func refreshHealthAuthorizationStatus() async {
        needsHealthAuthorization = await health.needsAuthorizationRequest(
            categories: settings.categories,
            heartRateDetails: heartRateDetailsActive
        )
    }

    // MARK: - Sync

    func syncNow(trigger: SyncTrigger = .manual, forceBackfill: Bool = false) async {
        guard !isSyncing, isPaired else { return }
        status = .running(.starting("Preparing…"))
        do {
            let report = try await SyncEngine.shared.run(
                trigger: trigger,
                isPro: isPro,
                forceBackfill: forceBackfill
            ) { progress in
                Task { @MainActor [weak self] in
                    guard let self, case .running = self.status else { return }
                    self.status = .running(progress)
                }
            }
            status = .succeeded(report)
            reload()
            BackgroundSync.shared.applyConfiguration()
            await refreshRemoteStatus()
        } catch {
            let syncError = SyncError.wrap(error)
            reload()
            switch syncError {
            case .deviceUnlinked:
                status = .idle
            case .alreadyRunning:
                status = .failed("A background sync is running right now. Try again in a moment.")
            default:
                status = .failed(syncError.errorDescription ?? "Sync failed.")
            }
        }
    }
}
