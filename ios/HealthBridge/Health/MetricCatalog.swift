import Foundation
import HealthKit

/// Categories the user can enable or disable.
enum MetricCategory: String, Codable, CaseIterable, Identifiable, Hashable, Sendable {
    case activity
    case heart
    case sleep
    case body
    case vitals
    case nutrition
    case mindfulness
    case workouts

    var id: String { rawValue }

    var title: String {
        switch self {
        case .activity: return "Activity"
        case .heart: return "Heart"
        case .sleep: return "Sleep"
        case .body: return "Body"
        case .vitals: return "Vitals"
        case .nutrition: return "Nutrition"
        case .mindfulness: return "Mindfulness"
        case .workouts: return "Workouts"
        }
    }

    var symbol: String {
        switch self {
        case .activity: return "flame.fill"
        case .heart: return "heart.fill"
        case .sleep: return "bed.double.fill"
        case .body: return "figure.arms.open"
        case .vitals: return "waveform.path.ecg"
        case .nutrition: return "fork.knife"
        case .mindfulness: return "brain.head.profile"
        case .workouts: return "figure.run"
        }
    }

    var summary: String {
        switch self {
        case .activity: return "Steps, distance, energy, exercise and stand time, ring goals"
        case .heart: return "Resting, walking and average heart rate, HRV, VO₂ max, recovery"
        case .sleep: return "Time asleep, sleep stages, bedtime and wake time"
        case .body: return "Weight, body fat, BMI, lean mass, height, waist"
        case .vitals: return "Respiratory rate, blood oxygen, temperature, blood pressure, glucose"
        case .nutrition: return "Energy, macronutrients, water, caffeine"
        case .mindfulness: return "Mindful minutes"
        case .workouts: return "Type, duration, energy, distance, heart rate"
        }
    }
}

/// JSON groups inside a HealthBatch day (sleep has its own structure).
enum MetricGroup: String, Codable, CaseIterable, Sendable {
    case activity
    case heart
    case body
    case vitals
    case nutrition
    case mindfulness
}

enum MetricAggregation {
    case sum
    case average
    case minimum
    case maximum
    case latest
    case summary

    var title: String {
        switch self {
        case .sum: return "daily total"
        case .average: return "daily average"
        case .minimum: return "daily minimum"
        case .maximum: return "daily maximum"
        case .latest: return "latest of day"
        case .summary: return "activity rings"
        }
    }

    /// The `HKStatisticsOptions` needed for this aggregation (empty for non-statistics sources).
    var statisticsOption: HKStatisticsOptions {
        switch self {
        case .sum: return .cumulativeSum
        case .average: return .discreteAverage
        case .minimum: return .discreteMin
        case .maximum: return .discreteMax
        case .latest: return .mostRecent
        case .summary: return []
        }
    }
}

enum ActivitySummaryField {
    case standHours
    case moveGoal
    case exerciseGoal
    case standGoal
}

enum MetricSource {
    case quantity(HKQuantityTypeIdentifier)
    case activitySummary(ActivitySummaryField)
    case sleepAnalysis
    case mindfulSession
}

enum MetricUnit {
    case count
    case meter
    case kilocalorie
    case minute
    case hour
    case beatsPerMinute
    case breathsPerMinute
    case millisecond
    case vo2Max
    case kilogram
    case percent
    case bmi
    case centimeter
    case degreeCelsius
    case millimeterOfMercury
    case milligramPerDeciliter
    case gram
    case milliliter
    case milligram

    /// The HealthKit unit used to read the raw value.
    var hkUnit: HKUnit {
        switch self {
        case .count, .bmi, .hour:
            return .count()
        case .meter:
            return .meter()
        case .kilocalorie:
            return .kilocalorie()
        case .minute:
            return .minute()
        case .beatsPerMinute, .breathsPerMinute:
            return HKUnit.count().unitDivided(by: .minute())
        case .millisecond:
            return .secondUnit(with: .milli)
        case .vo2Max:
            // ml/(kg·min), identical to HKUnit(from: "ml/kg*min")
            return HKUnit.literUnit(with: .milli)
                .unitDivided(by: HKUnit.gramUnit(with: .kilo).unitMultiplied(by: .minute()))
        case .kilogram:
            return .gramUnit(with: .kilo)
        case .percent:
            return .percent()
        case .centimeter:
            return .meterUnit(with: .centi)
        case .degreeCelsius:
            return .degreeCelsius()
        case .millimeterOfMercury:
            return .millimeterOfMercury()
        case .milligramPerDeciliter:
            return HKUnit.gramUnit(with: .milli).unitDivided(by: .literUnit(with: .deci))
        case .gram:
            return .gram()
        case .milliliter:
            return .literUnit(with: .milli)
        case .milligram:
            return .gramUnit(with: .milli)
        }
    }

