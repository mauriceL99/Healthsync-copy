import BackgroundTasks
import Foundation
import HealthKit
import UIKit

/// Identifiers must match `BGTaskSchedulerPermittedIdentifiers` in Info.plist.
enum BackgroundTaskID {
    static let refresh = "com.example.healthbridge.refresh"
    static let backfill = "com.example.healthbridge.backfill"
}

/// Calls a BGTask completion exactly once, whichever of "work finished" and
/// "expiration handler fired" happens first.
private final class TaskCompletion: @unchecked Sendable {
    private let task: BGTask
    private let lock = NSLock()
    private var done = false

    init(_ task: BGTask) {
        self.task = task
    }

    func complete(success: Bool) {
        lock.lock()
        let alreadyDone = done
        done = true
        lock.unlock()
        if !alreadyDone {
            task.setTaskCompleted(success: success)
        }
    }
}

/// Background execution: BGAppRefreshTask for "Scheduled", HKObserverQuery + background
/// delivery for "On new Health data", BGProcessingTask for large history imports.
@MainActor
final class BackgroundSync {
    static let shared = BackgroundSync()

    /// Minimum spacing between syncs triggered by HealthKit background delivery.
    private let healthUpdateThrottle: TimeInterval = 15 * 60
    /// Safety-net refresh interval while in "On new data" mode.
    private let onNewDataFallbackInterval: TimeInterval = 12 * 3600

    private var registered = false
    private var observerQueries: [HKObserverQuery] = []
    private var observedCategories: Set<MetricCategory> = []
    private var observerSyncTask: Task<Void, Never>?
    private var pendingObserverCompletions: [() -> Void] = []
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid

    private var health: HealthKitService { .shared }

    // MARK: - Registration (must happen before launch finishes)

    func registerTasks() {
        guard !registered else { return }
        registered = true
        _ = BGTaskScheduler.shared.register(forTaskWithIdentifier: BackgroundTaskID.refresh, using: nil) { task in
            Task { @MainActor in
                BackgroundSync.shared.handleAppRefresh(task)
            }
        }
        _ = BGTaskScheduler.shared.register(forTaskWithIdentifier: BackgroundTaskID.backfill, using: nil) { task in
            Task { @MainActor in
                BackgroundSync.shared.handleBackfill(task)
            }
        }
    }

    // MARK: - Configuration

    /// Applies the current sync mode: schedules or cancels refresh tasks and starts or
    /// stops HealthKit observers. Safe to call often.
    func applyConfiguration() {
        let settings = SettingsStore.loadSettings()
        let active = PairingStore.isPaired && SettingsStore.onboardingComplete
        let mode = active ? settings.effectiveMode(isPro: EntitlementCache.isPro) : .manual

        switch mode {
        case .manual:
            stopObservers()
            cancelRefresh()
        case .scheduled:
            stopObservers()
            scheduleRefresh(after: settings.interval.seconds)
        case .onNewData:
            startObservers(categories: settings.categories)
            scheduleRefresh(after: onNewDataFallbackInterval)
        }
    }

    func scheduleRefresh(after interval: TimeInterval) {
        let request = BGAppRefreshTaskRequest(identifier: BackgroundTaskID.refresh)
        let earliest = Date(timeIntervalSinceNow: interval)
        request.earliestBeginDate = earliest
        do {
            try BGTaskScheduler.shared.submit(request)
            SettingsStore.updateState { $0.nextScheduledSync = earliest }
        } catch {
            // Simulator or background refresh disabled by the user.
            SettingsStore.updateState { $0.nextScheduledSync = nil }
        }
    }

