import Foundation
import HealthKit
import os

enum HealthAccessError: LocalizedError {
    case unavailable
    case locked

    var errorDescription: String? {
        switch self {
        case .unavailable:
            return "Apple Health is not available on this device."
        case .locked:
            return "Health data is protected while your iPhone is locked. The sync will run after you unlock it."
        }
    }
}

/// A local calendar day range `[start, end)`, `start` at local midnight.
struct DayRange: Equatable, Sendable {
    let start: Date
    let dayCount: Int
    let calendar: Calendar

    init(start: Date, dayCount: Int, calendar: Calendar) {
        self.start = calendar.startOfDay(for: start)
        self.dayCount = max(dayCount, 1)
        self.calendar = calendar
    }

    /// The `dayCount` days ending with (and including) the day of `today`.
    static func lastDays(_ count: Int, endingOn today: Date, calendar: Calendar) -> DayRange {
        let todayStart = calendar.startOfDay(for: today)
        let start = calendar.date(byAdding: .day, value: -(max(count, 1) - 1), to: todayStart) ?? todayStart
        return DayRange(start: start, dayCount: count, calendar: calendar)
    }

    /// Exclusive end (local midnight after the last day).
    var end: Date {
        calendar.date(byAdding: .day, value: dayCount, to: start) ?? start
    }

    var lastDay: Date {
        calendar.date(byAdding: .day, value: dayCount - 1, to: start) ?? start
    }

    var days: [Date] {
        (0..<dayCount).compactMap { calendar.date(byAdding: .day, value: $0, to: start) }
    }

    var dayKeys: [String] {
        days.map { DayKey.string(from: $0, calendar: calendar) }
    }

    /// Splits the range into consecutive chunks of at most `size` days (oldest first).
    func chunked(maxDays size: Int) -> [DayRange] {
        let size = max(size, 1)
        return stride(from: 0, to: dayCount, by: size).compactMap { offset -> DayRange? in
            guard let chunkStart = calendar.date(byAdding: .day, value: offset, to: start) else { return nil }
            return DayRange(start: chunkStart, dayCount: min(size, dayCount - offset), calendar: calendar)
        }
    }
}

/// Daily values from one `HKStatisticsCollectionQuery`.
struct DailyStatistic: Sendable {
    var sum: Double?
    var average: Double?
    var minimum: Double?
    var maximum: Double?
    var mostRecent: Double?

    func value(for aggregation: MetricAggregation) -> Double? {
        switch aggregation {
        case .sum: return sum
        case .average: return average
        case .minimum: return minimum
        case .maximum: return maximum
        case .latest: return mostRecent
        case .summary: return nil
        }
    }

    var hasValue: Bool {
        sum != nil || average != nil || minimum != nil || maximum != nil || mostRecent != nil
    }
}

/// Mutable accumulator shared with HealthKit callbacks (which run on HealthKit's queue,
/// serially per query).
private final class Accumulator<Value>: @unchecked Sendable {
    var value: Value
    var finished = false

    init(_ value: Value) {
        self.value = value
    }
}

/// All HealthKit access. Never logs values.
final class HealthKitService: @unchecked Sendable {
    static let shared = HealthKitService()

    let store = HKHealthStore()

    var isAvailable: Bool {
        HKHealthStore.isHealthDataAvailable()
    }

    // MARK: - Authorization

    static let workoutQuantityTypes: [HKQuantityTypeIdentifier] = [
        .heartRate, .activeEnergyBurned, .distanceWalkingRunning, .distanceCycling,
        .distanceSwimming, .distanceWheelchair, .distanceDownhillSnowSports,
    ]

    /// Exactly the types needed for the selected categories.
    func readTypes(for categories: Set<MetricCategory>, heartRateDetails: Bool) -> Set<HKObjectType> {
        var types = Set<HKObjectType>()
        for metric in MetricCatalog.all where categories.contains(metric.category) {
            switch metric.source {
            case .quantity(let identifier):
                if let type = HKObjectType.quantityType(forIdentifier: identifier) {
                    types.insert(type)
                }
            case .activitySummary:
                types.insert(HKObjectType.activitySummaryType())
            case .sleepAnalysis:
                if let type = HKObjectType.categoryType(forIdentifier: .sleepAnalysis) {
                    types.insert(type)
                }
            case .mindfulSession:
                if let type = HKObjectType.categoryType(forIdentifier: .mindfulSession) {
                    types.insert(type)
                }
            }
        }
        if categories.contains(.workouts) {
            types.insert(HKObjectType.workoutType())
            for identifier in Self.workoutQuantityTypes {
                if let type = HKObjectType.quantityType(forIdentifier: identifier) {
                    types.insert(type)
                }
            }
            if heartRateDetails, let dateOfBirth = HKObjectType.characteristicType(forIdentifier: .dateOfBirth) {
                types.insert(dateOfBirth)
            }
        }
        return types
    }

