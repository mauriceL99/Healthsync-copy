import HealthKit
import XCTest
@testable import HealthBridge

/// Sleep attribution, workout heart-rate analysis, day ranges and sync planning.
final class HealthLogicTests: XCTestCase {
    private var calendar: Calendar!

    override func setUp() {
        super.setUp()
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Europe/Berlin") ?? .current
        self.calendar = calendar
    }

    /// Local time in Europe/Berlin.
    private func date(_ day: Int, _ hour: Int, _ minute: Int = 0, month: Int = 10, second: Int = 0) -> Date {
        let components = DateComponents(
            calendar: calendar, timeZone: calendar.timeZone,
            year: 2026, month: month, day: day, hour: hour, minute: minute, second: second
        )
        return calendar.date(from: components) ?? Date()
    }

    // MARK: - Sleep

    func testWakeDayAttribution() {
        // Ends before 18:00 → same day; at or after 18:00 → next day.
        XCTAssertEqual(DayKey.string(from: SleepAggregator.wakeDay(forEnd: date(12, 6), calendar: calendar), calendar: calendar), "2026-10-12")
        XCTAssertEqual(DayKey.string(from: SleepAggregator.wakeDay(forEnd: date(12, 15), calendar: calendar), calendar: calendar), "2026-10-12")
        XCTAssertEqual(DayKey.string(from: SleepAggregator.wakeDay(forEnd: date(12, 23), calendar: calendar), calendar: calendar), "2026-10-13")
        XCTAssertEqual(SleepAggregator.windowStart(forDay: date(12, 12), calendar: calendar), date(11, 18))
        XCTAssertEqual(SleepAggregator.windowEnd(forDay: date(12, 12), calendar: calendar), date(12, 18))
    }

    func testNightWithStagesAndInBed() throws {
        let intervals = [
            SleepInterval(stage: .inBed, start: date(11, 21, 50), end: date(12, 6, 10)),
            SleepInterval(stage: .core, start: date(11, 22, 0), end: date(12, 1, 0)),
            SleepInterval(stage: .deep, start: date(12, 1, 0), end: date(12, 2, 0)),
            SleepInterval(stage: .awake, start: date(12, 2, 0), end: date(12, 2, 20)),
            SleepInterval(stage: .rem, start: date(12, 2, 20), end: date(12, 6, 0)),
        ]
        let records = SleepAggregator.aggregate(intervals, calendar: calendar, dayKeys: ["2026-10-12"])
        let night = try XCTUnwrap(records["2026-10-12"])
        XCTAssertEqual(night.inBedMin, 500)
        XCTAssertEqual(night.coreMin, 180)
        XCTAssertEqual(night.deepMin, 60)
        XCTAssertEqual(night.remMin, 220)
        XCTAssertEqual(night.awakeMin, 20)
        XCTAssertEqual(night.asleepMin, 460)
        XCTAssertNil(night.unspecifiedMin)
        // Berlin is UTC+2 in October 2026 (before the switch on 25 October).
        XCTAssertEqual(night.bedtime, "2026-10-11T19:50:00Z")
        XCTAssertEqual(night.wakeTime, "2026-10-12T04:00:00Z")
        let sessions = try XCTUnwrap(night.sessions)
        XCTAssertEqual(sessions.map(\.stage), ["in_bed", "core", "deep", "awake", "rem", "in_bed"])
    }

    func testOverlappingSourcesAreNotDoubleCounted() throws {
        // iPhone says "asleep" 23:00–01:00, Apple Watch says "deep" 23:30–00:30.
        let intervals = [
            SleepInterval(stage: .asleepUnspecified, start: date(11, 23, 0), end: date(12, 1, 0)),
            SleepInterval(stage: .deep, start: date(11, 23, 30), end: date(12, 0, 30)),
            SleepInterval(stage: .deep, start: date(11, 23, 30), end: date(12, 0, 30)),
        ]
        let night = try XCTUnwrap(SleepAggregator.aggregate(intervals, calendar: calendar, dayKeys: ["2026-10-12"])["2026-10-12"])
        XCTAssertEqual(night.deepMin, 60)
        XCTAssertEqual(night.unspecifiedMin, 60)
        XCTAssertEqual(night.asleepMin, 120)
        XCTAssertEqual(night.inBedMin, 120)
    }

    func testDaysOutsideRangeAreDropped() {
        let intervals = [SleepInterval(stage: .core, start: date(12, 22), end: date(13, 6))]
        XCTAssertTrue(SleepAggregator.aggregate(intervals, calendar: calendar, dayKeys: ["2026-10-12"]).isEmpty)
    }

    func testAwakeOnlyProducesNoRecord() {
        XCTAssertNil(SleepAggregator.summarize([SleepInterval(stage: .awake, start: date(12, 3), end: date(12, 4))]))
    }

