// Package training turns workout files from sports watches (FIT, e.g. from a
// Garmin fenix) into activities with laps, strength sets and per-second
// streams, and computes analyses on them: best efforts, aerobic decoupling,
// HYROX splits, training load and records.
//
// GPS positions are deliberately never part of an Activity. The analyses do
// not need them, and an AI assistant reading the results should not learn
// where the user lives or trains.
package training

import "time"

// AnalysisVersion changes whenever Parse derives different values from the
// same file. Stored activities with an older version are parsed again.
const AnalysisVersion = 2

// Activity kinds.
const (
	KindRun        = "run"
	KindBike       = "bike"
	KindStrength   = "strength"
	KindHyrox      = "hyrox"
	KindCardio     = "cardio" // HIIT, rowing, elliptical, other cardio
	KindSwim       = "swim"
	KindWalk       = "walk" // walking and hiking
	KindMultisport = "multisport"
	KindOther      = "other"
)

// Activity is one recorded workout.
type Activity struct {
	ID       string    `json:"id"`
	Version  int       `json:"analysis_version"`
	Kind     string    `json:"kind"`
	Sport    string    `json:"sport"`
	SubSport string    `json:"sub_sport,omitempty"`
	Name     string    `json:"name,omitempty"` // activity profile name on the watch, e.g. "Trail Run"
	Device   string    `json:"device,omitempty"`
	Start    time.Time `json:"start"`
	// UTCOffsetMin is the watch's local time offset at the start, when the
	// file records one.
	UTCOffsetMin *int `json:"utc_offset_min,omitempty"`

	Summary    Summary     `json:"summary"`
	Segments   []Segment   `json:"segments,omitempty"` // sessions of a multisport activity
	Laps       []Lap       `json:"laps,omitempty"`
	Sets       []Set       `json:"sets,omitempty"`
	HRZones    []ZoneTime  `json:"hr_zones,omitempty"`
	PowerZones []ZoneTime  `json:"power_zones,omitempty"`
	Bests      []Best      `json:"bests,omitempty"`
	Decoupling *Decoupling `json:"aerobic_decoupling,omitempty"`
	HRV        *HRVSummary `json:"hrv,omitempty"`
	Athlete    *Athlete    `json:"athlete,omitempty"`
	Stream     *Stream     `json:"-"` // per-second data; kept out of stored summaries
	Warnings   []string    `json:"warnings,omitempty"`
}

// Local returns the start in the watch's local time, or in loc when the file
// has no offset.
func (a *Activity) Local(loc *time.Location) time.Time {
	if a.UTCOffsetMin != nil {
		return a.Start.In(time.FixedZone("", *a.UTCOffsetMin*60))
	}
	return a.Start.In(loc)
}

