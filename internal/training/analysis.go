package training

import (
	"fmt"
	"math"
	"slices"
)

// HYROX lap classification: a lap is a run when it covers at least
// hyroxMinRunM at no slower than hyroxMinRunSpeed (about 10:25 min/km).
const (
	hyroxMinRunM     = 300
	hyroxMinRunSpeed = 1.6
)

// HyroxStations is the standard station order of a HYROX race.
var HyroxStations = []string{"SkiErg", "Sled Push", "Sled Pull", "Burpee Broad Jumps", "Rowing", "Farmers Carry", "Sandbag Lunges", "Wall Balls"}

// HyroxSplit is one run, station or transition of a HYROX race or simulation.
type HyroxSplit struct {
	Type   string `json:"type"`             // run, station or transition
	Number int    `json:"number,omitempty"` // Run 1–8, Station 1–8
	Name   string `json:"name,omitempty"`
	// NameAssumed is set when the name comes from the standard station order
	// rather than from the watch.
	NameAssumed bool    `json:"name_from_standard_order,omitempty"`
	Seconds     float64 `json:"seconds"`
	DistanceM   float64 `json:"distance_m,omitempty"`
	PaceSPerKm  float64 `json:"pace_s_per_km,omitempty"`
	AvgHR       float64 `json:"avg_hr_bpm,omitempty"`
	MaxHR       float64 `json:"max_hr_bpm,omitempty"`
}

// Hyrox summarizes a HYROX activity.
type Hyrox struct {
	Source       string       `json:"source"` // "segments" (multisport) or "laps"
	Splits       []HyroxSplit `json:"splits"`
	Runs         int          `json:"runs"`
	Stations     int          `json:"stations"`
	RunS         float64      `json:"run_s"`
	StationS     float64      `json:"station_s"`
	TransitionS  float64      `json:"transition_s,omitempty"`
	AvgRunPace   float64      `json:"avg_run_pace_s_per_km,omitempty"`
	FastestRun   int          `json:"fastest_run,omitempty"`
	SlowestRun   int          `json:"slowest_run,omitempty"`
	RunFadePct   *float64     `json:"run_fade_pct,omitempty"` // last vs. first run pace; positive = slower
	SlowestStage string       `json:"slowest_station,omitempty"`
}

// HyroxSplits splits a HYROX activity into runs and stations: from its
// sessions when it was recorded as multisport, otherwise from its laps.
func HyroxSplits(a *Activity) *Hyrox {
	h := &Hyrox{}
	switch {
	case len(a.Segments) > 1:
		h.Source = "segments"
		for _, s := range a.Segments {
			typ := "station"
			switch s.Sport {
			case "running":
				typ = "run"
			case "transition":
				typ = "transition"
			}
			h.Splits = append(h.Splits, HyroxSplit{Type: typ, Name: s.Name, Seconds: s.TimerS, DistanceM: s.DistanceM, AvgHR: s.AvgHR, MaxHR: s.MaxHR})
		}
	case len(a.Laps) > 1:
		h.Source = "laps"
		for _, l := range a.Laps {
			typ := "station"
			if l.DistanceM >= hyroxMinRunM && l.AvgSpeed >= hyroxMinRunSpeed {
				typ = "run"
			}
			h.Splits = append(h.Splits, HyroxSplit{Type: typ, Name: l.Name, Seconds: l.TimerS, DistanceM: l.DistanceM, AvgHR: l.AvgHR, MaxHR: l.MaxHR})
		}
	default:
		return nil
	}
	var firstPace, lastPace, fastest, slowest float64
	var slowestStation float64
	unnamed := true
	for i := range h.Splits {
		s := &h.Splits[i]
		switch s.Type {
		case "run":
			h.Runs++
			s.Number = h.Runs
			h.RunS += s.Seconds
			if s.DistanceM > 0 {
				s.PaceSPerKm = math.Round(s.Seconds / s.DistanceM * 1000)
				if firstPace == 0 {
					firstPace = s.PaceSPerKm
				}
				lastPace = s.PaceSPerKm
				if fastest == 0 || s.PaceSPerKm < fastest {
					fastest, h.FastestRun = s.PaceSPerKm, s.Number
				}
				if s.PaceSPerKm > slowest {
					slowest, h.SlowestRun = s.PaceSPerKm, s.Number
				}
			}
		case "station":
			h.Stations++
			s.Number = h.Stations
			h.StationS += s.Seconds
			unnamed = unnamed && s.Name == ""
		default:
			h.TransitionS += s.Seconds
		}
	}
	if unnamed && h.Stations == len(HyroxStations) {
		for i := range h.Splits {
			if s := &h.Splits[i]; s.Type == "station" {
				s.Name, s.NameAssumed = HyroxStations[s.Number-1], true
			}
		}
	}
	for _, s := range h.Splits {
		if s.Type == "station" && s.Seconds > slowestStation {
			slowestStation = s.Seconds
			h.SlowestStage = firstNonEmpty(s.Name, fmt.Sprintf("Station %d", s.Number))
		}
	}
	var runDist float64
	for _, s := range h.Splits {
		if s.Type == "run" {
			runDist += s.DistanceM
		}
	}
	if runDist > 0 {
		h.AvgRunPace = math.Round(h.RunS / runDist * 1000)
	}
	if h.Runs > 1 && firstPace > 0 {
		fade := round((lastPace-firstPace)/firstPace*100, 1)
		h.RunFadePct = &fade
	}
	return h
}

