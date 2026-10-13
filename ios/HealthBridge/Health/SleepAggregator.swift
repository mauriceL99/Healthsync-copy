import Foundation

enum SleepStage: String, CaseIterable, Sendable {
    case inBed = "in_bed"
    case asleepUnspecified = "asleep_unspecified"
    case core
    case deep
    case rem
    case awake

    /// When sources overlap (e.g. iPhone and Apple Watch), the most specific stage wins.
    var priority: Int {
        switch self {
        case .deep: return 6
        case .rem: return 5
        case .core: return 4
        case .asleepUnspecified: return 3
        case .awake: return 2
        case .inBed: return 1
        }
    }

    var isAsleep: Bool {
        switch self {
        case .core, .deep, .rem, .asleepUnspecified: return true
        case .awake, .inBed: return false
        }
    }
}

struct SleepInterval: Equatable, Sendable {
    let stage: SleepStage
    let start: Date
    let end: Date
}

/// Turns raw (possibly overlapping, multi-source) sleep samples into one `SleepRecord` per
/// wake-up day. Pure logic so it can be unit tested without HealthKit.
enum SleepAggregator {
    static let maxSessions = 300
    /// Sleep windows run from the previous day 18:00 to the day itself 18:00 (local time).
    static let dayBoundaryHour = 18

    /// The local day a sleep interval is attributed to: the day on which the user wakes up.
    /// An interval ending before 18:00 belongs to that calendar day, otherwise to the next one.
    static func wakeDay(forEnd end: Date, calendar: Calendar) -> Date {
        let dayStart = calendar.startOfDay(for: end)
        guard let boundary = calendar.date(bySettingHour: dayBoundaryHour, minute: 0, second: 0, of: dayStart) else {
            return dayStart
        }
        if end < boundary {
            return dayStart
        }
        return calendar.date(byAdding: .day, value: 1, to: dayStart) ?? dayStart
    }

    /// Start of the attribution window for `day` (previous day 18:00).
    static func windowStart(forDay day: Date, calendar: Calendar) -> Date {
        let dayStart = calendar.startOfDay(for: day)
        let previous = calendar.date(byAdding: .day, value: -1, to: dayStart) ?? dayStart
        return calendar.date(bySettingHour: dayBoundaryHour, minute: 0, second: 0, of: previous) ?? previous
    }

    /// End of the attribution window for `day` (the day itself 18:00).
    static func windowEnd(forDay day: Date, calendar: Calendar) -> Date {
        let dayStart = calendar.startOfDay(for: day)
        return calendar.date(bySettingHour: dayBoundaryHour, minute: 0, second: 0, of: dayStart) ?? dayStart
    }

    /// Groups intervals by wake day and summarizes each day. Only days in `dayKeys` are kept.
    static func aggregate(
        _ intervals: [SleepInterval],
        calendar: Calendar,
        dayKeys: Set<String>
    ) -> [String: SleepRecord] {
        var grouped: [String: [SleepInterval]] = [:]
        for interval in intervals where interval.end > interval.start {
            let key = DayKey.string(from: wakeDay(forEnd: interval.end, calendar: calendar), calendar: calendar)
            guard dayKeys.contains(key) else { continue }
            grouped[key, default: []].append(interval)
        }
        var result: [String: SleepRecord] = [:]
        for (key, dayIntervals) in grouped {
            if let record = summarize(dayIntervals) {
                result[key] = record
            }
        }
        return result
    }

    struct Segment: Equatable {
        let stage: SleepStage
        let start: Date
        var end: Date
    }

