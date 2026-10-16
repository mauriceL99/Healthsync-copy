// Package healthdata defines the HealthBatch schema (docs/DATA_SCHEMA.md), its
// strict sanitizer and a synthetic data generator for tests and demos.
package healthdata

import "sort"

// Aggregation describes how the device aggregates a metric per day and how
// summaries combine several days.
type Aggregation string

const (
	Sum     Aggregation = "sum"
	Avg     Aggregation = "avg"
	Min     Aggregation = "min"
	Max     Aggregation = "max"
	Latest  Aggregation = "latest"
	Summary Aggregation = "summary"
)

// Metric is one numeric day metric.
type Metric struct {
	Group     string
	Key       string
	Label     string
	Unit      string
	Agg       Aggregation
	Min, Max  float64
	HealthKit string
}

// Path returns "group.key".
func (m Metric) Path() string { return m.Group + "." + m.Key }

// Cumulative reports whether the daily value is a total (summed across days).
func (m Metric) Cumulative() bool { return m.Agg == Sum }

// Groups lists the metric groups in display order.
var Groups = []string{"activity", "heart", "sleep", "body", "vitals", "nutrition", "mindfulness"}

// GroupLabels are human-readable group titles.
var GroupLabels = map[string]string{
	"activity": "Activity", "heart": "Heart", "sleep": "Sleep", "body": "Body",
	"vitals": "Vitals", "nutrition": "Nutrition", "mindfulness": "Mindfulness",
}

