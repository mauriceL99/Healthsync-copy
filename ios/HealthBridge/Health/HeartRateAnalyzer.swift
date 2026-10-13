import Foundation

struct HeartRatePoint: Equatable, Sendable {
    let date: Date
    let bpm: Double
}

/// Workout heart-rate details: zones, downsampled curve and 1-minute recovery
/// (docs/DATA_SCHEMA.md, "Workouts"). Pure logic, unit tested.
enum HeartRateAnalyzer {
    static let validRange: ClosedRange<Double> = 20...260
    static let maxSamples = 4_000
    static let minSampleSpacing = 5
    /// Gaps longer than this between two readings are not attributed to a zone.
    static let maxGapForZones: TimeInterval = 30
    static let zonePercentages: [Double] = [0.5, 0.6, 0.7, 0.8, 0.9, 1.0]

    /// `max_reference_bpm`: 220 − age if known, otherwise the highest heart rate observed in
    /// the last 365 days, otherwise 190. Always clamped to 100–240.
    static func maxReference(age: Int?, observedMax: Double?) -> Double {
        if let age, age > 0, age < 120 {
            return clampReference(Double(220 - age))
        }
        if let observedMax, observedMax.isFinite, observedMax >= 100 {
            return clampReference(observedMax.rounded())
        }
        return 190
    }

    private static func clampReference(_ value: Double) -> Double {
        min(max(value, 100), 240)
    }

    /// Zone boundaries in bpm: Z1 50–60 %, Z2 60–70 %, Z3 70–80 %, Z4 80–90 %, Z5 90–100 %.
    static func zoneBounds(maxReference: Double) -> [(zone: Int, lower: Double, upper: Double)] {
        let edges = zonePercentages.map { ($0 * maxReference).rounded() }
        return (1...5).map { zone in
            (zone: zone, lower: edges[zone - 1], upper: edges[zone])
        }
    }

    /// Zone (1–5) for a heart rate, or nil below zone 1. Values above the maximum count as Z5.
    static func zone(for bpm: Double, bounds: [(zone: Int, lower: Double, upper: Double)]) -> Int? {
        for bound in bounds.reversed() where bpm >= bound.lower {
            return bound.zone
        }
        return nil
    }

    static func analyze(
        points: [HeartRatePoint],
        start: Date,
        end: Date,
        maxReference: Double
    ) -> WorkoutHeartRateRecord {
        let valid = points
            .filter { $0.bpm.isFinite && validRange.contains($0.bpm) }
            .sorted { $0.date < $1.date }
        let during = valid.filter { $0.date >= start && $0.date <= end }

        var record = WorkoutHeartRateRecord()
        if !during.isEmpty {
            let values = during.map(\.bpm)
            record.avgBpm = MetricValue.normalize(values.reduce(0, +) / Double(values.count), decimals: 1, range: validRange)
            record.minBpm = values.min().flatMap { MetricValue.normalize($0, decimals: 0, range: validRange) }
            record.maxBpm = values.max().flatMap { MetricValue.normalize($0, decimals: 0, range: validRange) }
            record.maxReferenceBpm = maxReference
            record.zones = zones(during: during, end: end, maxReference: maxReference)
            let samples = downsample(during, start: start, end: end)
            record.samples = samples.isEmpty ? nil : samples
        }
        record.recovery1minBpm = recovery(points: valid, end: end)
        return record
    }

    static func zones(during points: [HeartRatePoint], end: Date, maxReference: Double) -> [HeartRateZoneRecord] {
        let bounds = zoneBounds(maxReference: maxReference)
        var seconds = [Double](repeating: 0, count: 5)
        for (index, point) in points.enumerated() {
            let next = index + 1 < points.count ? points[index + 1].date : end
            let duration = min(max(next.timeIntervalSince(point.date), 0), maxGapForZones)
            if let zoneNumber = Self.zone(for: point.bpm, bounds: bounds) {
                seconds[zoneNumber - 1] += duration
            }
        }
        return bounds.map { bound in
            HeartRateZoneRecord(
                zone: bound.zone,
                lowerBpm: bound.lower,
                upperBpm: bound.upper,
                seconds: min(seconds[bound.zone - 1].rounded(), 172_800)
            )
        }
    }

    /// Averages readings into buckets of at least 5 s (larger for very long workouts so that
    /// at most 4000 samples remain). `t` is the bucket start in seconds since `start`.
    static func downsample(_ points: [HeartRatePoint], start: Date, end: Date) -> [HeartRateSampleRecord] {
        let duration = max(end.timeIntervalSince(start), 0)
        var bucketSize = minSampleSpacing
        let needed = Int((duration / Double(maxSamples)).rounded(.up))
        if needed > bucketSize {
            bucketSize = Int((Double(needed) / Double(minSampleSpacing)).rounded(.up)) * minSampleSpacing
        }

        var sums: [Int: (total: Double, count: Int)] = [:]
        for point in points {
            let offset = point.date.timeIntervalSince(start)
            guard offset >= 0, offset <= min(duration, 172_800) else { continue }
            let bucket = Int(offset) / bucketSize
            let current = sums[bucket] ?? (total: 0, count: 0)
            sums[bucket] = (total: current.total + point.bpm, count: current.count + 1)
        }
        let samples = sums.keys.sorted().compactMap { bucket -> HeartRateSampleRecord? in
            guard let entry = sums[bucket], entry.count > 0 else { return nil }
            let average = (entry.total / Double(entry.count)).rounded()
            guard validRange.contains(average) else { return nil }
            return HeartRateSampleRecord(t: bucket * bucketSize, bpm: average)
        }
        return Array(samples.prefix(maxSamples))
    }

    /// Heart rate at the end of the workout minus heart rate one minute later.
    static func recovery(points: [HeartRatePoint], end: Date) -> Double? {
        let atEnd = points.last(where: { $0.date <= end && $0.date >= end.addingTimeInterval(-30) })
        let target = end.addingTimeInterval(60)
        let afterCandidates = points.filter {
            $0.date >= target.addingTimeInterval(-15) && $0.date <= target.addingTimeInterval(20)
        }
        let after = afterCandidates.min(by: {
            abs($0.date.timeIntervalSince(target)) < abs($1.date.timeIntervalSince(target))
        })
        guard let atEnd, let after else { return nil }
        return MetricValue.normalize(atEnd.bpm - after.bpm, decimals: 0, range: 0...150)
    }
}
