import BackgroundTasks
import Foundation
import HealthKit
import os
import UIKit

/// Identifiers must match `BGTaskSchedulerPermittedIdentifiers` in Info.plist, which
/// expands `$(PRODUCT_BUNDLE_IDENTIFIER).refresh` / `.backfill` to the same values.
enum BackgroundTaskID {
    static let refresh = AppIdentifiers.refreshTask
    static let backfill = AppIdentifiers.backfillTask
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
    /// Earliest start for a newly scheduled refresh, so a failing sync is not retried
    /// every few minutes.
    private let minimumRefreshDelay: TimeInterval = 5 * 60

    private var registered = false
    private var observerQueries: [HKObserverQuery] = []
    private var observedCategories: Set<MetricCategory> = []
    private var observerSyncTask: Task<Void, Never>?
    private var pendingObserverCompletions: [() -> Void] = []
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid
    private var deliveryClearedThisLaunch = false
    private var deliveryUpdate: Task<Void, Never>?

    private var health: HealthKitService { .shared }

    // MARK: - Registration (must happen before launch finishes)

    func registerTasks() {
        guard !registered else { return }
        registered = true
        let refresh = BGTaskScheduler.shared.register(forTaskWithIdentifier: BackgroundTaskID.refresh, using: nil) { task in
            Task { @MainActor in
                BackgroundSync.shared.handleAppRefresh(task)
            }
        }
        let backfill = BGTaskScheduler.shared.register(forTaskWithIdentifier: BackgroundTaskID.backfill, using: nil) { task in
            Task { @MainActor in
                BackgroundSync.shared.handleBackfill(task)
            }
        }
        if !refresh || !backfill {
            // Only happens when the identifiers are missing from BGTaskSchedulerPermittedIdentifiers;
            // background sync would then never run.
            Log.background.fault("Background task registration failed (refresh: \(refresh), backfill: \(backfill)); check BGTaskSchedulerPermittedIdentifiers in Info.plist")
            assertionFailure("Background task identifiers do not match Info.plist")
        }
    }

    // MARK: - Configuration

    /// Applies the current sync mode: schedules or cancels refresh tasks and starts or
    /// stops HealthKit observers. Safe to call often.
    func applyConfiguration() {
        // Keychain not readable (e.g. launched before the first unlock): keep the current setup.
        guard let paired = PairingStore.isPairedIfKnown() else { return }
        let settings = SettingsStore.loadSettings()
        let active = paired && SettingsStore.onboardingComplete
        let mode = active ? settings.effectiveMode(isPro: EntitlementCache.isPro) : .manual

        switch mode {
        case .manual:
            stopObservers()
            cancelRefresh()
        case .scheduled:
            stopObservers()
            scheduleRefresh(at: nextRefreshDate(interval: settings.interval.seconds))
        case .onNewData:
            startObservers(categories: settings.categories)
            scheduleRefresh(at: nextRefreshDate(interval: onNewDataFallbackInterval))
        }
    }

    /// The next run is anchored to the last sync attempt, successful or not: opening the app
    /// does not keep pushing the background sync further out, and a sync that keeps failing
    /// is retried once per interval instead of every few minutes.
    private func nextRefreshDate(interval: TimeInterval) -> Date {
        let state = SettingsStore.loadState()
        let anchor = [state.lastSuccessfulSync, state.lastAttempt].compactMap { $0 }.max() ?? Date()
        return max(anchor.addingTimeInterval(interval), Date(timeIntervalSinceNow: minimumRefreshDelay))
    }