// ExerciseSummary aggregates the sets of one exercise in a strength session.
type ExerciseSummary struct {
	Exercise    string  `json:"exercise"`
	Category    string  `json:"category,omitempty"`
	Sets        int     `json:"sets"`
	Reps        int     `json:"reps"`
	MaxWeightKg float64 `json:"max_weight_kg,omitempty"`
	VolumeKg    float64 `json:"volume_kg,omitempty"`
	BestSet     string  `json:"best_set,omitempty"` // heaviest set, e.g. "5 × 100 kg"
	// Est1RMKg is the estimated one-repetition maximum (Epley) of the best
	// set with 1–12 reps.
	Est1RMKg float64 `json:"est_1rm_kg,omitempty"`
	WorkS    float64 `json:"work_s"`
}

// Strength summarizes a strength session.
type Strength struct {
	Exercises []ExerciseSummary `json:"exercises"`
	Sets      int               `json:"active_sets"`
	Reps      int               `json:"reps"`
	VolumeKg  float64           `json:"volume_kg,omitempty"`
	WorkS     float64           `json:"work_s"`
	RestS     float64           `json:"rest_s"`
	AvgRestS  float64           `json:"avg_rest_s,omitempty"`
}

// maxEpleyReps is the highest rep count the Epley estimate is used for.
const maxEpleyReps = 12

// Epley estimates the one-repetition maximum from a set.
func Epley(weightKg float64, reps int) float64 {
	if reps <= 0 || reps > maxEpleyReps || weightKg <= 0 {
		return 0
	}
	if reps == 1 {
		return weightKg
	}
	return weightKg * (1 + float64(reps)/30)
}

// StrengthSummary groups the recorded sets by exercise, in order of first use.
func StrengthSummary(a *Activity) *Strength {
	if len(a.Sets) == 0 {
		return nil
	}
	st := &Strength{}
	index := map[string]int{}
	rests := 0
	for _, s := range a.Sets {
		if s.Rest {
			st.RestS += s.Duration
			rests++
			continue
		}
		name := firstNonEmpty(s.Exercise, s.Category, "unknown exercise")
		i, ok := index[name]
		if !ok {
			i = len(st.Exercises)
			index[name] = i
			st.Exercises = append(st.Exercises, ExerciseSummary{Exercise: name, Category: s.Category})
		}
		e := &st.Exercises[i]
		e.Sets++
		e.Reps += s.Reps
		e.WorkS += s.Duration
		st.Sets++
		st.Reps += s.Reps
		st.WorkS += s.Duration
		if s.WeightKg == nil {
			continue
		}
		w := *s.WeightKg
		e.VolumeKg += w * float64(s.Reps)
		st.VolumeKg += w * float64(s.Reps)
		if w > e.MaxWeightKg {
			e.MaxWeightKg = w
			e.BestSet = fmt.Sprintf("%d × %s kg", s.Reps, trimFloat(w))
		}
		e.Est1RMKg = math.Max(e.Est1RMKg, round(Epley(w, s.Reps), 1))
	}
	if rests > 0 {
		st.AvgRestS = math.Round(st.RestS / float64(rests))
	}
	return st
}

func trimFloat(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func firstNonEmpty(values ...string) string {
	if i := slices.IndexFunc(values, func(s string) bool { return s != "" }); i >= 0 {
		return values[i]
	}
	return ""
}