    /// HealthKit percentages are fractions (0–1); the schema uses 0–100.
    var scale: Double {
        self == .percent ? 100 : 1
    }

    var symbol: String {
        switch self {
        case .count: return ""
        case .meter: return "m"
        case .kilocalorie: return "kcal"
        case .minute: return "min"
        case .hour: return "h"
        case .beatsPerMinute: return "bpm"
        case .breathsPerMinute: return "breaths/min"
        case .millisecond: return "ms"
        case .vo2Max: return "ml/kg·min"
        case .kilogram: return "kg"
        case .percent: return "%"
        case .bmi: return ""
        case .centimeter: return "cm"
        case .degreeCelsius: return "°C"
        case .millimeterOfMercury: return "mmHg"
        case .milligramPerDeciliter: return "mg/dL"
        case .gram: return "g"
        case .milliliter: return "ml"
        case .milligram: return "mg"
        }
    }
}

/// One metric of docs/DATA_SCHEMA.md.
struct MetricDefinition: Identifiable {
    /// JSON key inside its group, e.g. `steps`.
    let key: String
    let category: MetricCategory
    /// JSON group; nil for sleep keys, which live in their own structure.
    let group: MetricGroup?
    let source: MetricSource
    let unit: MetricUnit
    let aggregation: MetricAggregation
    let range: ClosedRange<Double>
    let decimals: Int
    let displayName: String
    let symbol: String

    var id: String { "\(category.rawValue).\(key)" }

    /// Converts a raw HealthKit value to the schema value: scale, round, drop non-finite and
    /// out-of-range values.
    func normalized(_ raw: Double) -> Double? {
        MetricValue.normalize(raw * unit.scale, decimals: decimals, range: range)
    }
}

enum MetricValue {
    static func normalize(_ value: Double, decimals: Int, range: ClosedRange<Double>) -> Double? {
        guard value.isFinite else { return nil }
        let factor = pow(10.0, Double(decimals))
        let rounded = (value * factor).rounded() / factor
        guard rounded.isFinite, range.contains(rounded) else { return nil }
        return rounded
    }
}