    /// Resolves overlaps with a sweep line: at every instant the highest-priority stage wins.
    static func timeline(_ intervals: [SleepInterval]) -> [Segment] {
        struct Event {
            let time: Date
            let stage: SleepStage
            let delta: Int
        }
        var events: [Event] = []
        events.reserveCapacity(intervals.count * 2)
        for interval in intervals where interval.end > interval.start {
            events.append(Event(time: interval.start, stage: interval.stage, delta: 1))
            events.append(Event(time: interval.end, stage: interval.stage, delta: -1))
        }
        events.sort { $0.time < $1.time }

        var active: [SleepStage: Int] = [:]
        var segments: [Segment] = []
        var previousTime: Date?
        var index = 0
        while index < events.count {
            let time = events[index].time
            if let previousTime, time > previousTime, let top = topStage(active) {
                if var last = segments.last, last.stage == top, last.end == previousTime {
                    last.end = time
                    segments[segments.count - 1] = last
                } else {
                    segments.append(Segment(stage: top, start: previousTime, end: time))
                }
            }
            while index < events.count && events[index].time == time {
                active[events[index].stage, default: 0] += events[index].delta
                index += 1
            }
            previousTime = time
        }
        return segments
    }

    private static func topStage(_ active: [SleepStage: Int]) -> SleepStage? {
        active.filter { $0.value > 0 }.keys.max { $0.priority < $1.priority }
    }

    static func summarize(_ intervals: [SleepInterval]) -> SleepRecord? {
        let segments = timeline(intervals)
        guard !segments.isEmpty else { return nil }

        var seconds: [SleepStage: TimeInterval] = [:]
        for segment in segments {
            seconds[segment.stage, default: 0] += segment.end.timeIntervalSince(segment.start)
        }
        let asleepSeconds = SleepStage.allCases.filter(\.isAsleep).reduce(0.0) { $0 + (seconds[$1] ?? 0) }
        let totalSeconds = segments.reduce(0.0) { $0 + $1.end.timeIntervalSince($1.start) }
        let inBedOnlySeconds = seconds[.inBed] ?? 0
        guard asleepSeconds > 0 || inBedOnlySeconds > 0 else {
            // Only "awake" samples: nothing meaningful to report.
            return nil
        }

        func minutes(_ value: TimeInterval?) -> Double? {
            guard let value, value > 0 else { return nil }
            return MetricValue.normalize(value / 60, decimals: 0, range: 0...1_440)
        }

        var record = SleepRecord()
        record.inBedMin = minutes(totalSeconds)
        record.asleepMin = minutes(asleepSeconds)
        record.coreMin = minutes(seconds[.core])
        record.deepMin = minutes(seconds[.deep])
        record.remMin = minutes(seconds[.rem])
        record.awakeMin = minutes(seconds[.awake])
        record.unspecifiedMin = minutes(seconds[.asleepUnspecified])

        if let firstStart = segments.first(where: { $0.stage != .awake })?.start {
            record.bedtime = RFC3339.string(from: firstStart)
        }
        if let lastAsleepEnd = segments.last(where: { $0.stage.isAsleep })?.end {
            record.wakeTime = RFC3339.string(from: lastAsleepEnd)
        }
        let sessions = segments.prefix(maxSessions).map {
            SleepSessionRecord(
                stage: $0.stage.rawValue,
                start: RFC3339.string(from: $0.start),
                end: RFC3339.string(from: $0.end)
            )
        }
        record.sessions = sessions.isEmpty ? nil : Array(sessions)
        return record
    }
}

/// `YYYY-MM-DD` keys for device-local calendar days (always Gregorian digits).
enum DayKey {
    static func string(from date: Date, calendar: Calendar) -> String {
        let components = calendar.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", components.year ?? 0, components.month ?? 0, components.day ?? 0)
    }

    static func string(from components: DateComponents) -> String? {
        guard let year = components.year, let month = components.month, let day = components.day else {
            return nil
        }
        return String(format: "%04d-%02d-%02d", year, month, day)
    }

    /// A Gregorian calendar in the device time zone, used for all day math.
    static var localCalendar: Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        calendar.locale = Locale(identifier: "en_US_POSIX")
        return calendar
    }
}
