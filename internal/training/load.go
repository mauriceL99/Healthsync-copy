package training

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"time"
)

// Load bases.
const (
	LoadGarmin = "garmin_training_load"
	LoadTRIMP  = "trimp"
	LoadMixed  = "mixed"
)

// Time constants (days) of the fitness and fatigue curves.
const (
	fitnessDays = 42
	fatigueDays = 7
	chronicDays = 28
)

const dateLayout = "2006-01-02"

// ActivityLoad returns the activity's training load and its basis: the
// watch's own load when it recorded one, otherwise TRIMP.
func ActivityLoad(a *Activity) (float64, string) {
	if a.Summary.TrainingLoad > 0 {
		return a.Summary.TrainingLoad, LoadGarmin
	}
	if a.Summary.TRIMP > 0 {
		return a.Summary.TRIMP, LoadTRIMP
	}
	return 0, ""
}

// LocalDate is the activity's start date in the watch's time zone (or loc).
func LocalDate(a *Activity, loc *time.Location) string {
	return a.Local(loc).Format(dateLayout)
}

// KindTotals sums activities of one kind.
type KindTotals struct {
	Kind      string  `json:"kind"`
	Count     int     `json:"count"`
	Seconds   float64 `json:"seconds"`
	DistanceM float64 `json:"distance_m,omitempty"`
	AscentM   float64 `json:"ascent_m,omitempty"`
	Load      float64 `json:"load,omitempty"`
	Calories  float64 `json:"calories_kcal,omitempty"`
}

// WeekTotals sums one ISO week (Monday to Sunday).
type WeekTotals struct {
	Week             string       `json:"week"` // e.g. 2026-W42
	From             string       `json:"from"` // Monday
	Count            int          `json:"count"`
	Seconds          float64      `json:"seconds"`
	DistanceM        float64      `json:"distance_m,omitempty"`
	Load             float64      `json:"load,omitempty"`
	StrengthVolumeKg float64      `json:"strength_volume_kg,omitempty"`
	RunEF            float64      `json:"run_efficiency_factor,omitempty"`
	BikeEF           float64      `json:"bike_efficiency_factor,omitempty"`
	ByKind           []KindTotals `json:"by_kind"`
}

// LoadStatus is the training-load picture on one day.
type LoadStatus struct {
	Date        string   `json:"date"`
	Basis       string   `json:"basis"`
	Acute7      float64  `json:"acute_7d"`           // load of the last 7 days
	Chronic     float64  `json:"chronic_weekly_avg"` // average weekly load of the last 28 days
	Ratio       *float64 `json:"acute_chronic_ratio,omitempty"`
	Fitness     float64  `json:"fitness_ctl"` // 42-day exponentially weighted daily load
	Fatigue     float64  `json:"fatigue_atl"` // 7-day exponentially weighted daily load
	Form        float64  `json:"form_tsb"`    // fitness − fatigue
	HistoryDays int      `json:"history_days"`
}

// Intensity is the time spent per heart-rate zone across activities.
type Intensity struct {
	ZoneSeconds []float64 `json:"zone_seconds"` // index 0 = below zone 1
	LowPct      float64   `json:"low_pct"`      // below zone 1 to zone 2
	ModeratePct float64   `json:"moderate_pct"` // zone 3
	HighPct     float64   `json:"high_pct"`     // zone 4 and above
}

// Report summarizes training over a window of local dates.
type Report struct {
	From           string         `json:"from"`
	To             string         `json:"to"`
	Activities     int            `json:"activities"`
	Totals         []KindTotals   `json:"totals"`
	Weeks          []WeekTotals   `json:"weeks"`
	Load           *LoadStatus    `json:"load,omitempty"`
	Intensity      *Intensity     `json:"intensity,omitempty"`
	TrainingEffect map[string]int `json:"aerobic_training_effect_bands,omitempty"`
	Notes          []string       `json:"notes,omitempty"`
}