// Summary holds whole-activity totals and averages. Zero means "not recorded".
type Summary struct {
	ElapsedS  float64 `json:"elapsed_s"`
	TimerS    float64 `json:"timer_s"`
	MovingS   float64 `json:"moving_s,omitempty"`
	DistanceM float64 `json:"distance_m,omitempty"`
	Calories  float64 `json:"calories_kcal,omitempty"`
	AscentM   float64 `json:"ascent_m,omitempty"`
	DescentM  float64 `json:"descent_m,omitempty"`

	AvgHR float64 `json:"avg_hr_bpm,omitempty"`
	MaxHR float64 `json:"max_hr_bpm,omitempty"`
	MinHR float64 `json:"min_hr_bpm,omitempty"`

	AvgSpeed float64 `json:"avg_speed_mps,omitempty"`
	MaxSpeed float64 `json:"max_speed_mps,omitempty"`

	AvgPower        float64 `json:"avg_power_w,omitempty"`
	MaxPower        float64 `json:"max_power_w,omitempty"`
	NormalizedPower float64 `json:"normalized_power_w,omitempty"`
	// AvgCadence is steps per minute for running and walking, revolutions per
	// minute for cycling.
	AvgCadence float64 `json:"avg_cadence,omitempty"`
	MaxCadence float64 `json:"max_cadence,omitempty"`

	AerobicTE   float64 `json:"aerobic_training_effect,omitempty"`
	AnaerobicTE float64 `json:"anaerobic_training_effect,omitempty"`
	// TrainingLoad is the watch's exercise load (Garmin: EPOC based).
	TrainingLoad    float64 `json:"training_load,omitempty"`
	TRIMP           float64 `json:"trimp,omitempty"` // Banister TRIMP from the heart-rate stream
	TSS             float64 `json:"tss,omitempty"`
	IntensityFactor float64 `json:"intensity_factor,omitempty"`
	// EfficiencyFactor is normalized power per heartbeat for cycling and
	// speed (m/min) per heartbeat for running.
	EfficiencyFactor float64 `json:"efficiency_factor,omitempty"`

	AvgRespiration float64  `json:"avg_respiration_brpm,omitempty"`
	MaxRespiration float64  `json:"max_respiration_brpm,omitempty"`
	AvgTempC       *float64 `json:"avg_temperature_c,omitempty"`
	MaxTempC       *float64 `json:"max_temperature_c,omitempty"`

	Running *RunningDynamics `json:"running_dynamics,omitempty"`
	Cycling *CyclingDynamics `json:"cycling_dynamics,omitempty"`

	RPE  float64 `json:"rpe,omitempty"`  // 0–10, as entered on the watch
	Feel float64 `json:"feel,omitempty"` // 0–100, as entered on the watch

	ActiveSets int     `json:"active_sets,omitempty"`
	TotalReps  int     `json:"total_reps,omitempty"`
	VolumeKg   float64 `json:"volume_kg,omitempty"` // sum of reps × weight
}

// RunningDynamics are averages from a running-dynamics capable watch or pod.
type RunningDynamics struct {
	VerticalOscillationMM float64 `json:"vertical_oscillation_mm,omitempty"`
	GroundContactMS       float64 `json:"ground_contact_ms,omitempty"`
	GCTBalanceLeftPct     float64 `json:"ground_contact_balance_left_pct,omitempty"`
	VerticalRatioPct      float64 `json:"vertical_ratio_pct,omitempty"`
	StrideLengthM         float64 `json:"stride_length_m,omitempty"`
}

// CyclingDynamics come from power meters that measure both legs.
type CyclingDynamics struct {
	BalanceLeftPct         float64 `json:"balance_left_pct,omitempty"`
	TorqueEffectivenessPct float64 `json:"torque_effectiveness_pct,omitempty"`
	PedalSmoothnessPct     float64 `json:"pedal_smoothness_pct,omitempty"`
}

// Segment is one sport within a multisport activity (e.g. a HYROX station).
type Segment struct {
	Index     int     `json:"index"`
	Sport     string  `json:"sport"`
	SubSport  string  `json:"sub_sport,omitempty"`
	Name      string  `json:"name,omitempty"`
	StartS    float64 `json:"start_offset_s"`
	TimerS    float64 `json:"timer_s"`
	DistanceM float64 `json:"distance_m,omitempty"`
	AvgHR     float64 `json:"avg_hr_bpm,omitempty"`
	MaxHR     float64 `json:"max_hr_bpm,omitempty"`
	Calories  float64 `json:"calories_kcal,omitempty"`
}

// Lap is one lap or interval.
type Lap struct {
	Index     int     `json:"index"`
	Name      string  `json:"name,omitempty"` // workout step name, when the lap belongs to one
	Sport     string  `json:"sport,omitempty"`
	Intensity string  `json:"intensity,omitempty"` // active, rest, warmup, cooldown, recovery, interval
	Trigger   string  `json:"trigger,omitempty"`   // manual, distance, time, …
	StartS    float64 `json:"start_offset_s"`
	TimerS    float64 `json:"timer_s"`
	DistanceM float64 `json:"distance_m,omitempty"`
	AvgSpeed  float64 `json:"avg_speed_mps,omitempty"`
	MaxSpeed  float64 `json:"max_speed_mps,omitempty"`
	AvgHR     float64 `json:"avg_hr_bpm,omitempty"`
	MaxHR     float64 `json:"max_hr_bpm,omitempty"`
	AvgPower  float64 `json:"avg_power_w,omitempty"`
	MaxPower  float64 `json:"max_power_w,omitempty"`
	NormPower float64 `json:"normalized_power_w,omitempty"`
	Cadence   float64 `json:"avg_cadence,omitempty"`
	AscentM   float64 `json:"ascent_m,omitempty"`
	DescentM  float64 `json:"descent_m,omitempty"`
	Calories  float64 `json:"calories_kcal,omitempty"`

	GroundContactMS       float64 `json:"ground_contact_ms,omitempty"`
	VerticalOscillationMM float64 `json:"vertical_oscillation_mm,omitempty"`
	StrideLengthM         float64 `json:"stride_length_m,omitempty"`
	Respiration           float64 `json:"avg_respiration_brpm,omitempty"`
}

