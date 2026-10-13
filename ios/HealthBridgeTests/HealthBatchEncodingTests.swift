import HealthKit
import XCTest
@testable import HealthBridge

/// The JSON produced by the HealthBatch model must match docs/DATA_SCHEMA.md exactly.
final class HealthBatchEncodingTests: XCTestCase {
    // Keys per group as listed in docs/DATA_SCHEMA.md.
    private let specKeys: [MetricGroup: Set<String>] = [
        .activity: [
            "steps", "distance_walking_running_m", "distance_cycling_m", "distance_swimming_m",
            "active_energy_kcal", "basal_energy_kcal", "flights_climbed", "exercise_min", "stand_min",
            "stand_hours", "move_goal_kcal", "exercise_goal_min", "stand_goal_hours",
        ],
        .heart: [
            "resting_bpm", "walking_avg_bpm", "avg_bpm", "min_bpm", "max_bpm", "hrv_sdnn_ms",
            "vo2max_ml_kg_min", "recovery_1min_bpm",
        ],
        .body: ["weight_kg", "body_fat_pct", "bmi", "lean_body_mass_kg", "height_cm", "waist_cm"],
        .vitals: [
            "respiratory_rate_bpm", "oxygen_saturation_pct", "body_temperature_c",
            "sleeping_wrist_temperature_c", "blood_pressure_systolic_mmhg",
            "blood_pressure_diastolic_mmhg", "blood_glucose_mg_dl",
        ],
        .nutrition: [
            "energy_kcal", "protein_g", "carbohydrates_g", "fat_g", "fiber_g", "sugar_g", "water_ml", "caffeine_mg",
        ],
        .mindfulness: ["mindful_min"],
    ]

    private let specSleepKeys: Set<String> = [
        "in_bed_min", "asleep_min", "core_min", "deep_min", "rem_min", "awake_min", "unspecified_min",
        "bedtime", "wake_time", "sessions",
    ]

