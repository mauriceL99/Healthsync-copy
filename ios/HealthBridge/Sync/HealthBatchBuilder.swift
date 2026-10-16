import Foundation
import HealthKit
import os

struct BatchOptions: Equatable, Sendable {
    var categories: Set<MetricCategory>
    /// Zones, curve and recovery for workouts (Pro).
    var heartRateDetails: Bool
    /// `max_reference_bpm`, computed once per sync run when details are enabled.
    var maxReferenceBPM: Double?
}

/// Reads HealthKit for a day range and produces a HealthBatch (docs/DATA_SCHEMA.md).
/// Use one builder per sync run: it counts the reads it had to skip.
final class HealthBatchBuilder {
    let health: HealthKitService
    /// HealthKit reads that failed with an ordinary error and were left out (each is logged).
    private(set) var skippedReads = 0

    init(health: HealthKitService) {
        self.health = health
    }

    func build(range: DayRange, options: BatchOptions, now: Date = Date()) async throws -> HealthBatch {
        var days: [String: DayRecord] = [:]
        let categories = options.categories

        try await addQuantityMetrics(to: &days, range: range, categories: categories)
        if categories.contains(.activity) {
            try await addActivitySummaries(to: &days, range: range)
        }
        if categories.contains(.sleep) {
            try await addSleep(to: &days, range: range)
        }
        if categories.contains(.mindfulness) {
            try await addMindfulness(to: &days, range: range)
        }
        var workouts: [WorkoutRecord] = []
        if categories.contains(.workouts) {
            workouts = try await buildWorkouts(range: range, options: options)
        }

        return HealthBatch(
            generatedAt: RFC3339.string(from: now),
            timezone: AppInfo.timeZoneIdentifier,
            source: .current,
            days: days.filter { !$0.value.isEmpty },
            workouts: workouts
        )
    }

    /// `max_reference_bpm`: `220 − age`, else the highest heart rate of the last 365 days,
    /// else the default from `HeartRateAnalyzer.maxReference`.
    func maxReferenceBPM(now: Date, calendar: Calendar) async throws -> Double {
        let age = health.age(on: now)
        var observedMax: Double?
        if age == nil, let yearAgo = calendar.date(byAdding: .day, value: -365, to: now) {
            observedMax = try await tolerant("maximum heart rate", nil) {
                try await health.maximumHeartRate(since: yearAgo)
            }
        }
        return HeartRateAnalyzer.maxReference(age: age, observedMax: observedMax)
    }

    /// Runs a HealthKit read and skips it on ordinary errors (logged and counted in
    /// `skippedReads`), but propagates a locked database (the whole sync must be retried
    /// later) and cancellation.
    private func tolerant<T>(_ read: String, _ fallback: T, _ operation: () async throws -> T) async throws -> T {
        do {
            return try await operation()
        } catch let error as HealthAccessError {
            throw error
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            if HealthKitService.isDatabaseLocked(error) {
                throw HealthAccessError.locked
            }
            skippedReads += 1
            Log.sync.error("Skipped HealthKit read (\(read, privacy: .public)): \(error.localizedDescription, privacy: .public)")
            return fallback
        }
    }

    // MARK: - Quantities

    private func addQuantityMetrics(
        to days: inout [String: DayRecord],
        range: DayRange,
        categories: Set<MetricCategory>
    ) async throws {
        // Group metrics by HealthKit type so e.g. heart-rate avg/min/max need one query.
        var byIdentifier: [HKQuantityTypeIdentifier: [MetricDefinition]] = [:]
        var order: [HKQuantityTypeIdentifier] = []
        for metric in MetricCatalog.all where categories.contains(metric.category) {
            guard case .quantity(let identifier) = metric.source else { continue }
            if byIdentifier[identifier] == nil {
                order.append(identifier)
            }
            byIdentifier[identifier, default: []].append(metric)
        }

        for identifier in order {
            try Task.checkCancellation()
            guard let definitions = byIdentifier[identifier], let first = definitions.first else { continue }
            var options: HKStatisticsOptions = []
            for definition in definitions {
                options.formUnion(definition.aggregation.statisticsOption)
            }
            let unit = first.unit.hkUnit
            let statistics = try await tolerant(identifier.rawValue, [:]) {
                try await health.dailyStatistics(identifier: identifier, unit: unit, options: options, range: range)
            }
            for (dayKey, daily) in statistics {
                for definition in definitions {
                    guard let group = definition.group,
                          let raw = daily.value(for: definition.aggregation),
                          let value = definition.normalized(raw) else { continue }
                    days[dayKey, default: DayRecord()].set(value, key: definition.key, in: group)
                }
            }
        }
    }