    func requestAuthorization(categories: Set<MetricCategory>, heartRateDetails: Bool) async throws {
        guard isAvailable else { throw HealthAccessError.unavailable }
        let types = readTypes(for: categories, heartRateDetails: heartRateDetails)
        guard !types.isEmpty else { return }
        try await store.requestAuthorization(toShare: [], read: types)
    }

    /// True if iOS would show the permission sheet (some selected types were never asked for).
    /// HealthKit never reveals whether read access was granted.
    func needsAuthorizationRequest(categories: Set<MetricCategory>, heartRateDetails: Bool) async -> Bool {
        guard isAvailable else { return false }
        let types = readTypes(for: categories, heartRateDetails: heartRateDetails)
        guard !types.isEmpty else { return false }
        do {
            let status = try await store.statusForAuthorizationRequest(toShare: [], read: types)
            return status == .shouldRequest
        } catch {
            return true
        }
    }

    // MARK: - Error helpers

    static func isDatabaseLocked(_ error: Error) -> Bool {
        if let hkError = error as? HKError, hkError.code == .errorDatabaseInaccessible {
            return true
        }
        let nsError = error as NSError
        return nsError.domain == HKError.errorDomain && nsError.code == HKError.Code.errorDatabaseInaccessible.rawValue
    }

    static func isNoData(_ error: Error) -> Bool {
        if let hkError = error as? HKError, hkError.code == .errorNoData {
            return true
        }
        let nsError = error as NSError
        return nsError.domain == HKError.errorDomain && nsError.code == HKError.Code.errorNoData.rawValue
    }

    private static func mapped(_ error: Error) -> Error {
        isDatabaseLocked(error) ? HealthAccessError.locked : error
    }

    /// Ends a query continuation after an error: "no data" is an empty result, a locked
    /// database becomes `HealthAccessError.locked`, anything else is passed on.
    private static func finish<T>(_ continuation: CheckedContinuation<T, Error>, after error: Error, empty: T) {
        if isNoData(error) {
            continuation.resume(returning: empty)
        } else {
            continuation.resume(throwing: mapped(error))
        }
    }

    // MARK: - Daily statistics

    func dailyStatistics(
        identifier: HKQuantityTypeIdentifier,
        unit: HKUnit,
        options: HKStatisticsOptions,
        range: DayRange
    ) async throws -> [String: DailyStatistic] {
        guard let type = HKObjectType.quantityType(forIdentifier: identifier) else { return [:] }
        // Cumulative options on a discrete type (or vice versa) raise an exception. The
        // catalog must never produce that (pinned by HealthLogicTests); in release builds
        // the metric is skipped instead of crashing.
        let isCumulative = type.aggregationStyle == .cumulative
        if isCumulative != options.contains(.cumulativeSum) {
            assertionFailure("Statistics options do not match the aggregation style of \(identifier.rawValue)")
            Log.sync.error("Skipped \(identifier.rawValue, privacy: .public): options do not match its aggregation style")
            return [:]
        }
        let predicate = HKQuery.predicateForSamples(withStart: range.start, end: range.end, options: .strictStartDate)
        let calendar = range.calendar
        let rangeStart = range.start
        let rangeEnd = range.end

        return try await withCheckedThrowingContinuation { continuation in
            let query = HKStatisticsCollectionQuery(
                quantityType: type,
                quantitySamplePredicate: predicate,
                options: options,
                anchorDate: rangeStart,
                intervalComponents: DateComponents(day: 1)
            )
            query.initialResultsHandler = { _, collection, error in
                if let error {
                    Self.finish(continuation, after: error, empty: [:])
                    return
                }
                let result = Accumulator<[String: DailyStatistic]>([:])
                collection?.enumerateStatistics(from: rangeStart, to: rangeEnd) { statistics, _ in
                    guard statistics.startDate < rangeEnd else { return }
                    var daily = DailyStatistic()
                    if options.contains(.cumulativeSum) {
                        daily.sum = statistics.sumQuantity()?.doubleValue(for: unit)
                    }
                    if options.contains(.discreteAverage) {
                        daily.average = statistics.averageQuantity()?.doubleValue(for: unit)
                    }
                    if options.contains(.discreteMin) {
                        daily.minimum = statistics.minimumQuantity()?.doubleValue(for: unit)
                    }
                    if options.contains(.discreteMax) {
                        daily.maximum = statistics.maximumQuantity()?.doubleValue(for: unit)
                    }
                    if options.contains(.mostRecent) {
                        daily.mostRecent = statistics.mostRecentQuantity()?.doubleValue(for: unit)
                    }
                    if daily.hasValue {
                        result.value[DayKey.string(from: statistics.startDate, calendar: calendar)] = daily
                    }
                }
                continuation.resume(returning: result.value)
            }
            store.execute(query)
        }
    }

