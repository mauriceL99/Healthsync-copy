package healthdata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"
)

// Limits from docs/DATA_SCHEMA.md.
const (
	MaxDays          = 4000
	MaxWorkouts      = 2000
	MaxSleepSessions = 300
	MaxHRSamples     = 4000
	MaxWorkoutSecs   = 172800

	// MinBPM and MaxBPM bound every heart-rate value (docs/DATA_SCHEMA.md).
	MinBPM = 20
	MaxBPM = 260
	// HeartRateZones is the number of workout heart-rate zones (Z1–Z5).
	HeartRateZones = 5
	// minYear and maxYear reject nonsensical day keys: Apple Health has no data
	// before the Unix epoch, and dates far in the future are corrupt input.
	minYear = 1970
	maxYear = 2200
)

var (
	datePattern      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	timezonePattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+./-]{1,63}$`)
	versionPattern   = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	modelPattern     = regexp.MustCompile(`^[A-Za-z]{2,16}[0-9]{0,3}(,[0-9]{1,3})?$`)
	workoutType      = regexp.MustCompile(`^[a-z][a-z0-9_]{1,47}$`)
	sourcePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 .'()&+-]{0,63}$`)
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$`)
	sleepStages      = map[string]bool{"in_bed": true, "asleep_unspecified": true, "core": true, "deep": true, "rem": true, "awake": true}
)

// Day maps group → key → value. Values are float64 for metrics, string for
// timestamps and []SleepSession for sleep.sessions.
type Day map[string]map[string]any

// SleepSession is one sleep stage interval.
type SleepSession struct {
	Stage string `json:"stage"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// HRZone is time spent in one heart-rate zone during a workout.
type HRZone struct {
	Zone     int     `json:"zone"`
	LowerBPM float64 `json:"lower_bpm"`
	UpperBPM float64 `json:"upper_bpm"`
	Seconds  float64 `json:"seconds"`
}

// HRSample is a heart-rate sample at t seconds after workout start.
type HRSample struct {
	T   float64 `json:"t"`
	BPM float64 `json:"bpm"`
}

// WorkoutHeartRate holds detailed workout heart-rate metrics.
type WorkoutHeartRate struct {
	AvgBPM          *float64   `json:"avg_bpm,omitempty"`
	MinBPM          *float64   `json:"min_bpm,omitempty"`
	MaxBPM          *float64   `json:"max_bpm,omitempty"`
	Recovery1MinBPM *float64   `json:"recovery_1min_bpm,omitempty"`
	MaxReferenceBPM *float64   `json:"max_reference_bpm,omitempty"`
	Zones           []HRZone   `json:"zones,omitempty"`
	Samples         []HRSample `json:"samples,omitempty"`
}

// Workout is one sanitized workout.
type Workout struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Start          string            `json:"start"`
	End            string            `json:"end"`
	DurationS      *float64          `json:"duration_s,omitempty"`
	EnergyKcal     *float64          `json:"energy_kcal,omitempty"`
	DistanceM      *float64          `json:"distance_m,omitempty"`
	ElevationGainM *float64          `json:"elevation_gain_m,omitempty"`
	Indoor         *bool             `json:"indoor,omitempty"`
	Source         string            `json:"source,omitempty"`
	HeartRate      *WorkoutHeartRate `json:"heart_rate,omitempty"`
}

// StartTime parses Start; sanitized workouts always have a valid start.
func (w Workout) StartTime() time.Time { t, _ := time.Parse(time.RFC3339Nano, w.Start); return t }

// Batch is a sanitized HealthBatch.
type Batch struct {
	Schema      int               `json:"schema"`
	GeneratedAt string            `json:"generated_at"`
	Timezone    string            `json:"timezone,omitempty"`
	Source      map[string]string `json:"source,omitempty"`
	Days        map[string]Day    `json:"days"`
	Workouts    []Workout         `json:"workouts"`
}

// Report counts what the sanitizer removed.
type Report struct {
	Days            int `json:"days"`
	Workouts        int `json:"workouts"`
	DroppedDays     int `json:"dropped_days"`
	DroppedValues   int `json:"dropped_values"`
	DroppedWorkouts int `json:"dropped_workouts"`
}

// Location returns the batch time zone, falling back to UTC.
func (b Batch) Location() *time.Location {
	if loc, err := time.LoadLocation(b.Timezone); err == nil && b.Timezone != "" {
		return loc
	}
	return time.UTC
}

// Parse decodes and sanitizes a plaintext HealthBatch. Unknown or invalid
// content is dropped; structural errors are returned.
func Parse(plaintext []byte) (Batch, Report, error) {
	dec := json.NewDecoder(bytes.NewReader(plaintext))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Batch{}, Report{}, fmt.Errorf("health batch is not a JSON object: %w", err)
	}
	if dec.More() {
		return Batch{}, Report{}, errors.New("trailing data after health batch")
	}
	return Sanitize(raw)
}