    // MARK: - Activity summaries

    private func addActivitySummaries(to days: inout [String: DayRecord], range: DayRange) async throws {
        let summaries = try await tolerant("activity summaries", [:]) {
            try await health.activitySummaries(range: range)
        }
        let validKeys = Set(range.dayKeys)
        let definitions = MetricCatalog.metrics(in: .activity)
        for (dayKey, values) in summaries where validKeys.contains(dayKey) {
            for definition in definitions {
                guard case .activitySummary(let field) = definition.source,
                      let group = definition.group,
                      let raw = values.value(for: field),
                      let value = definition.normalized(raw) else { continue }
                days[dayKey, default: DayRecord()].set(value, key: definition.key, in: group)
            }
        }
    }

    // MARK: - Sleep

    private func addSleep(to days: inout [String: DayRecord], range: DayRange) async throws {
        let calendar = range.calendar
        let queryStart = SleepAggregator.windowStart(forDay: range.start, calendar: calendar)
        let queryEnd = SleepAggregator.windowEnd(forDay: range.lastDay, calendar: calendar)
        let intervals = try await tolerant("sleep", []) {
            try await health.sleepIntervals(from: queryStart, to: queryEnd)
        }
        let records = SleepAggregator.aggregate(intervals, calendar: calendar, dayKeys: Set(range.dayKeys))
        for (dayKey, record) in records {
            days[dayKey, default: DayRecord()].sleep = record
        }
    }

    // MARK: - Mindfulness