enum MetricCatalog {
    // swiftlint:disable function_body_length
    static let all: [MetricDefinition] = [
        // MARK: activity
        quantity("steps", .activity, .stepCount, .count, .sum, 0...200_000, 0, "Steps", "figure.walk"),
        quantity("distance_walking_running_m", .activity, .distanceWalkingRunning, .meter, .sum, 0...300_000, 1,
                 "Walking + Running Distance", "figure.run"),
        quantity("distance_cycling_m", .activity, .distanceCycling, .meter, .sum, 0...1_000_000, 1,
                 "Cycling Distance", "bicycle"),
        quantity("distance_swimming_m", .activity, .distanceSwimming, .meter, .sum, 0...100_000, 1,
                 "Swimming Distance", "figure.pool.swim"),
        quantity("active_energy_kcal", .activity, .activeEnergyBurned, .kilocalorie, .sum, 0...20_000, 1,
                 "Active Energy", "flame"),
        quantity("basal_energy_kcal", .activity, .basalEnergyBurned, .kilocalorie, .sum, 0...10_000, 1,
                 "Resting Energy", "flame.circle"),
        quantity("flights_climbed", .activity, .flightsClimbed, .count, .sum, 0...2_000, 0,
                 "Flights Climbed", "figure.stairs"),
        quantity("exercise_min", .activity, .appleExerciseTime, .minute, .sum, 0...1_440, 0,
                 "Exercise Minutes", "timer"),
        quantity("stand_min", .activity, .appleStandTime, .minute, .sum, 0...1_440, 0,
                 "Stand Minutes", "figure.stand"),
        summary("stand_hours", .standHours, .hour, 0...24, "Stand Hours", "clock"),
        summary("move_goal_kcal", .moveGoal, .kilocalorie, 0...20_000, "Move Goal", "target"),
        summary("exercise_goal_min", .exerciseGoal, .minute, 0...1_440, "Exercise Goal", "target"),
        summary("stand_goal_hours", .standGoal, .hour, 0...24, "Stand Goal", "target"),

        // MARK: heart
        quantity("resting_bpm", .heart, .restingHeartRate, .beatsPerMinute, .average, 20...250, 1,
                 "Resting Heart Rate", "heart"),
        quantity("walking_avg_bpm", .heart, .walkingHeartRateAverage, .beatsPerMinute, .average, 20...250, 1,
                 "Walking Heart Rate Average", "figure.walk.motion"),
        quantity("avg_bpm", .heart, .heartRate, .beatsPerMinute, .average, 20...260, 1,
                 "Heart Rate (average)", "heart.fill"),
        quantity("min_bpm", .heart, .heartRate, .beatsPerMinute, .minimum, 20...260, 0,
                 "Heart Rate (minimum)", "arrow.down.heart"),
        quantity("max_bpm", .heart, .heartRate, .beatsPerMinute, .maximum, 20...260, 0,
                 "Heart Rate (maximum)", "arrow.up.heart"),
        quantity("hrv_sdnn_ms", .heart, .heartRateVariabilitySDNN, .millisecond, .average, 0...500, 1,
                 "Heart Rate Variability (SDNN)", "waveform.path.ecg"),
        quantity("vo2max_ml_kg_min", .heart, .vo2Max, .vo2Max, .latest, 5...100, 1,
                 "Cardio Fitness (VO₂ max)", "lungs"),
        quantity("recovery_1min_bpm", .heart, .heartRateRecoveryOneMinute, .beatsPerMinute, .average, 0...150, 1,
                 "Heart Rate Recovery (1 min)", "heart.circle"),

        // MARK: sleep (aggregated by SleepAggregator)
        sleep("in_bed_min", "Time in Bed", "bed.double"),
        sleep("asleep_min", "Time Asleep", "moon.zzz.fill"),
        sleep("core_min", "Core Sleep", "moon"),
        sleep("deep_min", "Deep Sleep", "moon.fill"),
        sleep("rem_min", "REM Sleep", "sparkles"),
        sleep("awake_min", "Awake", "eye"),
        sleep("unspecified_min", "Asleep (unspecified)", "moon.stars"),

        // MARK: body (latest of day)
        quantity("weight_kg", .body, .bodyMass, .kilogram, .latest, 1...500, 2, "Weight", "scalemass"),
        quantity("body_fat_pct", .body, .bodyFatPercentage, .percent, .latest, 1...80, 1, "Body Fat", "percent"),
        quantity("bmi", .body, .bodyMassIndex, .bmi, .latest, 5...100, 1, "Body Mass Index", "figure"),
        quantity("lean_body_mass_kg", .body, .leanBodyMass, .kilogram, .latest, 1...300, 2,
                 "Lean Body Mass", "figure.strengthtraining.traditional"),
        quantity("height_cm", .body, .height, .centimeter, .latest, 30...260, 1, "Height", "ruler"),
        quantity("waist_cm", .body, .waistCircumference, .centimeter, .latest, 20...300, 1,
                 "Waist Circumference", "ruler"),

        // MARK: vitals (daily average)
        quantity("respiratory_rate_bpm", .vitals, .respiratoryRate, .breathsPerMinute, .average, 2...80, 1,
                 "Respiratory Rate", "lungs.fill"),
        quantity("oxygen_saturation_pct", .vitals, .oxygenSaturation, .percent, .average, 50...100, 1,
                 "Blood Oxygen", "drop"),
        quantity("body_temperature_c", .vitals, .bodyTemperature, .degreeCelsius, .average, 30...45, 2,
                 "Body Temperature", "thermometer.medium"),
        quantity("sleeping_wrist_temperature_c", .vitals, .appleSleepingWristTemperature, .degreeCelsius,
                 .average, 25...45, 2, "Sleeping Wrist Temperature", "thermometer.low"),
        quantity("blood_pressure_systolic_mmhg", .vitals, .bloodPressureSystolic, .millimeterOfMercury,
                 .average, 40...300, 0, "Blood Pressure (systolic)", "heart.text.square"),
        quantity("blood_pressure_diastolic_mmhg", .vitals, .bloodPressureDiastolic, .millimeterOfMercury,
                 .average, 20...200, 0, "Blood Pressure (diastolic)", "heart.text.square"),
        quantity("blood_glucose_mg_dl", .vitals, .bloodGlucose, .milligramPerDeciliter, .average, 10...1_000, 1,
                 "Blood Glucose", "drop.fill"),

        // MARK: nutrition (daily sum)
        quantity("energy_kcal", .nutrition, .dietaryEnergyConsumed, .kilocalorie, .sum, 0...20_000, 1,
                 "Dietary Energy", "fork.knife"),
        quantity("protein_g", .nutrition, .dietaryProtein, .gram, .sum, 0...1_000, 1, "Protein", "p.circle"),
        quantity("carbohydrates_g", .nutrition, .dietaryCarbohydrates, .gram, .sum, 0...2_000, 1,
                 "Carbohydrates", "c.circle"),
        quantity("fat_g", .nutrition, .dietaryFatTotal, .gram, .sum, 0...1_000, 1, "Total Fat", "f.circle"),
        quantity("fiber_g", .nutrition, .dietaryFiber, .gram, .sum, 0...500, 1, "Fiber", "leaf"),
        quantity("sugar_g", .nutrition, .dietarySugar, .gram, .sum, 0...1_000, 1, "Sugar", "cube"),
        quantity("water_ml", .nutrition, .dietaryWater, .milliliter, .sum, 0...20_000, 0, "Water", "drop.circle"),
        quantity("caffeine_mg", .nutrition, .dietaryCaffeine, .milligram, .sum, 0...5_000, 0,
                 "Caffeine", "cup.and.saucer"),

        // MARK: mindfulness
        MetricDefinition(
            key: "mindful_min", category: .mindfulness, group: .mindfulness, source: .mindfulSession,
            unit: .minute, aggregation: .sum, range: 0...1_440, decimals: 1,
            displayName: "Mindful Minutes", symbol: "brain.head.profile"
        ),
    ]
    // swiftlint:enable function_body_length