    private func jsonObject(_ batch: HealthBatch) throws -> [String: Any] {
        let data = try batch.encodedJSON()
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    /// Filled from the production catalog, so `testDayKeys` checks catalog + encoding
    /// against the keys written down from docs/DATA_SCHEMA.md.
    private func fullBatch() -> HealthBatch {
        var day = DayRecord()
        for metric in MetricCatalog.all {
            guard let group = metric.group else { continue }
            day.set(1, key: metric.key, in: group)
        }
        day.sleep = SleepRecord(
            inBedMin: 452, asleepMin: 431, coreMin: 271, deepMin: 62, remMin: 98, awakeMin: 18, unspecifiedMin: 0,
            bedtime: "2026-10-11T21:58:00Z", wakeTime: "2026-10-12T05:30:00Z",
            sessions: [SleepSessionRecord(stage: "core", start: "2026-10-11T22:05:00Z", end: "2026-10-11T23:10:00Z")]
        )
        var workout = WorkoutRecord(
            id: "6f1c0b8e-3d1a-4c55-9a3e-7a6c8d1e2f30",
            type: "running",
            start: "2026-10-12T16:02:11Z",
            end: "2026-10-12T16:48:40Z",
            durationS: 2789
        )
        workout.energyKcal = 498.2
        workout.distanceM = 8040.5
        workout.elevationGainM = 63
        workout.indoor = false
        workout.source = "Apple Watch"
        workout.heartRate = WorkoutHeartRateRecord(
            avgBpm: 152.3, minBpm: 96, maxBpm: 178, recovery1minBpm: 31, maxReferenceBpm: 190,
            zones: [HeartRateZoneRecord(zone: 1, lowerBpm: 95, upperBpm: 114, seconds: 120)],
            samples: [HeartRateSampleRecord(t: 0, bpm: 98), HeartRateSampleRecord(t: 5, bpm: 104)]
        )
        return HealthBatch(
            generatedAt: "2026-10-13T08:00:00Z",
            timezone: "Europe/Berlin",
            source: BatchSource(appVersion: "1.0.0", osVersion: "18.1", deviceModel: "iPhone16,1"),
            days: ["2026-10-12": day],
            workouts: [workout]
        )
    }

    func testTopLevelKeys() throws {
        let object = try jsonObject(fullBatch())
        XCTAssertEqual(Set(object.keys), ["schema", "generated_at", "timezone", "source", "days", "workouts"])
        XCTAssertEqual(object["schema"] as? Int, 1)
        XCTAssertEqual(object["generated_at"] as? String, "2026-10-13T08:00:00Z")
        let source = try XCTUnwrap(object["source"] as? [String: Any])
        XCTAssertEqual(Set(source.keys), ["app_version", "os_version", "device_model"])
    }

    func testDayKeys() throws {
        let object = try jsonObject(fullBatch())
        let days = try XCTUnwrap(object["days"] as? [String: Any])
        let day = try XCTUnwrap(days["2026-10-12"] as? [String: Any])
        XCTAssertEqual(Set(day.keys), ["activity", "heart", "sleep", "body", "vitals", "nutrition", "mindfulness"])
        for (group, keys) in specKeys {
            let values = try XCTUnwrap(day[group.rawValue] as? [String: Any], group.rawValue)
            XCTAssertEqual(Set(values.keys), keys, group.rawValue)
        }
        let sleep = try XCTUnwrap(day["sleep"] as? [String: Any])
        XCTAssertEqual(Set(sleep.keys), specSleepKeys)
        let session = try XCTUnwrap((sleep["sessions"] as? [[String: Any]])?.first)
        XCTAssertEqual(Set(session.keys), ["stage", "start", "end"])
    }

    func testWorkoutKeys() throws {
        let object = try jsonObject(fullBatch())
        let workout = try XCTUnwrap((object["workouts"] as? [[String: Any]])?.first)
        XCTAssertEqual(Set(workout.keys), [
            "id", "type", "start", "end", "duration_s", "energy_kcal", "distance_m",
            "elevation_gain_m", "indoor", "source", "heart_rate",
        ])
        let heartRate = try XCTUnwrap(workout["heart_rate"] as? [String: Any])
        XCTAssertEqual(Set(heartRate.keys), [
            "avg_bpm", "min_bpm", "max_bpm", "recovery_1min_bpm", "max_reference_bpm", "zones", "samples",
        ])
        let zone = try XCTUnwrap((heartRate["zones"] as? [[String: Any]])?.first)
        XCTAssertEqual(Set(zone.keys), ["zone", "lower_bpm", "upper_bpm", "seconds"])
        let sample = try XCTUnwrap((heartRate["samples"] as? [[String: Any]])?.first)
        XCTAssertEqual(Set(sample.keys), ["t", "bpm"])
        XCTAssertEqual(workout["indoor"] as? Bool, false)
    }

    func testNilFieldsAreOmitted() throws {
        let batch = HealthBatch(
            generatedAt: "2026-10-13T08:00:00Z",
            timezone: "Europe/Berlin",
            source: BatchSource(appVersion: "1.0.0", osVersion: "17.4", deviceModel: nil),
            days: [:],
            workouts: [WorkoutRecord(
                id: "6f1c0b8e-3d1a-4c55-9a3e-7a6c8d1e2f30",
                type: "yoga",
                start: "2026-10-12T16:00:00Z",
                end: "2026-10-12T16:30:00Z",
                durationS: 1800
            )]
        )
        let object = try jsonObject(batch)
        let source = try XCTUnwrap(object["source"] as? [String: Any])
        XCTAssertEqual(Set(source.keys), ["app_version", "os_version"])
        let workout = try XCTUnwrap((object["workouts"] as? [[String: Any]])?.first)
        XCTAssertEqual(Set(workout.keys), ["id", "type", "start", "end", "duration_s"])
        XCTAssertEqual(object["days"] as? [String: Double], [:])
        XCTAssertFalse(String(decoding: try batch.encodedJSON(), as: UTF8.self).contains("null"))
    }

    func testEmptyDayIsEmpty() {
        var day = DayRecord()
        XCTAssertTrue(day.isEmpty)
        day.set(10, key: "mindful_min", in: .mindfulness)
        XCTAssertFalse(day.isEmpty)
    }

    func testWholeNumbersEncodeWithoutFraction() throws {
        var day = DayRecord()
        day.set(9214, key: "steps", in: .activity)
        let batch = HealthBatch(
            generatedAt: "2026-10-13T08:00:00Z",
            timezone: "Europe/Berlin",
            source: BatchSource(appVersion: "1.0.0", osVersion: "17.4", deviceModel: nil),
            days: ["2026-10-12": day],
            workouts: []
        )
        let json = String(decoding: try batch.encodedJSON(), as: UTF8.self)
        XCTAssertTrue(json.contains("\"steps\":9214"), json)
        XCTAssertTrue(json.contains("\"workouts\":[]"), json)
    }

    func testRoundTrip() throws {
        let batch = fullBatch()
        let decoded = try JSONDecoder().decode(HealthBatch.self, from: batch.encodedJSON())
        XCTAssertEqual(decoded, batch)
    }

    // MARK: - Catalog

    func testCatalogMatchesSchema() {
        for group in MetricGroup.allCases {
            let catalogKeys = Set(MetricCatalog.all.filter { $0.group == group }.map(\.key))
            XCTAssertEqual(catalogKeys, specKeys[group], group.rawValue)
        }
        let sleepKeys = Set(MetricCatalog.metrics(in: .sleep).map(\.key))
        XCTAssertEqual(sleepKeys, specSleepKeys.subtracting(["bedtime", "wake_time", "sessions"]))
        let ids = MetricCatalog.all.map(\.id)
        XCTAssertEqual(ids.count, Set(ids).count, "duplicate catalog entries")
    }

    func testNormalizationDropsInvalidValues() throws {
        let steps = try XCTUnwrap(MetricCatalog.definition(group: .activity, key: "steps"))
        XCTAssertEqual(steps.normalized(9214.4), 9214)
        XCTAssertNil(steps.normalized(.nan))
        XCTAssertNil(steps.normalized(.infinity))
        XCTAssertNil(steps.normalized(-1))
        XCTAssertNil(steps.normalized(250_000))

        let oxygen = try XCTUnwrap(MetricCatalog.definition(group: .vitals, key: "oxygen_saturation_pct"))
        XCTAssertEqual(oxygen.normalized(0.973), 97.3, "HealthKit fractions are scaled to percent")
        XCTAssertNil(oxygen.normalized(0.3))

        let weight = try XCTUnwrap(MetricCatalog.definition(group: .body, key: "weight_kg"))
        XCTAssertEqual(weight.normalized(78.4049), 78.4)
    }

    // MARK: - String rules

    func testDeviceModelPattern() {
        XCTAssertTrue(AppInfo.isValidDeviceModel("iPhone16,1"))
        XCTAssertTrue(AppInfo.isValidDeviceModel("iPad14,3"))
        XCTAssertFalse(AppInfo.isValidDeviceModel("x86_64"))
        XCTAssertFalse(AppInfo.isValidDeviceModel("iPhone 15 Pro"))
        XCTAssertFalse(AppInfo.isValidDeviceModel(""))
        if let model = AppInfo.deviceModel {
            XCTAssertTrue(AppInfo.isValidDeviceModel(model))
            XCTAssertNotEqual(model, "arm64")
        }
    }

    func testWorkoutSourceRule() {
        XCTAssertEqual(HealthBatchBuilder.sourceName("Apple Watch"), "Apple Watch")
        XCTAssertEqual(HealthBatchBuilder.sourceName("Strava"), "Strava")
        XCTAssertEqual(HealthBatchBuilder.sourceName("Maurice's Apple Watch"), "Maurice's Apple Watch")
        XCTAssertNil(HealthBatchBuilder.sourceName("Maurice’s Apple Watch"), "curly apostrophe is not allowed")
        XCTAssertNil(HealthBatchBuilder.sourceName("-Strava"))
        XCTAssertNil(HealthBatchBuilder.sourceName(""))
        let long = String(repeating: "a", count: 80)
        XCTAssertEqual(HealthBatchBuilder.sourceName(long), String(repeating: "a", count: 64))
    }

    func testVersionSanitizing() {
        XCTAssertEqual(AppInfo.sanitizedVersion("1.0.0"), "1.0.0")
        XCTAssertEqual(AppInfo.sanitizedVersion("1.0 (beta)"), "1.0beta")
        XCTAssertNil(AppInfo.sanitizedVersion("()"))
    }

    func testWorkoutTypeNames() {
        XCTAssertEqual(HKWorkoutActivityType.running.schemaName, "running")
        XCTAssertEqual(HKWorkoutActivityType.traditionalStrengthTraining.schemaName, "traditional_strength_training")
        XCTAssertEqual(HKWorkoutActivityType.highIntensityIntervalTraining.schemaName, "high_intensity_interval_training")
        let pattern = #"^[a-z][a-z0-9_]{1,47}$"#
        for rawValue in UInt(1)...UInt(90) {
            guard let type = HKWorkoutActivityType(rawValue: rawValue) else { continue }
            XCTAssertNotNil(type.schemaName.range(of: pattern, options: .regularExpression), type.schemaName)
        }
        XCTAssertEqual(HKWorkoutActivityType.other.schemaName, "other")
    }
}
