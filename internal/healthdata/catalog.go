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
	// HigherIsBetter is used for neutral trend wording; nil means no preference.
	HigherIsBetter *bool
}

// Path returns "group.key".
func (m Metric) Path() string { return m.Group + "." + m.Key }

// Cumulative reports whether the daily value is a total (summed across days).
func (m Metric) Cumulative() bool { return m.Agg == Sum }

var (
	yes = true
	no  = false
)

// Groups lists the metric groups in display order.
var Groups = []string{"activity", "heart", "sleep", "body", "vitals", "nutrition", "mindfulness"}

// GroupLabels are human-readable group titles.
var GroupLabels = map[string]string{
	"activity": "Activity", "heart": "Heart", "sleep": "Sleep", "body": "Body",
	"vitals": "Vitals", "nutrition": "Nutrition", "mindfulness": "Mindfulness",
}

// Catalog lists every numeric day metric accepted by the sanitizer.
var Catalog = []Metric{
	{"activity", "steps", "Steps", "steps", Sum, 0, 200000, "stepCount", &yes},
	{"activity", "distance_walking_running_m", "Walking + running distance", "m", Sum, 0, 300000, "distanceWalkingRunning", &yes},
	{"activity", "distance_cycling_m", "Cycling distance", "m", Sum, 0, 1000000, "distanceCycling", nil},
	{"activity", "distance_swimming_m", "Swimming distance", "m", Sum, 0, 100000, "distanceSwimming", nil},
	{"activity", "active_energy_kcal", "Active energy", "kcal", Sum, 0, 20000, "activeEnergyBurned", &yes},
	{"activity", "basal_energy_kcal", "Resting energy", "kcal", Sum, 0, 10000, "basalEnergyBurned", nil},
	{"activity", "flights_climbed", "Flights climbed", "floors", Sum, 0, 2000, "flightsClimbed", &yes},
	{"activity", "exercise_min", "Exercise", "min", Sum, 0, 1440, "appleExerciseTime", &yes},
	{"activity", "stand_min", "Stand time", "min", Sum, 0, 1440, "appleStandTime", &yes},
	{"activity", "stand_hours", "Stand hours", "h", Summary, 0, 24, "HKActivitySummary.appleStandHours", &yes},
	{"activity", "move_goal_kcal", "Move goal", "kcal", Summary, 0, 20000, "HKActivitySummary.activeEnergyBurnedGoal", nil},
	{"activity", "exercise_goal_min", "Exercise goal", "min", Summary, 0, 1440, "HKActivitySummary.appleExerciseTimeGoal", nil},
	{"activity", "stand_goal_hours", "Stand goal", "h", Summary, 0, 24, "HKActivitySummary.appleStandHoursGoal", nil},

	{"heart", "resting_bpm", "Resting heart rate", "bpm", Avg, 20, 250, "restingHeartRate", &no},
	{"heart", "walking_avg_bpm", "Walking heart rate", "bpm", Avg, 20, 250, "walkingHeartRateAverage", &no},
	{"heart", "avg_bpm", "Average heart rate", "bpm", Avg, 20, 260, "heartRate", nil},
	{"heart", "min_bpm", "Lowest heart rate", "bpm", Min, 20, 260, "heartRate", nil},
	{"heart", "max_bpm", "Highest heart rate", "bpm", Max, 20, 260, "heartRate", nil},
	{"heart", "hrv_sdnn_ms", "Heart rate variability (SDNN)", "ms", Avg, 0, 500, "heartRateVariabilitySDNN", &yes},
	{"heart", "vo2max_ml_kg_min", "VO2 max", "ml/kg/min", Latest, 5, 100, "vo2Max", &yes},
	{"heart", "recovery_1min_bpm", "Heart rate recovery (1 min)", "bpm", Avg, 0, 150, "heartRateRecoveryOneMinute", &yes},

	{"sleep", "in_bed_min", "Time in bed", "min", Sum, 0, 1440, "sleepAnalysis.inBed", nil},
	{"sleep", "asleep_min", "Time asleep", "min", Sum, 0, 1440, "sleepAnalysis.asleep*", &yes},
	{"sleep", "core_min", "Core sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepCore", nil},
	{"sleep", "deep_min", "Deep sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepDeep", &yes},
	{"sleep", "rem_min", "REM sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepREM", &yes},
	{"sleep", "awake_min", "Awake during sleep", "min", Sum, 0, 1440, "sleepAnalysis.awake", &no},
	{"sleep", "unspecified_min", "Unspecified sleep", "min", Sum, 0, 1440, "sleepAnalysis.asleepUnspecified", nil},

	{"body", "weight_kg", "Weight", "kg", Latest, 1, 500, "bodyMass", nil},
	{"body", "body_fat_pct", "Body fat", "%", Latest, 1, 80, "bodyFatPercentage", nil},
	{"body", "bmi", "BMI", "", Latest, 5, 100, "bodyMassIndex", nil},
	{"body", "lean_body_mass_kg", "Lean body mass", "kg", Latest, 1, 300, "leanBodyMass", nil},
	{"body", "height_cm", "Height", "cm", Latest, 30, 260, "height", nil},
	{"body", "waist_cm", "Waist circumference", "cm", Latest, 20, 300, "waistCircumference", nil},

	{"vitals", "respiratory_rate_bpm", "Respiratory rate", "breaths/min", Avg, 2, 80, "respiratoryRate", nil},
	{"vitals", "oxygen_saturation_pct", "Blood oxygen", "%", Avg, 50, 100, "oxygenSaturation", nil},
	{"vitals", "body_temperature_c", "Body temperature", "°C", Avg, 30, 45, "bodyTemperature", nil},
	{"vitals", "sleeping_wrist_temperature_c", "Sleeping wrist temperature", "°C", Avg, 25, 45, "appleSleepingWristTemperature", nil},
	{"vitals", "blood_pressure_systolic_mmhg", "Blood pressure (systolic)", "mmHg", Avg, 40, 300, "bloodPressureSystolic", nil},
	{"vitals", "blood_pressure_diastolic_mmhg", "Blood pressure (diastolic)", "mmHg", Avg, 20, 200, "bloodPressureDiastolic", nil},
	{"vitals", "blood_glucose_mg_dl", "Blood glucose", "mg/dL", Avg, 10, 1000, "bloodGlucose", nil},

	{"nutrition", "energy_kcal", "Dietary energy", "kcal", Sum, 0, 20000, "dietaryEnergyConsumed", nil},
	{"nutrition", "protein_g", "Protein", "g", Sum, 0, 1000, "dietaryProtein", nil},
	{"nutrition", "carbohydrates_g", "Carbohydrates", "g", Sum, 0, 2000, "dietaryCarbohydrates", nil},
	{"nutrition", "fat_g", "Fat", "g", Sum, 0, 1000, "dietaryFatTotal", nil},
	{"nutrition", "fiber_g", "Fiber", "g", Sum, 0, 500, "dietaryFiber", nil},
	{"nutrition", "sugar_g", "Sugar", "g", Sum, 0, 1000, "dietarySugar", nil},
	{"nutrition", "water_ml", "Water", "ml", Sum, 0, 20000, "dietaryWater", nil},
	{"nutrition", "caffeine_mg", "Caffeine", "mg", Sum, 0, 5000, "dietaryCaffeine", nil},

	{"mindfulness", "mindful_min", "Mindful minutes", "min", Sum, 0, 1440, "mindfulSession", &yes},
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