    // MARK: - Activity summaries

    struct ActivitySummaryValues: Sendable {
        var standHours: Double?
        var moveGoalKcal: Double?
        var exerciseGoalMin: Double?
        var standGoalHours: Double?

        func value(for field: ActivitySummaryField) -> Double? {
            switch field {
            case .standHours: return standHours
            case .moveGoal: return moveGoalKcal
            case .exerciseGoal: return exerciseGoalMin
            case .standGoal: return standGoalHours
            }
        }
    }

    func activitySummaries(range: DayRange) async throws -> [String: ActivitySummaryValues] {
        let calendar = range.calendar
        var startComponents = calendar.dateComponents([.era, .year, .month, .day], from: range.start)
        startComponents.calendar = calendar
        var endComponents = calendar.dateComponents([.era, .year, .month, .day], from: range.lastDay)
        endComponents.calendar = calendar
        let predicate = HKQuery.predicate(forActivitySummariesBetweenStart: startComponents, end: endComponents)

        return try await withCheckedThrowingContinuation { continuation in
            let query = HKActivitySummaryQuery(predicate: predicate) { _, summaries, error in
                if let error {
                    Self.finish(continuation, after: error, empty: [:])
                    return
                }
                var result: [String: ActivitySummaryValues] = [:]
                for summary in summaries ?? [] {
                    guard let key = DayKey.string(from: summary.dateComponents(for: calendar)) else { continue }
                    let standGoal = summary.standHoursGoal?.doubleValue(for: .count())
                    let standHours = summary.appleStandHours.doubleValue(for: .count())
                    let moveGoal = summary.activeEnergyBurnedGoal.doubleValue(for: .kilocalorie())
                    let exerciseGoal = summary.exerciseTimeGoal?.doubleValue(for: .minute())
                    var values = ActivitySummaryValues()
                    // Without an Apple Watch summaries can exist with all zeros; skip those.
                    if standHours > 0 || (standGoal ?? 0) > 0 {
                        values.standHours = standHours
                    }
                    values.moveGoalKcal = moveGoal > 0 ? moveGoal : nil
                    values.exerciseGoalMin = (exerciseGoal ?? 0) > 0 ? exerciseGoal : nil
                    values.standGoalHours = (standGoal ?? 0) > 0 ? standGoal : nil
                    result[key] = values
                }
                continuation.resume(returning: result)
            }
            store.execute(query)
        }
    }

    // MARK: - Samples

    func categorySamples(_ identifier: HKCategoryTypeIdentifier, from start: Date, to end: Date) async throws -> [HKCategorySample] {
        guard let type = HKObjectType.categoryType(forIdentifier: identifier) else { return [] }
        let predicate = HKQuery.predicateForSamples(withStart: start, end: end, options: [])
        let results = try await fetchSamples(of: type, predicate: predicate)
        return results.compactMap { $0 as? HKCategorySample }
    }

    func workouts(from start: Date, to end: Date) async throws -> [HKWorkout] {
        let predicate = HKQuery.predicateForSamples(withStart: start, end: end, options: .strictStartDate)
        let results = try await fetchSamples(of: HKObjectType.workoutType(), predicate: predicate)
        return results.compactMap { $0 as? HKWorkout }
    }

    private func fetchSamples(of type: HKSampleType, predicate: NSPredicate) async throws -> [HKSample] {
        try await withCheckedThrowingContinuation { continuation in
            let sort = NSSortDescriptor(key: HKSampleSortIdentifierStartDate, ascending: true)
            let query = HKSampleQuery(
                sampleType: type,
                predicate: predicate,
                limit: HKObjectQueryNoLimit,
                sortDescriptors: [sort]
            ) { _, samples, error in
                if let error {
                    Self.finish(continuation, after: error, empty: [])
                    return
                }
                continuation.resume(returning: samples ?? [])
            }
            store.execute(query)
        }
    }

    /// Sleep intervals overlapping `[start, end)`.
    func sleepIntervals(from start: Date, to end: Date) async throws -> [SleepInterval] {
        let results = try await categorySamples(.sleepAnalysis, from: start, to: end)
        return results.compactMap { sample -> SleepInterval? in
            guard let stage = Self.sleepStage(for: sample.value) else { return nil }
            return SleepInterval(stage: stage, start: sample.startDate, end: sample.endDate)
        }
    }

    static func sleepStage(for rawValue: Int) -> SleepStage? {
        guard let value = HKCategoryValueSleepAnalysis(rawValue: rawValue) else { return nil }
        switch value {
        case .inBed: return .inBed
        case .asleepUnspecified: return .asleepUnspecified
        case .asleepCore: return .core
        case .asleepDeep: return .deep
        case .asleepREM: return .rem
        case .awake: return .awake
        @unknown default: return nil
        }
    }