// Sanitize validates a decoded HealthBatch (numbers as json.Number or float64).
func Sanitize(raw map[string]any) (Batch, Report, error) {
	var rep Report
	if v, ok := num(raw["schema"]); !ok || v != 1 {
		return Batch{}, rep, errors.New("unsupported health batch schema")
	}
	b := Batch{Schema: 1, Days: map[string]Day{}, Workouts: []Workout{}}
	gen, _ := raw["generated_at"].(string)
	if !validTimestamp(gen) {
		return Batch{}, rep, errors.New("health batch generated_at is invalid")
	}
	b.GeneratedAt = gen
	if tz, ok := raw["timezone"].(string); ok && timezonePattern.MatchString(tz) {
		b.Timezone = tz
	}
	if src, ok := raw["source"].(map[string]any); ok {
		b.Source = map[string]string{}
		for key, pattern := range map[string]*regexp.Regexp{"app_version": versionPattern, "os_version": versionPattern, "device_model": modelPattern} {
			if s, ok := src[key].(string); ok && pattern.MatchString(s) {
				b.Source[key] = s
			}
		}
		if len(b.Source) == 0 {
			b.Source = nil
		}
	}
	days, _ := raw["days"].(map[string]any)
	keys := sortedKeys(days)
	for i, date := range keys {
		if i >= MaxDays || !validDate(date) {
			rep.DroppedDays++
			continue
		}
		groups, ok := days[date].(map[string]any)
		if !ok {
			rep.DroppedDays++
			continue
		}
		day := sanitizeDay(groups, &rep)
		if len(day) == 0 {
			rep.DroppedDays++
			continue
		}
		b.Days[date] = day
	}
	workouts, _ := raw["workouts"].([]any)
	seen := map[string]bool{}
	for i, item := range workouts {
		m, ok := item.(map[string]any)
		if i >= MaxWorkouts || !ok {
			rep.DroppedWorkouts++
			continue
		}
		w, err := sanitizeWorkout(m, &rep)
		if err != nil || seen[w.ID] {
			rep.DroppedWorkouts++
			continue
		}
		seen[w.ID] = true
		b.Workouts = append(b.Workouts, w)
	}
	rep.Days, rep.Workouts = len(b.Days), len(b.Workouts)
	return b, rep, nil
}

func sanitizeDay(groups map[string]any, rep *Report) Day {
	day := Day{}
	for _, group := range sortedKeys(groups) {
		values, ok := groups[group].(map[string]any)
		if !ok {
			rep.DroppedValues++
			continue
		}
		out := map[string]any{}
		for _, key := range sortedKeys(values) {
			value := values[key]
			if group == "sleep" {
				switch key {
				case "bedtime", "wake_time":
					if s, ok := value.(string); ok && validTimestamp(s) {
						out[key] = s
					} else {
						rep.DroppedValues++
					}
					continue
				case "sessions":
					if sessions := sanitizeSessions(value, rep); len(sessions) > 0 {
						out[key] = sessions
					}
					continue
				}
			}
			metric, known := Lookup(group + "." + key)
			n, isNum := num(value)
			if !known || !isNum || n < metric.Min || n > metric.Max {
				rep.DroppedValues++
				continue
			}
			out[key] = round(n, 3)
		}
		if len(out) > 0 {
			day[group] = out
		}
	}
	return day
}

func sanitizeSessions(value any, rep *Report) []SleepSession {
	items, ok := value.([]any)
	if !ok {
		rep.DroppedValues++
		return nil
	}
	var out []SleepSession
	for i, item := range items {
		m, ok := item.(map[string]any)
		if i >= MaxSleepSessions || !ok {
			rep.DroppedValues++
			continue
		}
		stage, _ := m["stage"].(string)
		start, _ := m["start"].(string)
		end, _ := m["end"].(string)
		if !sleepStages[stage] || !validInterval(start, end, 24*time.Hour) {
			rep.DroppedValues++
			continue
		}
		out = append(out, SleepSession{stage, start, end})
	}
	return out
}

func sanitizeWorkout(m map[string]any, rep *Report) (Workout, error) {
	var w Workout
	w.ID, _ = m["id"].(string)
	w.Type, _ = m["type"].(string)
	w.Start, _ = m["start"].(string)
	w.End, _ = m["end"].(string)
	if !uuidPattern.MatchString(w.ID) || !workoutType.MatchString(w.Type) || !validInterval(w.Start, w.End, MaxWorkoutSecs*time.Second) {
		return w, errors.New("invalid workout identity or interval")
	}
	w.DurationS = bounded(m, "duration_s", 0, MaxWorkoutSecs, rep)
	w.EnergyKcal = bounded(m, "energy_kcal", 0, 20000, rep)
	w.DistanceM = bounded(m, "distance_m", 0, 1000000, rep)
	w.ElevationGainM = bounded(m, "elevation_gain_m", 0, 20000, rep)
	if v, ok := m["indoor"].(bool); ok {
		w.Indoor = &v
	}
	if s, ok := m["source"].(string); ok {
		if sourcePattern.MatchString(s) {
			w.Source = s
		} else {
			rep.DroppedValues++
		}
	}
	if hr, ok := m["heart_rate"].(map[string]any); ok {
		w.HeartRate = sanitizeWorkoutHR(hr, rep)
	}
	return w, nil
}