// Catalog lists every numeric day metric accepted by the sanitizer.
var Catalog = []Metric{
	{"activity", "steps", "Steps", "steps", Sum, 0, 200000, "stepCount"},
	{"activity", "distance_walking_running_m", "Walking + running distance", "m", Sum, 0, 300000, "distanceWalkingRunning"},
	{"activity", "distance_cycling_m", "Cycling distance", "m", Sum, 0, 1000000, "distanceCycling"},
	{"activity", "distance_swimming_m", "Swimming distance", "m", Sum, 0, 100000, "distanceSwimming"},
	{"activity", "active_energy_kcal", "Active energy", "kcal", Sum, 0, 20000, "activeEnergyBurned"},
	{"activity", "basal_energy_kcal", "Resting energy", "kcal", Sum, 0, 10000, "basalEnergyBurned"},
	{"activity", "flights_climbed", "Flights climbed", "floors", Sum, 0, 2000, "flightsClimbed"},
	{"activity", "exercise_min", "Exercise", "min", Sum, 0, 1440, "appleExerciseTime"},
	{"activity", "stand_min", "Stand time", "min", Sum, 0, 1440, "appleStandTime"},
	{"activity", "stand_hours", "Stand hours", "h", Summary, 0, 24, "HKActivitySummary.appleStandHours"},
	{"activity", "move_goal_kcal", "Move goal", "kcal", Summary, 0, 20000, "HKActivitySummary.activeEnergyBurnedGoal"},
	{"activity", "exercise_goal_min", "Exercise goal", "min", Summary, 0, 1440, "HKActivitySummary.appleExerciseTimeGoal"},
	{"activity", "stand_goal_hours", "Stand goal", "h", Summary, 0, 24, "HKActivitySummary.appleStandHoursGoal"},

	{"heart", "resting_bpm", "Resting heart rate", "bpm", Avg, 20, 250, "restingHeartRate"},
	{"heart", "walking_avg_bpm", "Walking heart rate", "bpm", Avg, 20, 250, "walkingHeartRateAverage"},
	{"heart", "avg_bpm", "Average heart rate", "bpm", Avg, MinBPM, MaxBPM, "heartRate"},
	{"heart", "min_bpm", "Lowest heart rate", "bpm", Min, MinBPM, MaxBPM, "heartRate"},
	{"heart", "max_bpm", "Highest heart rate", "bpm", Max, MinBPM, MaxBPM, "heartRate"},
	{"heart", "hrv_sdnn_ms", "Heart rate variability (SDNN)", "ms", Avg, 0, 500, "heartRateVariabilitySDNN"},
	{"heart", "vo2max_ml_kg_min", "VO2 max", "ml/kg/min", Latest, 5, 100, "vo2Max"},
	{"heart", "recovery_1min_bpm", "Heart rate recovery (1 min)", "bpm", Avg, 0, 150, "heartRateRecoveryOneMinute"},

	{"sleep", "in_bed_min", "Time in bed", "min", Sum, 0, 1440, "sleepAnalysis.inBed"},
	{"sleep", "asleep_min", "Time asleep", "min", Sum, 0, 1440, "sleepAnalysis.asleep*"},
	{"sleep", "core_min", "Core sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepCore"},
	{"sleep", "deep_min", "Deep sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepDeep"},
	{"sleep", "rem_min", "REM sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepREM"},
	{"sleep", "awake_min", "Awake during sleep", "min", Sum, 0, 1440, "sleepAnalysis.awake"},
	{"sleep", "unspecified_min", "Unspecified sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepUnspecified"},

	{"body", "weight_kg", "Weight", "kg", Latest, 1, 500, "bodyMass"},
	{"body", "body_fat_pct", "Body fat", "%", Latest, 1, 80, "bodyFatPercentage"},
	{"body", "bmi", "BMI", "", Latest, 5, 100, "bodyMassIndex"},
	{"body", "lean_body_mass_kg", "Lean body mass", "kg", Latest, 1, 300, "leanBodyMass"},
	{"body", "height_cm", "Height", "cm", Latest, 30, 260, "height"},
	{"body", "waist_cm", "Waist circumference", "cm", Latest, 20, 300, "waistCircumference"},

	{"vitals", "respiratory_rate_bpm", "Respiratory rate", "breaths/min", Avg, 2, 80, "respiratoryRate"},
	{"vitals", "oxygen_saturation_pct", "Blood oxygen", "%", Avg, 50, 100, "oxygenSaturation"},
	{"vitals", "body_temperature_c", "Body temperature", "°C", Avg, 30, 45, "bodyTemperature"},
	{"vitals", "sleeping_wrist_temperature_c", "Sleeping wrist temperature", "°C", Avg, 25, 45, "appleSleepingWristTemperature"},
	{"vitals", "blood_pressure_systolic_mmhg", "Blood pressure (systolic)", "mmHg", Avg, 40, 300, "bloodPressureSystolic"},
	{"vitals", "blood_pressure_diastolic_mmhg", "Blood pressure (diastolic)", "mmHg", Avg, 20, 200, "bloodPressureDiastolic"},
	{"vitals", "blood_glucose_mg_dl", "Blood glucose", "mg/dL", Avg, 10, 1000, "bloodGlucose"},

	{"nutrition", "energy_kcal", "Dietary energy", "kcal", Sum, 0, 20000, "dietaryEnergyConsumed"},
	{"nutrition", "protein_g", "Protein", "g", Sum, 0, 1000, "dietaryProtein"},
	{"nutrition", "carbohydrates_g", "Carbohydrates", "g", Sum, 0, 2000, "dietaryCarbohydrates"},
	{"nutrition", "fat_g", "Fat", "g", Sum, 0, 1000, "dietaryFatTotal"},
	{"nutrition", "fiber_g", "Fiber", "g", Sum, 0, 500, "dietaryFiber"},
	{"nutrition", "sugar_g", "Sugar", "g", Sum, 0, 1000, "dietarySugar"},
	{"nutrition", "water_ml", "Water", "ml", Sum, 0, 20000, "dietaryWater"},
	{"nutrition", "caffeine_mg", "Caffeine", "mg", Sum, 0, 5000, "dietaryCaffeine"},

	{"mindfulness", "mindful_min", "Mindful minutes", "min", Sum, 0, 1440, "mindfulSession"},
}

var byPath = func() map[string]Metric {
	m := map[string]Metric{}
	for _, metric := range Catalog {
		m[metric.Path()] = metric
	}
	return m
}()

// Lookup finds a metric by "group.key".
func Lookup(path string) (Metric, bool) {
	m, ok := byPath[path]
	return m, ok
}

// Paths returns all metric paths sorted alphabetically.
func Paths() []string {
	out := make([]string, 0, len(byPath))
	for p := range byPath {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
