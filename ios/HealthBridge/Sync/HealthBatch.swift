import Foundation

/// Plaintext of every envelope (docs/DATA_SCHEMA.md). Optional fields are omitted when nil.
struct HealthBatch: Codable, Equatable {
    static let schemaVersion = 1

    var schema: Int = HealthBatch.schemaVersion
    var generatedAt: String
    /// IANA name of the device time zone; omitted when unknown.
    var timezone: String?
    var source: BatchSource
    /// Keys are device-local calendar days `YYYY-MM-DD`.
    var days: [String: DayRecord]
    var workouts: [WorkoutRecord]

    enum CodingKeys: String, CodingKey {
        case schema
        case generatedAt = "generated_at"
        case timezone
        case source
        case days
        case workouts
    }

    var isEmpty: Bool {
        days.isEmpty && workouts.isEmpty
    }

    /// Deterministic, compact JSON (sorted keys, no escaped slashes).
    func encodedJSON() throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return try encoder.encode(self)
    }
}

struct BatchSource: Codable, Equatable {
    var appVersion: String
    var osVersion: String
    /// Hardware identifier such as `iPhone16,1`; omitted if unavailable.
    var deviceModel: String?

    enum CodingKeys: String, CodingKey {
        case appVersion = "app_version"
        case osVersion = "os_version"
        case deviceModel = "device_model"
    }

    static var current: BatchSource {
        BatchSource(appVersion: AppInfo.version, osVersion: AppInfo.osVersion, deviceModel: AppInfo.deviceModel)
    }
}

/// One local calendar day. Metric groups are flat `key → number` objects whose keys come
/// from `MetricCatalog`.
struct DayRecord: Codable, Equatable {
    var activity: [String: Double]?
    var heart: [String: Double]?
    var sleep: SleepRecord?
    var body: [String: Double]?
    var vitals: [String: Double]?
    var nutrition: [String: Double]?
    var mindfulness: [String: Double]?

    init() {}

    var isEmpty: Bool {
        MetricGroup.allCases.allSatisfy { (self[$0]?.isEmpty ?? true) } && sleep == nil
    }

    subscript(group: MetricGroup) -> [String: Double]? {
        get {
            switch group {
            case .activity: return activity
            case .heart: return heart
            case .body: return body
            case .vitals: return vitals
            case .nutrition: return nutrition
            case .mindfulness: return mindfulness
            }
        }
        set {
            switch group {
            case .activity: activity = newValue
            case .heart: heart = newValue
            case .body: body = newValue
            case .vitals: vitals = newValue
            case .nutrition: nutrition = newValue
            case .mindfulness: mindfulness = newValue
            }
        }
    }

    mutating func set(_ value: Double, key: String, in group: MetricGroup) {
        var values = self[group] ?? [:]
        values[key] = value
        self[group] = values
    }
}

struct SleepRecord: Codable, Equatable {
    var inBedMin: Double?
    var asleepMin: Double?
    var coreMin: Double?
    var deepMin: Double?
    var remMin: Double?
    var awakeMin: Double?
    var unspecifiedMin: Double?
    var bedtime: String?
    var wakeTime: String?
    var sessions: [SleepSessionRecord]?

    enum CodingKeys: String, CodingKey {
        case inBedMin = "in_bed_min"
        case asleepMin = "asleep_min"
        case coreMin = "core_min"
        case deepMin = "deep_min"
        case remMin = "rem_min"
        case awakeMin = "awake_min"
        case unspecifiedMin = "unspecified_min"
        case bedtime
        case wakeTime = "wake_time"
        case sessions
    }
}

struct SleepSessionRecord: Codable, Equatable {
    /// `in_bed`, `asleep_unspecified`, `core`, `deep`, `rem` or `awake`
    var stage: String
    var start: String
    var end: String
}

struct WorkoutRecord: Codable, Equatable {
    var id: String
    var type: String
    var start: String
    var end: String
    var durationS: Double
    var energyKcal: Double?
    var distanceM: Double?
    var elevationGainM: Double?
    var indoor: Bool?
    var source: String?
    var heartRate: WorkoutHeartRateRecord?

    enum CodingKeys: String, CodingKey {
        case id
        case type
        case start
        case end
        case durationS = "duration_s"
        case energyKcal = "energy_kcal"
        case distanceM = "distance_m"
        case elevationGainM = "elevation_gain_m"
        case indoor
        case source
        case heartRate = "heart_rate"
    }
}

struct WorkoutHeartRateRecord: Codable, Equatable {
    var avgBpm: Double?
    var minBpm: Double?
    var maxBpm: Double?
    var recovery1minBpm: Double?
    var maxReferenceBpm: Double?
    var zones: [HeartRateZoneRecord]?
    var samples: [HeartRateSampleRecord]?

    enum CodingKeys: String, CodingKey {
        case avgBpm = "avg_bpm"
        case minBpm = "min_bpm"
        case maxBpm = "max_bpm"
        case recovery1minBpm = "recovery_1min_bpm"
        case maxReferenceBpm = "max_reference_bpm"
        case zones
        case samples
    }

    var isEmpty: Bool {
        avgBpm == nil && minBpm == nil && maxBpm == nil && recovery1minBpm == nil
            && maxReferenceBpm == nil && (zones?.isEmpty ?? true) && (samples?.isEmpty ?? true)
    }
}

struct HeartRateZoneRecord: Codable, Equatable {
    var zone: Int
    var lowerBpm: Double
    var upperBpm: Double
    var seconds: Double

    enum CodingKeys: String, CodingKey {
        case zone
        case lowerBpm = "lower_bpm"
        case upperBpm = "upper_bpm"
        case seconds
    }
}

struct HeartRateSampleRecord: Codable, Equatable {
    /// Seconds since workout start.
    var t: Int
    var bpm: Double
}