    func cancelRefresh() {
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: BackgroundTaskID.refresh)
        SettingsStore.updateState { $0.nextScheduledSync = nil }
    }

    /// Schedules a BGProcessingTask when the history import has not completed yet
    /// (for example because the app was closed during the first sync).
    func scheduleBackfillIfNeeded() {
        guard PairingStore.isPaired, SettingsStore.onboardingComplete else { return }
        let settings = SettingsStore.loadSettings()
        let state = SettingsStore.loadState()
        guard state.backfilledDays < settings.effectiveHistoryDays(isPro: EntitlementCache.isPro) else { return }
        let request = BGProcessingTaskRequest(identifier: BackgroundTaskID.backfill)
        request.requiresNetworkConnectivity = true
        request.requiresExternalPower = false
        request.earliestBeginDate = Date(timeIntervalSinceNow: 60)
        try? BGTaskScheduler.shared.submit(request)
    }

    func cancelAll() {
        stopObservers()
        BGTaskScheduler.shared.cancelAllTaskRequests()
        SettingsStore.updateState { $0.nextScheduledSync = nil }
    }

    // MARK: - BGTask handlers

    private func handleAppRefresh(_ task: BGTask) {
        let completion = TaskCompletion(task)
        // Always queue the next run first.
        applyConfiguration()
        let work = Task { @MainActor in
            let success = await self.runSync(trigger: .scheduled, requiresPro: true)
            completion.complete(success: success)
        }
        task.expirationHandler = {
            work.cancel()
            completion.complete(success: false)
        }
    }

    private func handleBackfill(_ task: BGTask) {
        let completion = TaskCompletion(task)
        let work = Task { @MainActor in
            let success = await self.runSync(trigger: .backgroundBackfill, requiresPro: false)
            completion.complete(success: success)
        }
        task.expirationHandler = {
            work.cancel()
            completion.complete(success: false)
        }
    }

    /// Runs one sync. Returns false if it should be retried.
    private func runSync(trigger: SyncTrigger, requiresPro: Bool) async -> Bool {
        guard PairingStore.isPaired, SettingsStore.onboardingComplete else { return true }
        let isPro = await PurchaseManager.verifiedProEntitlement()
        EntitlementCache.isPro = isPro
        if requiresPro && !isPro {
            applyConfiguration()
            return true
        }
        do {
            try await SyncEngine.shared.run(trigger: trigger, isPro: isPro)
            return true
        } catch SyncError.deviceUnlinked {
            cancelAll()
            return true
        } catch SyncError.alreadyRunning {
            return true
        } catch {
            if case .cancelled = SyncError.wrap(error) {
                scheduleBackfillIfNeeded()
            }
            return false
        }
    }

    // MARK: - HealthKit background delivery

    private func startObservers(categories: Set<MetricCategory>) {
        if !observerQueries.isEmpty && observedCategories == categories {
            return
        }
        stopObservers()
        observedCategories = categories
        let store = health.store
        for type in health.observerTypes(for: categories) {
            let query = HKObserverQuery(sampleType: type, predicate: nil) { _, completionHandler, error in
                if error != nil {
                    completionHandler()
                    return
                }
                Task { @MainActor in
                    BackgroundSync.shared.healthDataChanged(completion: completionHandler)
                }
            }
            store.execute(query)
            observerQueries.append(query)
            store.enableBackgroundDelivery(for: type, frequency: .hourly) { _, _ in }
        }
    }

    private func stopObservers() {
        guard !observerQueries.isEmpty || !observedCategories.isEmpty else { return }
        let store = health.store
        observerQueries.forEach { store.stop($0) }
        observerQueries.removeAll()
        observedCategories = []
        store.disableAllBackgroundDelivery { _, _ in }
    }

    /// Called for every observer update. Coalesces bursts into one sync and always calls
    /// every HealthKit completion handler.
    private func healthDataChanged(completion: @escaping () -> Void) {
        pendingObserverCompletions.append(completion)
        guard observerSyncTask == nil else { return }

        if let last = SettingsStore.loadState().lastSuccessfulSync,
           Date().timeIntervalSince(last) < healthUpdateThrottle {
            flushObserverCompletions()
            return
        }

        backgroundTaskID = UIApplication.shared.beginBackgroundTask(withName: "HealthBridge sync") {
            MainActor.assumeIsolated {
                BackgroundSync.shared.observerSyncTask?.cancel()
                BackgroundSync.shared.endBackgroundTask()
            }
        }
        observerSyncTask = Task { @MainActor in
            _ = await self.runSync(trigger: .healthUpdate, requiresPro: true)
            self.observerSyncTask = nil
            self.flushObserverCompletions()
            self.endBackgroundTask()
        }
    }

    private func flushObserverCompletions() {
        let completions = pendingObserverCompletions
        pendingObserverCompletions.removeAll()
        completions.forEach { $0() }
    }

    private func endBackgroundTask() {
        guard backgroundTaskID != .invalid else { return }
        UIApplication.shared.endBackgroundTask(backgroundTaskID)
        backgroundTaskID = .invalid
    }
}