    private func addMindfulness(to days: inout [String: DayRecord], range: DayRange) async throws {
        let samples = try await tolerant("mindful sessions", []) {
            try await health.categorySamples(.mindfulSession, from: range.start, to: range.end)
        }
        guard !samples.isEmpty, let definition = MetricCatalog.metrics(in: .mindfulness).first else { return }
        let calendar = range.calendar

        // Split every session at local midnight, then merge overlaps per day so that the
        // same session recorded by two apps is not counted twice.
        var perDay: [String: [DateInterval]] = [:]
        for sample in samples where sample.endDate > sample.startDate {
            var cursor = max(sample.startDate, range.start)
            let end = min(sample.endDate, range.end)
            while cursor < end {
                let nextMidnight = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: cursor)) ?? end
                let pieceEnd = min(nextMidnight, end)
                perDay[DayKey.string(from: cursor, calendar: calendar), default: []]
                    .append(DateInterval(start: cursor, end: pieceEnd))
                cursor = pieceEnd
            }
        }
        for (dayKey, intervals) in perDay {
            let seconds = Self.mergedDuration(intervals)
            guard seconds > 0, let value = definition.normalized(seconds / 60), let group = definition.group else {
                continue
            }
            days[dayKey, default: DayRecord()].set(value, key: definition.key, in: group)
        }
    }

    static func mergedDuration(_ intervals: [DateInterval]) -> TimeInterval {
        let sorted = intervals.sorted { $0.start < $1.start }
        var total: TimeInterval = 0
        var current: DateInterval?
        for interval in sorted {
            if let active = current, interval.start <= active.end {
                current = DateInterval(start: active.start, end: max(active.end, interval.end))
            } else {
                if let active = current {
                    total += active.duration
                }
                current = interval
            }
        }
        if let active = current {
            total += active.duration
        }
        return total
    }

    // MARK: - Workouts

    private func buildWorkouts(range: DayRange, options: BatchOptions) async throws -> [WorkoutRecord] {
        let workouts = try await tolerant("workouts", []) {
            try await health.workouts(from: range.start, to: range.end)
        }
        var records: [WorkoutRecord] = []
        for workout in workouts {
            try Task.checkCancellation()
            if let workoutRecord = try await makeRecord(for: workout, options: options) {
                records.append(workoutRecord)
            }
        }
        return records
    }

    private func makeRecord(for workout: HKWorkout, options: BatchOptions) async throws -> WorkoutRecord? {
        let duration = workout.endDate.timeIntervalSince(workout.startDate)
        guard duration >= 0, duration <= HeartRateAnalyzer.maxWorkoutSeconds else { return nil }

        var record = WorkoutRecord(
            id: workout.uuid.uuidString.lowercased(),
            type: workout.workoutActivityType.schemaName,
            start: RFC3339.string(from: workout.startDate),
            end: RFC3339.string(from: workout.endDate),
            durationS: MetricValue.normalize(workout.duration, decimals: 0, range: 0...HeartRateAnalyzer.maxWorkoutSeconds)
                ?? duration.rounded()
        )

        record.energyKcal = health.sum(.activeEnergyBurned, in: workout, unit: .kilocalorie())
            .flatMap { MetricValue.normalize($0, decimals: 1, range: 0...20_000) }
        record.distanceM = workoutDistance(workout)
            .flatMap { MetricValue.normalize($0, decimals: 1, range: 0...1_000_000) }
        if let elevation = workout.metadata?[HKMetadataKeyElevationAscended] as? HKQuantity,
           elevation.is(compatibleWith: .meter()) {
            record.elevationGainM = MetricValue.normalize(elevation.doubleValue(for: .meter()), decimals: 1, range: 0...20_000)
        }
        if let indoor = workout.metadata?[HKMetadataKeyIndoorWorkout] as? Bool {
            record.indoor = indoor
        }
        record.source = Self.sourceLabel(for: workout.sourceRevision)

        // Basic heart-rate statistics are always included; details only for Pro.
        let stats = health.heartRateStatistics(in: workout)
        var heartRate = WorkoutHeartRateRecord()
        heartRate.avgBpm = stats.average.flatMap { MetricValue.normalize($0, decimals: 1, range: HeartRateAnalyzer.validRange) }
        heartRate.minBpm = stats.minimum.flatMap { MetricValue.normalize($0, decimals: 0, range: HeartRateAnalyzer.validRange) }
        heartRate.maxBpm = stats.maximum.flatMap { MetricValue.normalize($0, decimals: 0, range: HeartRateAnalyzer.validRange) }

        if options.heartRateDetails {
            let points = try await tolerant("workout heart rate", []) {
                try await health.heartRatePoints(from: workout.startDate, to: workout.endDate.addingTimeInterval(HeartRateAnalyzer.samplesAfterEnd))
            }
            if !points.isEmpty {
                let maxReference = options.maxReferenceBPM ?? HeartRateAnalyzer.maxReference(age: nil, observedMax: nil)
                let details = HeartRateAnalyzer.analyze(
                    points: points,
                    start: workout.startDate,
                    end: workout.endDate,
                    maxReference: maxReference
                )
                heartRate.avgBpm = heartRate.avgBpm ?? details.avgBpm
                heartRate.minBpm = heartRate.minBpm ?? details.minBpm
                heartRate.maxBpm = heartRate.maxBpm ?? details.maxBpm
                heartRate.recovery1minBpm = details.recovery1minBpm
                heartRate.maxReferenceBpm = details.maxReferenceBpm
                heartRate.zones = details.zones
                heartRate.samples = details.samples
            }
        }
        record.heartRate = heartRate.isEmpty ? nil : heartRate
        return record
    }

    private func workoutDistance(_ workout: HKWorkout) -> Double? {
        let candidates: [HKQuantityTypeIdentifier] = [
            .distanceWalkingRunning, .distanceCycling, .distanceSwimming,
            .distanceWheelchair, .distanceDownhillSnowSports,
        ]
        for identifier in candidates {
            if let meters = health.sum(identifier, in: workout, unit: .meter()), meters > 0 {
                return meters
            }
        }
        return nil
    }

    /// A non-personal label for the workout's source. Apple's own sources carry the
    /// user's device name (for example "Maurice's Apple Watch"), so they are reduced to
    /// the product family; names of third-party apps (for example "Strava") are kept.
    static func sourceLabel(for revision: HKSourceRevision) -> String? {
        if revision.source.bundleIdentifier.hasPrefix("com.apple.") {
            let product = revision.productType ?? ""
            if product.hasPrefix("Watch") { return "Apple Watch" }
            if product.hasPrefix("iPhone") { return "iPhone" }
            if product.hasPrefix("iPad") { return "iPad" }
            return "Apple Health"
        }
        return sourceName(revision.source.name)
    }

    /// The HKSource name truncated to 64 characters; omitted unless it matches
    /// `^[A-Za-z0-9][A-Za-z0-9 .'()&+-]{0,63}$`.
    static func sourceName(_ name: String) -> String? {
        let truncated = String(name.prefix(64))
        guard truncated.range(of: #"^[A-Za-z0-9][A-Za-z0-9 .'()&+-]{0,63}$"#, options: .regularExpression) != nil else {
            return nil
        }
        return truncated
    }
}