    func scheduleRefresh(at earliest: Date) {
        let request = BGAppRefreshTaskRequest(identifier: BackgroundTaskID.refresh)
        request.earliestBeginDate = earliest
        do {
            try BGTaskScheduler.shared.submit(request)
            SettingsStore.updateState { $0.nextScheduledSync = earliest }
        } catch {
            // Simulator or Background App Refresh turned off; Home shows "Background refresh off".
            Log.background.error("Scheduling the background refresh failed: \(error.localizedDescription, privacy: .public)")
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
        guard PairingStore.isPairedIfKnown() == true, SettingsStore.onboardingComplete else { return }
        let settings = SettingsStore.loadSettings()
        let state = SettingsStore.loadState()
        guard state.backfilledDays < settings.effectiveHistoryDays(isPro: EntitlementCache.isPro) else { return }
        let request = BGProcessingTaskRequest(identifier: BackgroundTaskID.backfill)
        request.requiresNetworkConnectivity = true
        request.requiresExternalPower = false
        request.earliestBeginDate = Date(timeIntervalSinceNow: 60)
        do {
            try BGTaskScheduler.shared.submit(request)
        } catch {
            // Simulator or Background App Refresh turned off: the next sync in the app
            // continues the import.
            Log.background.error("Scheduling the history import failed: \(error.localizedDescription, privacy: .public)")
        }
    }

    func cancelAll() {
        stopObservers()
        BGTaskScheduler.shared.cancelAllTaskRequests()
        SettingsStore.updateState { $0.nextScheduledSync = nil }
    }

    // MARK: - BGTask handlers

    private func handleAppRefresh(_ task: BGTask) {
        let completion = TaskCompletion(task)
        // Always queue the next run first (in case this one expires), then again after the
        // sync so the next run is anchored to the new "last sync".
        applyConfiguration()
        let work = Task { @MainActor in
            let success = await self.runSync(trigger: .scheduled, requiresPro: true)
            self.applyConfiguration()
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
        guard let paired = PairingStore.isPairedIfKnown() else {
            // Keychain not readable yet (before the first unlock after a reboot): retry later.
            return false
        }
        guard paired, SettingsStore.onboardingComplete else { return true }
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
        let store = health.store
        observerQueries.forEach { store.stop($0) }
        observerQueries.removeAll()

        let wanted = health.observerTypes(for: categories)
        // Drop registrations for categories that are no longer shared (they may survive
        // from an earlier launch).
        let unwanted = health.observerTypes(for: Set(MetricCategory.allCases)).filter { !wanted.contains($0) }
        observedCategories = categories
        deliveryClearedThisLaunch = true

        for type in wanted {
            let query = HKObserverQuery(sampleType: type, predicate: nil) { _, completionHandler, error in
                if let error {
                    Log.background.error("Observer query failed: \(error.localizedDescription, privacy: .public)")
                    completionHandler()
                    return
                }
                Task { @MainActor in
                    BackgroundSync.shared.healthDataChanged(completion: completionHandler)
                }
            }
            store.execute(query)
            observerQueries.append(query)
        }

        let fallbackHours = Int(onNewDataFallbackInterval / 3600)
        updateDelivery { store in
            for type in unwanted {
                do {
                    try await store.disableBackgroundDelivery(for: type)
                } catch {
                    Log.background.error("Disabling background delivery for \(type.identifier, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                }
            }
            var lastFailure: Error?
            for type in wanted {
                do {
                    try await store.enableBackgroundDelivery(for: type, frequency: .hourly)
                } catch {
                    Log.background.error("Enabling background delivery for \(type.identifier, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                    lastFailure = error
                }
            }
            guard let failure = lastFailure else { return nil }
            return "Apple Health did not allow background updates (\(failure.localizedDescription)). HealthBridge still syncs about every \(fallbackHours) hours."
        }
    }

    private func stopObservers() {
        // Background-delivery registrations survive relaunches, so clear them at least once
        // per launch even if no observer is running in this process.
        guard !observerQueries.isEmpty || !observedCategories.isEmpty || !deliveryClearedThisLaunch else { return }
        deliveryClearedThisLaunch = true
        let store = health.store
        observerQueries.forEach { store.stop($0) }
        observerQueries.removeAll()
        observedCategories = []
        updateDelivery { store in
            do {
                try await store.disableAllBackgroundDelivery()
            } catch {
                Log.background.error("Disabling background delivery failed: \(error.localizedDescription, privacy: .public)")
            }
            return nil
        }
    }

    /// HealthKit applies background-delivery changes asynchronously. Running them one after
    /// another keeps an earlier "disable all" from landing after a later "enable". The
    /// update returns a problem for the user (shown on Home) or nil.
    private func updateDelivery(_ update: @escaping @MainActor (HKHealthStore) async -> String?) {
        let previous = deliveryUpdate
        let store = health.store
        deliveryUpdate = Task { @MainActor in
            await previous?.value
            let problem = await update(store)
            SettingsStore.updateState { $0.backgroundDeliveryProblem = problem }
        }
    }

    /// Called for every observer update. Coalesces bursts into one sync and always calls
    /// every HealthKit completion handler.
    private func healthDataChanged(completion: @escaping () -> Void) {
        pendingObserverCompletions.append(completion)
        guard observerSyncTask == nil else { return }

        if let last = SettingsStore.loadState().lastSuccessfulSync,
           Date().timeIntervalSince(last) < healthUpdateThrottle {
            // Too soon after the last sync. Ask for a refresh when the throttle window ends,
            // so this update does not wait for the next observer wake-up or the fallback.
            scheduleRefresh(at: last.addingTimeInterval(healthUpdateThrottle))
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