func sanitizeWorkoutHR(m map[string]any, rep *Report) *WorkoutHeartRate {
	hr := &WorkoutHeartRate{
		AvgBPM:          bounded(m, "avg_bpm", MinBPM, MaxBPM, rep),
		MinBPM:          bounded(m, "min_bpm", MinBPM, MaxBPM, rep),
		MaxBPM:          bounded(m, "max_bpm", MinBPM, MaxBPM, rep),
		Recovery1MinBPM: bounded(m, "recovery_1min_bpm", 0, 150, rep),
		MaxReferenceBPM: bounded(m, "max_reference_bpm", 100, 240, rep),
	}
	if zones, ok := m["zones"].([]any); ok {
		for i, item := range zones {
			z, ok := item.(map[string]any)
			zone, okZ := num(z["zone"])
			lo, okL := num(z["lower_bpm"])
			hi, okH := num(z["upper_bpm"])
			secs, okS := num(z["seconds"])
			if i >= HeartRateZones || !ok || !okZ || !okL || !okH || !okS || zone < 1 || zone > HeartRateZones || zone != math.Trunc(zone) ||
				lo < 0 || hi > MaxBPM || lo > hi || secs < 0 || secs > MaxWorkoutSecs {
				rep.DroppedValues++
				continue
			}
			hr.Zones = append(hr.Zones, HRZone{int(zone), round(lo, 1), round(hi, 1), round(secs, 1)})
		}
	}
	if samples, ok := m["samples"].([]any); ok {
		for i, item := range samples {
			s, ok := item.(map[string]any)
			t, okT := num(s["t"])
			bpm, okB := num(s["bpm"])
			if i >= MaxHRSamples || !ok || !okT || !okB || t < 0 || t > MaxWorkoutSecs || bpm < MinBPM || bpm > MaxBPM {
				rep.DroppedValues++
				continue
			}
			hr.Samples = append(hr.Samples, HRSample{round(t, 1), round(bpm, 1)})
		}
		sort.SliceStable(hr.Samples, func(i, j int) bool { return hr.Samples[i].T < hr.Samples[j].T })
	}
	if hr.AvgBPM == nil && hr.MinBPM == nil && hr.MaxBPM == nil && len(hr.Zones) == 0 && len(hr.Samples) == 0 {
		return nil
	}
	return hr
}

func bounded(m map[string]any, key string, lo, hi float64, rep *Report) *float64 {
	value, present := m[key]
	if !present {
		return nil
	}
	n, ok := num(value)
	if !ok || n < lo || n > hi {
		rep.DroppedValues++
		return nil
	}
	n = round(n, 3)
	return &n
}

func num(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case json.Number:
		parsed, err := x.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	case float64:
		f = x
	case int:
		f = float64(x)
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func validDate(s string) bool {
	if !datePattern.MatchString(s) {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Year() >= minYear && t.Year() <= maxYear
}

func validTimestamp(s string) bool {
	if !timestampPattern.MatchString(s) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func validInterval(start, end string, max time.Duration) bool {
	if !validTimestamp(start) || !validTimestamp(end) {
		return false
	}
	a, _ := time.Parse(time.RFC3339Nano, start)
	b, _ := time.Parse(time.RFC3339Nano, end)
	return !b.Before(a) && b.Sub(a) <= max
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MergeDay overlays newer values onto an existing day (per group and key).
func MergeDay(old, newer Day) Day {
	out := Day{}
	for g, values := range old {
		out[g] = map[string]any{}
		for k, v := range values {
			out[g][k] = v
		}
	}
	for g, values := range newer {
		if out[g] == nil {
			out[g] = map[string]any{}
		}
		for k, v := range values {
			out[g][k] = v
		}
	}
	return out
}

// Number returns the numeric value at group.key, if present.
func (d Day) Number(path Metric) (float64, bool) {
	values := d[path.Group]
	if values == nil {
		return 0, false
	}
	return num(values[path.Key])
}

// String renders a short description for debugging.
func (r Report) String() string {
	return fmt.Sprintf("%d days, %d workouts (dropped: %d days, %d values, %d workouts)", r.Days, r.Workouts, r.DroppedDays, r.DroppedValues, r.DroppedWorkouts)
}