    // MARK: - Workout heart rate

    func testMaxReference() {
        XCTAssertEqual(HeartRateAnalyzer.maxReference(age: 30, observedMax: 200), 190)
        XCTAssertEqual(HeartRateAnalyzer.maxReference(age: nil, observedMax: 185.4), 185)
        XCTAssertEqual(HeartRateAnalyzer.maxReference(age: nil, observedMax: nil), 190)
        XCTAssertEqual(HeartRateAnalyzer.maxReference(age: nil, observedMax: 250), 240)
        XCTAssertEqual(HeartRateAnalyzer.maxReference(age: 150, observedMax: nil), 190)
    }

    func testZoneBounds() {
        let bounds = HeartRateAnalyzer.zoneBounds(maxReference: 200)
        XCTAssertEqual(bounds.map(\.zone), [1, 2, 3, 4, 5])
        XCTAssertEqual(bounds.map(\.lower), [100, 120, 140, 160, 180])
        XCTAssertEqual(bounds.map(\.upper), [120, 140, 160, 180, 200])
        XCTAssertNil(HeartRateAnalyzer.zone(for: 99, bounds: bounds))
        XCTAssertEqual(HeartRateAnalyzer.zone(for: 120, bounds: bounds), 2)
        XCTAssertEqual(HeartRateAnalyzer.zone(for: 210, bounds: bounds), 5)
    }

    func testAnalyzeSteadyWorkout() throws {
        let start = date(12, 16)
        let end = start.addingTimeInterval(600)
        var points = (0...600).map { HeartRatePoint(date: start.addingTimeInterval(Double($0)), bpm: 150) }
        points.append(HeartRatePoint(date: end.addingTimeInterval(60), bpm: 120))

        let record = HeartRateAnalyzer.analyze(points: points, start: start, end: end, maxReference: 200)
        XCTAssertEqual(record.avgBpm, 150)
        XCTAssertEqual(record.minBpm, 150)
        XCTAssertEqual(record.maxBpm, 150)
        XCTAssertEqual(record.maxReferenceBpm, 200)
        XCTAssertEqual(record.recovery1minBpm, 30)

        let zones = try XCTUnwrap(record.zones)
        XCTAssertEqual(zones.count, 5)
        XCTAssertEqual(zones[2].seconds, 600, "150 bpm is zone 3 (70–80 % of 200)")
        XCTAssertEqual(zones.filter { $0.zone != 3 }.map(\.seconds), [0, 0, 0, 0])

        let samples = try XCTUnwrap(record.samples)
        XCTAssertLessThanOrEqual(samples.count, 121)
        XCTAssertEqual(samples.first?.t, 0)
        for (previous, next) in zip(samples, samples.dropFirst()) {
            XCTAssertGreaterThanOrEqual(next.t - previous.t, 5)
        }
    }

    func testDownsampleLongWorkoutStaysWithinLimit() {
        let start = date(12, 6)
        let duration: TimeInterval = 30_000
        let points = stride(from: 0.0, through: duration, by: 1).map {
            HeartRatePoint(date: start.addingTimeInterval($0), bpm: 120)
        }
        let samples = HeartRateAnalyzer.downsample(points, start: start, end: start.addingTimeInterval(duration))
        XCTAssertLessThanOrEqual(samples.count, HeartRateAnalyzer.maxSamples)
        XCTAssertGreaterThan(samples.count, 1_000)
        for (previous, next) in zip(samples, samples.dropFirst()) {
            XCTAssertGreaterThanOrEqual(next.t - previous.t, 5)
        }
        XCTAssertTrue(samples.allSatisfy { $0.t >= 0 && $0.t <= 172_800 })
    }

    func testInvalidReadingsAreIgnored() {
        let start = date(12, 16)
        let points = [
            HeartRatePoint(date: start, bpm: .nan),
            HeartRatePoint(date: start.addingTimeInterval(5), bpm: 400),
            HeartRatePoint(date: start.addingTimeInterval(10), bpm: 10),
        ]
        let record = HeartRateAnalyzer.analyze(points: points, start: start, end: start.addingTimeInterval(60), maxReference: 190)
        XCTAssertTrue(record.isEmpty)
    }

    // MARK: - Mindfulness

    func testMergedDuration() {
        let base = date(12, 8)
        let intervals = [
            DateInterval(start: base, duration: 600),
            DateInterval(start: base.addingTimeInterval(300), duration: 600),
            DateInterval(start: base.addingTimeInterval(3_600), duration: 60),
        ]
        XCTAssertEqual(HealthBatchBuilder.mergedDuration(intervals), 960)
    }

    // MARK: - Day ranges and planning