    /// Every individual heart-rate reading in `[start, end]`, including readings stored
    /// inside quantity series (Apple Watch workout data).
    func heartRatePoints(from start: Date, to end: Date) async throws -> [HeartRatePoint] {
        guard let type = HKObjectType.quantityType(forIdentifier: .heartRate) else { return [] }
        let predicate = HKQuery.predicateForSamples(withStart: start, end: end, options: [])
        let unit = HKUnit.count().unitDivided(by: .minute())

        return try await withCheckedThrowingContinuation { continuation in
            let accumulator = Accumulator<[HeartRatePoint]>([])
            let query = HKQuantitySeriesSampleQuery(quantityType: type, predicate: predicate) { _, quantity, dateInterval, _, done, error in
                if accumulator.finished { return }
                if let error {
                    accumulator.finished = true
                    Self.finish(continuation, after: error, empty: accumulator.value)
                    return
                }
                if let quantity, let dateInterval {
                    let bpm = quantity.doubleValue(for: unit)
                    if bpm.isFinite {
                        accumulator.value.append(HeartRatePoint(date: dateInterval.start, bpm: bpm))
                    }
                }
                if done {
                    accumulator.finished = true
                    continuation.resume(returning: accumulator.value)
                }
            }
            store.execute(query)
        }
    }

    /// Highest heart rate since `start` (for `max_reference_bpm` when the age is unknown).
    func maximumHeartRate(since start: Date) async throws -> Double? {
        guard let type = HKObjectType.quantityType(forIdentifier: .heartRate) else { return nil }
        let predicate = HKQuery.predicateForSamples(withStart: start, end: Date(), options: [])
        let unit = HKUnit.count().unitDivided(by: .minute())
        return try await withCheckedThrowingContinuation { continuation in
            let query = HKStatisticsQuery(quantityType: type, quantitySamplePredicate: predicate, options: .discreteMax) { _, statistics, error in
                if let error {
                    Self.finish(continuation, after: error, empty: nil)
                    return
                }
                continuation.resume(returning: statistics?.maximumQuantity()?.doubleValue(for: unit))
            }
            store.execute(query)
        }
    }

    /// Age in whole years, if the user shares the date of birth.
    func age(on date: Date = Date()) -> Int? {
        // Not shared and not set are both "unknown" here; the caller falls back.
        guard let components = try? store.dateOfBirthComponents() else { return nil }
        let calendar = DayKey.localCalendar
        guard let birthDate = calendar.date(from: components) else { return nil }
        return calendar.dateComponents([.year], from: birthDate, to: date).year
    }

    // MARK: - Workout statistics

    func sum(_ identifier: HKQuantityTypeIdentifier, in workout: HKWorkout, unit: HKUnit) -> Double? {
        guard let type = HKObjectType.quantityType(forIdentifier: identifier) else { return nil }
        return workout.statistics(for: type)?.sumQuantity()?.doubleValue(for: unit)
    }

    func heartRateStatistics(in workout: HKWorkout) -> (average: Double?, minimum: Double?, maximum: Double?) {
        guard let type = HKObjectType.quantityType(forIdentifier: .heartRate),
              let statistics = workout.statistics(for: type) else {
            return (nil, nil, nil)
        }
        let unit = HKUnit.count().unitDivided(by: .minute())
        return (
            statistics.averageQuantity()?.doubleValue(for: unit),
            statistics.minimumQuantity()?.doubleValue(for: unit),
            statistics.maximumQuantity()?.doubleValue(for: unit)
        )
    }

    // MARK: - Background delivery

    /// One representative type per category used to wake the app when new data arrives.
    func observerTypes(for categories: Set<MetricCategory>) -> [HKSampleType] {
        var types: [HKSampleType] = []
        func addQuantity(_ identifier: HKQuantityTypeIdentifier) {
            if let type = HKObjectType.quantityType(forIdentifier: identifier) { types.append(type) }
        }
        func addCategory(_ identifier: HKCategoryTypeIdentifier) {
            if let type = HKObjectType.categoryType(forIdentifier: identifier) { types.append(type) }
        }
        for category in MetricCategory.allCases where categories.contains(category) {
            switch category {
            case .activity: addQuantity(.stepCount)
            case .heart: addQuantity(.restingHeartRate)
            case .sleep: addCategory(.sleepAnalysis)
            case .body: addQuantity(.bodyMass)
            case .vitals: addQuantity(.respiratoryRate)
            case .nutrition: addQuantity(.dietaryEnergyConsumed)
            case .mindfulness: addCategory(.mindfulSession)
            case .workouts: types.append(HKObjectType.workoutType())
            }
        }
        return types
    }
}
