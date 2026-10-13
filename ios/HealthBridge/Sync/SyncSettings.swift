import Foundation
import os

enum SyncMode: String, Codable, CaseIterable, Identifiable, Sendable {
    case manual
    case scheduled
    case onNewData

    var id: String { rawValue }

    var title: String {
        switch self {
        case .manual: return "Manual"
        case .scheduled: return "Scheduled"
        case .onNewData: return "On new data"
        }
    }

    var explanation: String {
        switch self {
        case .manual:
            return "Data is only sent when you tap “Sync now”."
        case .scheduled:
            return "iOS runs the sync in the background, at most at the chosen interval. The exact time is decided by iOS."
        case .onNewData:
            return "Apple Health wakes HealthBridge when new data arrives (at most hourly) and it syncs automatically."
        }
    }

    var requiresPro: Bool {
        self != .manual
    }
}

enum SyncInterval: Int, Codable, CaseIterable, Identifiable, Sendable {
    case hour1 = 1
    case hours3 = 3
    case hours6 = 6
    case hours12 = 12
    case hours24 = 24

    var id: Int { rawValue }

    var seconds: TimeInterval {
        TimeInterval(rawValue) * 3600
    }

    var title: String {
        rawValue == 1 ? "Every hour" : "Every \(rawValue) hours"
    }

    /// "1, 3, 6, 12 or 24 hours", built from the cases so texts never drift.
    static var choicesText: String {
        let values = allCases.map { String($0.rawValue) }
        guard let last = values.last else { return "" }
        return values.dropLast().joined(separator: ", ") + " or " + last + " hours"
    }
}

enum HistoryWindow: Int, Codable, CaseIterable, Identifiable, Sendable {
    case week = 7
    case month = 30
    case quarter = 90
    case year = 365

    var id: Int { rawValue }

    var title: String {
        "\(rawValue) days"
    }

    var requiresPro: Bool {
        rawValue > Self.freeLimit.rawValue
    }

    static let freeLimit: HistoryWindow = .week
    static let proLimit: HistoryWindow = .year
}

/// User preferences. Stored as JSON in UserDefaults (no health data).
struct SyncSettings: Codable, Equatable, Sendable {
    var categories: Set<MetricCategory>
    var workoutHeartRateDetails: Bool
    var mode: SyncMode
    var interval: SyncInterval
    var historyWindow: HistoryWindow

    static let `default` = SyncSettings(
        categories: Set(MetricCategory.allCases),
        workoutHeartRateDetails: true,
        mode: .manual,
        interval: .hours6,
        historyWindow: .week
    )

    init(
        categories: Set<MetricCategory>,
        workoutHeartRateDetails: Bool,
        mode: SyncMode,
        interval: SyncInterval,
        historyWindow: HistoryWindow
    ) {
        self.categories = categories
        self.workoutHeartRateDetails = workoutHeartRateDetails
        self.mode = mode
        self.interval = interval
        self.historyWindow = historyWindow
    }

    // Tolerant decoding so new fields never wipe existing preferences.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let fallback = SyncSettings.default
        categories = (try? container.decode(Set<MetricCategory>.self, forKey: .categories)) ?? fallback.categories
        workoutHeartRateDetails = (try? container.decode(Bool.self, forKey: .workoutHeartRateDetails))
            ?? fallback.workoutHeartRateDetails
        mode = (try? container.decode(SyncMode.self, forKey: .mode)) ?? fallback.mode
        interval = (try? container.decode(SyncInterval.self, forKey: .interval)) ?? fallback.interval
        historyWindow = (try? container.decode(HistoryWindow.self, forKey: .historyWindow)) ?? fallback.historyWindow
    }

    /// The history window that is actually used for the current entitlement.
    func effectiveHistoryWindow(isPro: Bool) -> HistoryWindow {
        !isPro && historyWindow.requiresPro ? .freeLimit : historyWindow
    }

    func effectiveHistoryDays(isPro: Bool) -> Int {
        effectiveHistoryWindow(isPro: isPro).rawValue
    }

    func effectiveMode(isPro: Bool) -> SyncMode {
        isPro ? mode : .manual
    }

    func includesHeartRateDetails(isPro: Bool) -> Bool {
        isPro && workoutHeartRateDetails && categories.contains(.workouts)
    }
}

/// Bookkeeping about past syncs. Stored in UserDefaults (no health data).
struct SyncState: Codable, Equatable, Sendable {
    var lastSuccessfulSync: Date?
    var lastAttempt: Date?
    var lastError: String?
    /// Number of days covered by the last completed backfill (0 = none yet).
    var backfilledDays: Int = 0
    var nextScheduledSync: Date?
    /// Why "On new Health data" cannot work right now (HealthKit refused background delivery).
    var backgroundDeliveryProblem: String?

    init() {}

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        lastSuccessfulSync = try? container.decode(Date.self, forKey: .lastSuccessfulSync)
        lastAttempt = try? container.decode(Date.self, forKey: .lastAttempt)
        lastError = try? container.decode(String.self, forKey: .lastError)
        backfilledDays = (try? container.decode(Int.self, forKey: .backfilledDays)) ?? 0
        nextScheduledSync = try? container.decode(Date.self, forKey: .nextScheduledSync)
        backgroundDeliveryProblem = try? container.decode(String.self, forKey: .backgroundDeliveryProblem)
    }
}