// BuildReport summarizes the activities with a local date in [from, to].
// history should hold all activities up to `to` (at least 6 weeks back) so
// that load curves start warm.
func BuildReport(history []*Activity, from, to string, loc *time.Location) Report {
	r := Report{From: from, To: to, Totals: []KindTotals{}, Weeks: []WeekTotals{}}
	totals := map[string]*KindTotals{}
	weeks := map[string]*WeekTotals{}
	weekKinds := map[string]map[string]*KindTotals{}
	type efAcc struct{ run, runN, bike, bikeN float64 }
	ef := map[string]*efAcc{}
	var zoneSec []float64
	te := map[string]int{}
	for _, a := range history {
		date := LocalDate(a, loc)
		if date < from || date > to {
			continue
		}
		r.Activities++
		load, _ := ActivityLoad(a)
		addKind(totals, a, load)
		day, _ := time.Parse(dateLayout, date)
		y, w := day.ISOWeek()
		key := fmt.Sprintf("%d-W%02d", y, w)
		wt := weeks[key]
		if wt == nil {
			monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
			wt = &WeekTotals{Week: key, From: monday.Format(dateLayout)}
			weeks[key], weekKinds[key], ef[key] = wt, map[string]*KindTotals{}, &efAcc{}
		}
		wt.Count++
		wt.Seconds += a.Summary.TimerS
		wt.DistanceM += a.Summary.DistanceM
		wt.Load += load
		wt.StrengthVolumeKg += a.Summary.VolumeKg
		addKind(weekKinds[key], a, load)
		if e := a.Summary.EfficiencyFactor; e > 0 {
			switch a.Kind {
			case KindRun:
				ef[key].run += e
				ef[key].runN++
			case KindBike:
				ef[key].bike += e
				ef[key].bikeN++
			}
		}
		for i, z := range a.HRZones {
			for len(zoneSec) <= i {
				zoneSec = append(zoneSec, 0)
			}
			zoneSec[i] += z.Seconds
		}
		if a.Summary.AerobicTE > 0 {
			te[teBand(a.Summary.AerobicTE)]++
		}
	}
	for _, t := range sortedKinds(totals) {
		r.Totals = append(r.Totals, roundKind(t))
	}
	for _, key := range slices.Sorted(maps.Keys(weeks)) {
		wt := weeks[key]
		for _, t := range sortedKinds(weekKinds[key]) {
			wt.ByKind = append(wt.ByKind, roundKind(t))
		}
		if e := ef[key]; e.runN > 0 {
			wt.RunEF = round(e.run/e.runN, 2)
		}
		if e := ef[key]; e.bikeN > 0 {
			wt.BikeEF = round(e.bike/e.bikeN, 2)
		}
		wt.Seconds, wt.DistanceM, wt.Load, wt.StrengthVolumeKg = math.Round(wt.Seconds), math.Round(wt.DistanceM), math.Round(wt.Load), math.Round(wt.StrengthVolumeKg)
		r.Weeks = append(r.Weeks, *wt)
	}
	if len(te) > 0 {
		r.TrainingEffect = te
	}
	r.Intensity = intensity(zoneSec)
	r.Load = loadStatus(history, to, loc)
	switch l := r.Load; {
	case l == nil || l.Ratio != nil:
	case l.HistoryDays < chronicDays:
		r.Notes = append(r.Notes, fmt.Sprintf("The acute:chronic ratio needs %d days of imported history; only %d available.", chronicDays, l.HistoryDays))
	default:
		r.Notes = append(r.Notes, "No training load in the last 4 weeks, so there is no acute:chronic ratio.")
	}
	if r.Load != nil && r.Load.Basis == LoadMixed {
		r.Notes = append(r.Notes, "Load mixes the watch's training load with TRIMP (for activities without one); the two scales are similar but not identical.")
	}
	return r
}

func addKind(m map[string]*KindTotals, a *Activity, load float64) {
	t := m[a.Kind]
	if t == nil {
		t = &KindTotals{Kind: a.Kind}
		m[a.Kind] = t
	}
	t.Count++
	t.Seconds += a.Summary.TimerS
	t.DistanceM += a.Summary.DistanceM
	t.AscentM += a.Summary.AscentM
	t.Load += load
	t.Calories += a.Summary.Calories
}

func roundKind(t *KindTotals) KindTotals {
	out := *t
	out.Seconds, out.DistanceM, out.AscentM, out.Load, out.Calories = math.Round(t.Seconds), math.Round(t.DistanceM), math.Round(t.AscentM), math.Round(t.Load), math.Round(t.Calories)
	return out
}

func sortedKinds(m map[string]*KindTotals) []*KindTotals {
	out := make([]*KindTotals, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b *KindTotals) int {
		if a.Seconds != b.Seconds {
			if a.Seconds > b.Seconds {
				return -1
			}
			return 1
		}
		if a.Kind < b.Kind {
			return -1
		}
		return 1
	})
	return out
}

// teBand names Garmin's aerobic training-effect ranges.
func teBand(te float64) string {
	switch {
	case te >= 5:
		return "overreaching"
	case te >= 4:
		return "highly_improving"
	case te >= 3:
		return "improving"
	case te >= 2:
		return "maintaining"
	case te >= 1:
		return "minor"
	}
	return "no_benefit"
}

func intensity(zoneSec []float64) *Intensity {
	var total, low, mod, high float64
	for i, s := range zoneSec {
		total += s
		switch {
		case i <= 2:
			low += s
		case i == 3:
			mod += s
		default:
			high += s
		}
	}
	if total == 0 {
		return nil
	}
	rounded := make([]float64, len(zoneSec))
	for i, s := range zoneSec {
		rounded[i] = math.Round(s)
	}
	return &Intensity{ZoneSeconds: rounded, LowPct: round(low/total*100, 1), ModeratePct: round(mod/total*100, 1), HighPct: round(high/total*100, 1)}
}