// Set is one strength-training set as recorded by the watch.
type Set struct {
	Index    int      `json:"index"`
	Rest     bool     `json:"rest,omitempty"`
	StartS   float64  `json:"start_offset_s"`
	Duration float64  `json:"duration_s"`
	Reps     int      `json:"reps,omitempty"`
	WeightKg *float64 `json:"weight_kg,omitempty"`
	Category string   `json:"category,omitempty"` // e.g. squat, bench_press
	Exercise string   `json:"exercise,omitempty"` // e.g. back_squat
}

// ZoneTime is the time spent in one heart-rate or power zone.
type ZoneTime struct {
	Zone    int     `json:"zone"`           // 0 = below zone 1
	High    float64 `json:"high,omitempty"` // upper boundary (bpm or W); 0 for the top zone
	Seconds float64 `json:"seconds"`
}

// Best is the best effort over a distance (running) or duration (power).
type Best struct {
	Kind      string  `json:"kind"` // "distance" or "power"
	Label     string  `json:"label"`
	DistanceM float64 `json:"distance_m,omitempty"`
	Seconds   float64 `json:"seconds"`         // time for the distance, or the duration
	Watts     float64 `json:"watts,omitempty"` // average power over the duration
	AvgHR     float64 `json:"avg_hr_bpm,omitempty"`
	StartS    float64 `json:"start_offset_s"`
}

// Decoupling compares output per heartbeat in the first and second half of
// a steady endurance session. Below about 5 % suggests good aerobic
// endurance at that intensity.
type Decoupling struct {
	Percent      float64 `json:"percent"`
	Basis        string  `json:"basis"` // "pace" or "power"
	FirstHalfEF  float64 `json:"first_half_ef"`
	SecondHalfEF float64 `json:"second_half_ef"`
}

// HRVSummary is computed from beat-to-beat intervals recorded during the
// activity (only when HRV logging is enabled on the watch).
type HRVSummary struct {
	Beats    int     `json:"beats"`
	RMSSDms  float64 `json:"rmssd_ms"`
	SDNNms   float64 `json:"sdnn_ms"`
	MeanRRms float64 `json:"mean_rr_ms"`
}

// Athlete holds the physiological settings the watch used for this activity.
type Athlete struct {
	MaxHR       float64 `json:"max_hr_bpm,omitempty"`
	RestingHR   float64 `json:"resting_hr_bpm,omitempty"`
	ThresholdHR float64 `json:"threshold_hr_bpm,omitempty"`
	FTP         float64 `json:"ftp_w,omitempty"`
	WeightKg    float64 `json:"weight_kg,omitempty"`
	Female      bool    `json:"female,omitempty"`
}

// Stream is per-second data at the recording rate of the watch. Missing
// values are NaN. T is seconds since Activity.Start.
type Stream struct {
	T        []float64
	HR       []float64
	Speed    []float64 // m/s
	Distance []float64 // m
	Power    []float64 // W
	Cadence  []float64 // steps/min (running) or rpm (cycling)
	Altitude []float64 // m
	// Pauses are the stretches [from, to] (seconds since start) with the
	// timer stopped.
	Pauses [][2]float64
}

// Len returns the number of samples.
func (s *Stream) Len() int { return len(s.T) }