    static func metrics(in category: MetricCategory) -> [MetricDefinition] {
        all.filter { $0.category == category }
    }

    static func definition(group: MetricGroup, key: String) -> MetricDefinition? {
        all.first { $0.group == group && $0.key == key }
    }

    struct FieldInfo: Identifiable {
        let name: String
        let symbol: String
        var id: String { name }
    }

    /// Workout fields shown on the metrics screen (they are part of `workouts[]`).
    static let workoutFields: [FieldInfo] = [
        FieldInfo(name: "Type, start, end, duration", symbol: "clock"),
        FieldInfo(name: "Active energy", symbol: "flame"),
        FieldInfo(name: "Distance", symbol: "point.topleft.down.to.point.bottomright.curvepath"),
        FieldInfo(name: "Elevation gain", symbol: "mountain.2"),
        FieldInfo(name: "Indoor / outdoor, source", symbol: "house"),
        FieldInfo(name: "Average, minimum and maximum heart rate", symbol: "heart"),
    ]

    static let workoutHeartRateDetailFields: [FieldInfo] = [
        FieldInfo(name: "Heart-rate zones (5 zones)", symbol: "chart.bar.fill"),
        FieldInfo(name: "Heart-rate curve (one point per 5 s)", symbol: "waveform.path"),
        FieldInfo(name: "Heart-rate recovery after 1 minute", symbol: "arrow.down.heart"),
    ]

    // MARK: Builders

    private static func quantity(
        _ key: String,
        _ group: MetricGroup,
        _ identifier: HKQuantityTypeIdentifier,
        _ unit: MetricUnit,
        _ aggregation: MetricAggregation,
        _ range: ClosedRange<Double>,
        _ decimals: Int,
        _ name: String,
        _ symbol: String
    ) -> MetricDefinition {
        MetricDefinition(
            key: key,
            category: MetricCategory(rawValue: group.rawValue) ?? .activity,
            group: group,
            source: .quantity(identifier),
            unit: unit,
            aggregation: aggregation,
            range: range,
            decimals: decimals,
            displayName: name,
            symbol: symbol
        )
    }

    private static func summary(
        _ key: String,
        _ field: ActivitySummaryField,
        _ unit: MetricUnit,
        _ range: ClosedRange<Double>,
        _ name: String,
        _ symbol: String
    ) -> MetricDefinition {
        MetricDefinition(
            key: key, category: .activity, group: .activity, source: .activitySummary(field),
            unit: unit, aggregation: .summary, range: range, decimals: unit == .hour ? 0 : 1,
            displayName: name, symbol: symbol
        )
    }

    private static func sleep(_ key: String, _ name: String, _ symbol: String) -> MetricDefinition {
        MetricDefinition(
            key: key, category: .sleep, group: nil, source: .sleepAnalysis,
            unit: .minute, aggregation: .sum, range: 0...1_440, decimals: 0,
            displayName: name, symbol: symbol
        )
    }
}