enum SettingsStore {
    private static let settingsKey = "healthbridge.settings.v1"
    private static let stateKey = "healthbridge.syncState.v1"
    private static let onboardingKey = "healthbridge.onboardingComplete"
    private static let installedKey = "healthbridge.installed"

    private static var defaults: UserDefaults { .standard }

    static func loadSettings() -> SyncSettings {
        guard let data = defaults.data(forKey: settingsKey),
              let settings = try? JSONDecoder().decode(SyncSettings.self, from: data) else {
            return .default
        }
        return settings
    }

    static func saveSettings(_ settings: SyncSettings) {
        if let data = try? JSONEncoder().encode(settings) {
            defaults.set(data, forKey: settingsKey)
        }
    }

    static func loadState() -> SyncState {
        guard let data = defaults.data(forKey: stateKey),
              let state = try? JSONDecoder().decode(SyncState.self, from: data) else {
            return SyncState()
        }
        return state
    }

    static func saveState(_ state: SyncState) {
        if let data = try? JSONEncoder().encode(state) {
            defaults.set(data, forKey: stateKey)
        }
    }

    @discardableResult
    static func updateState(_ change: (inout SyncState) -> Void) -> SyncState {
        var state = loadState()
        change(&state)
        saveState(state)
        return state
    }

    static var onboardingComplete: Bool {
        get { defaults.bool(forKey: onboardingKey) }
        set { defaults.set(newValue, forKey: onboardingKey) }
    }

    /// Resets everything that belongs to a pairing (used on unpair / unlink).
    static func resetPairingState() {
        defaults.removeObject(forKey: stateKey)
        onboardingComplete = false
    }

    /// Keychain items survive app deletion. On the first launch after a (re)install we drop
    /// the stale pairing record; the device key is kept so pairing again with the same QR
    /// code is idempotent on the relay (docs/PROTOCOL.md §5).
    static func prepareFirstLaunchIfNeeded() {
        guard !defaults.bool(forKey: installedKey) else { return }
        do {
            try PairingStore.delete()
        } catch {
            // Keychain not writable right now: try again on the next launch. A pairing saved
            // in the meantime replaces the stale record and calls `markInstalled()`.
            Log.storage.error("Removing the stale pairing record failed: \(error.localizedDescription, privacy: .public)")
            return
        }
        markInstalled()
    }

    /// Records that no pairing from a previous install is left in the keychain.
    static func markInstalled() {
        defaults.set(true, forKey: installedKey)
    }
}

/// Cached StoreKit entitlement so background tasks can decide quickly. Refreshed from
/// `Transaction.currentEntitlements` whenever possible.
enum EntitlementCache {
    private static let key = "healthbridge.pro.cached"

    static var isPro: Bool {
        get { UserDefaults.standard.bool(forKey: key) }
        set { UserDefaults.standard.set(newValue, forKey: key) }
    }
}

enum AppInfo {
    /// The marketing version, or "0" when it is missing or unusable (never a plausible
    /// made-up version).
    static var version: String {
        let raw = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        return sanitizedVersion(raw) ?? "0"
    }

    static var build: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "1"
    }

    /// `^[0-9A-Za-z.+-]{1,32}$`
    static func sanitizedVersion(_ value: String) -> String? {
        let allowed = Set("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.+-")
        let filtered = String(value.filter { allowed.contains($0) }.prefix(32))
        return filtered.isEmpty ? nil : filtered
    }

    static var osVersion: String {
        let version = ProcessInfo.processInfo.operatingSystemVersion
        let raw = "\(version.majorVersion).\(version.minorVersion).\(version.patchVersion)"
        return sanitizedVersion(raw) ?? "0"
    }

    /// Hardware identifier from `utsname().machine`, e.g. `iPhone16,1`. Returns nil when it
    /// does not match `^[A-Za-z]{2,16}[0-9]{0,3}(,[0-9]{1,3})?$`. In the Simulator `machine`
    /// is the host CPU (`arm64` – which would even match the pattern – or `x86_64`), so the
    /// simulated model identifier is used instead, or nothing.
    static var deviceModel: String? {
        #if targetEnvironment(simulator)
        let machine = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] ?? ""
        #else
        var systemInfo = utsname()
        uname(&systemInfo)
        let machine = withUnsafeBytes(of: systemInfo.machine) { buffer -> String in
            let bytes = buffer.prefix { $0 != 0 }
            return String(decoding: bytes, as: UTF8.self)
        }
        #endif
        return isValidDeviceModel(machine) ? machine : nil
    }

    static func isValidDeviceModel(_ value: String) -> Bool {
        value.range(of: #"^[A-Za-z]{2,16}[0-9]{0,3}(,[0-9]{1,3})?$"#, options: .regularExpression) != nil
    }

    /// IANA time zone name matching `^[A-Za-z][A-Za-z0-9_+./-]{1,63}$`, or nil. Day keys
    /// are always local, so claiming "UTC" for an unusual identifier would be wrong;
    /// omitting the field lets the agent know the zone is unknown.
    static var timeZoneIdentifier: String? {
        let identifier = TimeZone.current.identifier
        if identifier.range(of: #"^[A-Za-z][A-Za-z0-9_+./-]{1,63}$"#, options: .regularExpression) != nil {
            return identifier
        }
        return nil
    }
}