// loadStatus computes acute and chronic load and the fitness/fatigue curves
// on date `to` from all activities up to that day.
func loadStatus(history []*Activity, to string, loc *time.Location) *LoadStatus {
	daily := map[string]float64{}
	first := ""
	bases := map[string]bool{}
	for _, a := range history {
		date := LocalDate(a, loc)
		if date > to {
			continue
		}
		load, basis := ActivityLoad(a)
		if load == 0 {
			continue
		}
		daily[date] += load
		bases[basis] = true
		if first == "" || date < first {
			first = date
		}
	}
	if first == "" {
		return nil
	}
	end, err := time.Parse(dateLayout, to)
	if err != nil {
		return nil
	}
	start, _ := time.Parse(dateLayout, first)
	st := &LoadStatus{Date: to, Basis: LoadGarmin}
	switch {
	case bases[LoadGarmin] && bases[LoadTRIMP]:
		st.Basis = LoadMixed
	case bases[LoadTRIMP]:
		st.Basis = LoadTRIMP
	}
	kFit, kFat := 1-math.Exp(-1.0/fitnessDays), 1-math.Exp(-1.0/fatigueDays)
	var fit, fat, chronic float64
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		load := daily[d.Format(dateLayout)]
		fit += (load - fit) * kFit
		fat += (load - fat) * kFat
		age := int(end.Sub(d).Hours()/24 + 0.5)
		if age < fatigueDays {
			st.Acute7 += load
		}
		if age < chronicDays {
			chronic += load
		}
	}
	st.HistoryDays = int(end.Sub(start).Hours()/24+0.5) + 1
	st.Chronic = round(chronic/(chronicDays/fatigueDays), 1)
	st.Acute7 = round(st.Acute7, 1)
	st.Fitness, st.Fatigue = round(fit, 1), round(fat, 1)
	st.Form = round(fit-fat, 1)
	if st.HistoryDays >= chronicDays && st.Chronic > 0 {
		ratio := round(st.Acute7/st.Chronic, 2)
		st.Ratio = &ratio
	}
	return st
}

// Record is a personal best within a set of activities.
type Record struct {
	Category   string  `json:"category"` // run_distance, run_power, bike_power, strength_1rm, strength_weight, hyrox, longest
	Label      string  `json:"label"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`                 // s, W, kg, m
	DistanceM  float64 `json:"distance_m,omitempty"` // of a run_distance record
	Date       string  `json:"date"`
	ActivityID string  `json:"activity_id"`
	AvgHR      float64 `json:"avg_hr_bpm,omitempty"`
}

// Records finds personal bests: fastest times over standard distances, best
// power per duration, heaviest weight and estimated 1RM per exercise,
// fastest HYROX and longest run and ride.
func Records(acts []*Activity, loc *time.Location) []Record {
	best := map[string]Record{}
	order := []string{}
	consider := func(r Record, lower bool) {
		key := r.Category + "|" + r.Label
		old, ok := best[key]
		if !ok {
			order = append(order, key)
		}
		if !ok || (lower && r.Value < old.Value) || (!lower && r.Value > old.Value) {
			best[key] = r
		}
	}
	for _, a := range acts {
		date := LocalDate(a, loc)
		for _, b := range a.Bests {
			switch {
			case b.Kind == "distance" && a.Kind == KindRun:
				consider(Record{Category: "run_distance", Label: b.Label, Value: b.Seconds, Unit: "s", DistanceM: b.DistanceM, Date: date, ActivityID: a.ID, AvgHR: b.AvgHR}, true)
			case b.Kind == "power" && (a.Kind == KindRun || a.Kind == KindBike):
				consider(Record{Category: a.Kind + "_power", Label: b.Label, Value: b.Watts, Unit: "W", Date: date, ActivityID: a.ID, AvgHR: b.AvgHR}, false)
			}
		}
		if st := StrengthSummary(a); st != nil {
			for _, e := range st.Exercises {
				if e.Est1RMKg > 0 {
					consider(Record{Category: "strength_1rm", Label: e.Exercise, Value: e.Est1RMKg, Unit: "kg", Date: date, ActivityID: a.ID}, false)
				}
				if e.MaxWeightKg > 0 {
					consider(Record{Category: "strength_weight", Label: e.Exercise, Value: e.MaxWeightKg, Unit: "kg", Date: date, ActivityID: a.ID}, false)
				}
			}
		}
		switch a.Kind {
		case KindHyrox:
			consider(Record{Category: "hyrox", Label: "Total time", Value: math.Round(a.Summary.TimerS), Unit: "s", Date: date, ActivityID: a.ID, AvgHR: a.Summary.AvgHR}, true)
		case KindRun, KindBike:
			if a.Summary.DistanceM > 0 {
				consider(Record{Category: "longest", Label: "Longest " + a.Kind, Value: math.Round(a.Summary.DistanceM), Unit: "m", Date: date, ActivityID: a.ID}, false)
			}
		}
	}
	out := make([]Record, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	slices.SortStableFunc(out, func(a, b Record) int { return categoryRank(a.Category) - categoryRank(b.Category) })
	return out
}

func categoryRank(c string) int {
	if i := slices.Index([]string{"run_distance", "run_power", "bike_power", "hyrox", "strength_1rm", "strength_weight", "longest"}, c); i >= 0 {
		return i
	}
	return 99
}