    func testDayRangeChunks() {
        let range = DayRange.lastDays(365, endingOn: date(13, 9), calendar: calendar)
        XCTAssertEqual(range.dayKeys.first, "2025-10-14")
        XCTAssertEqual(range.dayKeys.last, "2026-10-13")
        let chunks = range.chunked(maxDays: 30)
        XCTAssertEqual(chunks.count, 13)
        XCTAssertEqual(chunks.map(\.dayCount).reduce(0, +), 365)
        XCTAssertEqual(chunks.first?.dayKeys.first, "2025-10-14")
        XCTAssertEqual(chunks.last?.dayKeys.last, "2026-10-13")
        XCTAssertEqual(Set(chunks.flatMap(\.dayKeys)).count, 365)
    }

    func testDayRangeAcrossDSTChange() {
        // 25 October 2026: clocks go back in Europe/Berlin (25-hour day).
        let range = DayRange(start: date(24, 12), dayCount: 3, calendar: calendar)
        XCTAssertEqual(range.dayKeys, ["2026-10-24", "2026-10-25", "2026-10-26"])
        XCTAssertEqual(range.end, date(27, 0))
    }

    func testPlanFirstSyncIsBackfill() {
        var settings = SyncSettings.default
        settings.historyWindow = .quarter
        let freePlan = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: settings, state: SyncState(), isPro: false, forceBackfill: false)
        XCTAssertEqual(freePlan.kind, .backfill)
        XCTAssertEqual(freePlan.range.dayCount, 7, "free tier is limited to 7 days")

        let proPlan = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: settings, state: SyncState(), isPro: true, forceBackfill: false)
        XCTAssertEqual(proPlan.kind, .backfill)
        XCTAssertEqual(proPlan.range.dayCount, 90)
        XCTAssertEqual(proPlan.chunks.count, 3)
    }

    func testPlanIncrementalSync() {
        var state = SyncState()
        state.backfilledDays = 7
        state.lastSuccessfulSync = date(13, 7)
        let plan = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: .default, state: state, isPro: false, forceBackfill: false)
        XCTAssertEqual(plan.kind, .sync)
        XCTAssertEqual(plan.range.dayKeys, ["2026-10-11", "2026-10-12", "2026-10-13"])
        XCTAssertEqual(plan.chunks.count, 1)
    }

    func testPlanCoversGapSinceLastSync() {
        var settings = SyncSettings.default
        settings.historyWindow = .month
        var state = SyncState()
        state.backfilledDays = 30
        state.lastSuccessfulSync = date(3, 7)
        let pro = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: settings, state: state, isPro: true, forceBackfill: false)
        XCTAssertEqual(pro.kind, .sync)
        XCTAssertEqual(pro.range.dayCount, 11)
        XCTAssertEqual(pro.range.dayKeys.first, "2026-10-03")

        let forced = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: settings, state: state, isPro: true, forceBackfill: true)
        XCTAssertEqual(forced.kind, .backfill)
        XCTAssertEqual(forced.range.dayCount, 30)
    }

    func testUpgradeTriggersLargerBackfill() {
        var settings = SyncSettings.default
        settings.historyWindow = .year
        var state = SyncState()
        state.backfilledDays = 7
        state.lastSuccessfulSync = date(13, 7)
        let plan = SyncPlan.make(now: date(13, 9), calendar: calendar, settings: settings, state: state, isPro: true, forceBackfill: false)
        XCTAssertEqual(plan.kind, .backfill)
        XCTAssertEqual(plan.range.dayCount, 365)
        XCTAssertEqual(plan.chunks.count, 13)
    }

    func testSettingsDecodingIsTolerant() throws {
        let json = Data(#"{"mode":"scheduled","interval":3,"unknown":true}"#.utf8)
        let settings = try JSONDecoder().decode(SyncSettings.self, from: json)
        XCTAssertEqual(settings.mode, .scheduled)
        XCTAssertEqual(settings.interval, .hours3)
        XCTAssertEqual(settings.categories, Set(MetricCategory.allCases))
        XCTAssertEqual(settings.effectiveMode(isPro: false), .manual)
        XCTAssertEqual(settings.effectiveMode(isPro: true), .scheduled)
    }

    /// HealthKit raises an exception for cumulative options on discrete types (and vice
    /// versa); HealthKitService.dailyStatistics relies on the catalog never doing that.
    func testQuantityAggregationsMatchHealthKitTypes() {
        for metric in MetricCatalog.all {
            guard case .quantity(let identifier) = metric.source else { continue }
            let isCumulative = HKQuantityType(identifier).aggregationStyle == .cumulative
            XCTAssertEqual(
                isCumulative,
                metric.aggregation.statisticsOption.contains(.cumulativeSum),
                "\(metric.key) uses \(identifier.rawValue)"
            )
        }
    }

    func testEveryGroupMapsToItsOwnCategory() {
        for group in MetricGroup.allCases {
            XCTAssertEqual(group.category.rawValue, group.rawValue)
        }
    }
}
