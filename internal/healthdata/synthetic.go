package healthdata

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Synthetic generates a plausible, clearly fake HealthBatch covering the
// `days` local calendar days that end with `end`. It is used by
// `healthbridge dev simulate-device` and tests; it never touches real data.
func Synthetic(end time.Time, days int, loc *time.Location, seed int64) map[string]any {
	r := rand.New(rand.NewSource(seed))
	end = end.In(loc)
	dayMap := map[string]any{}
	var workouts []any
	weight := 78 + r.Float64()*4
	vo2 := 42 + r.Float64()*4
	for i := days - 1; i >= 0; i-- {
		d := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -i)
		date := d.Format("2006-01-02")
		weekend := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
		steps := 6500 + r.NormFloat64()*2200
		if weekend {
			steps += 1500
		}
		steps = math.Max(800, steps)
		active := 380 + steps*0.03 + r.NormFloat64()*60
		asleep := 400 + r.NormFloat64()*35
		deep := asleep * (0.13 + r.Float64()*0.05)
		rem := asleep * (0.2 + r.Float64()*0.05)
		core := asleep - deep - rem
		awake := 10 + r.Float64()*25
		wake := time.Date(d.Year(), d.Month(), d.Day(), 6, 30, 0, 0, loc).Add(time.Duration(r.NormFloat64()*25) * time.Minute)
		bed := wake.Add(-time.Duration(asleep+awake) * time.Minute)
		weight += r.NormFloat64() * 0.15
		vo2 += r.NormFloat64() * 0.05
		day := map[string]any{
			"activity": map[string]any{
				"steps":                      math.Round(steps),
				"distance_walking_running_m": math.Round(steps * 0.76),
				"active_energy_kcal":         round(active, 1),
				"basal_energy_kcal":          round(1680+r.NormFloat64()*20, 1),
				"flights_climbed":            math.Round(math.Max(0, 8+r.NormFloat64()*5)),
				"exercise_min":               math.Round(math.Max(0, 28+r.NormFloat64()*15)),
				"stand_min":                  math.Round(math.Max(0, 120+r.NormFloat64()*30)),
				"stand_hours":                math.Round(math.Min(16, math.Max(4, 11+r.NormFloat64()*2))),
				"move_goal_kcal":             600,
				"exercise_goal_min":          30,
				"stand_goal_hours":           12,
			},
			"heart": map[string]any{
				"resting_bpm":      round(56+r.NormFloat64()*2.5, 1),
				"walking_avg_bpm":  round(95+r.NormFloat64()*4, 1),
				"avg_bpm":          round(72+r.NormFloat64()*4, 1),
				"min_bpm":          math.Round(48 + r.Float64()*6),
				"max_bpm":          math.Round(130 + r.Float64()*45),
				"hrv_sdnn_ms":      round(48+r.NormFloat64()*8, 1),
				"vo2max_ml_kg_min": round(vo2, 1),
			},
			"sleep": map[string]any{
				"in_bed_min": round(asleep+awake+8, 1), "asleep_min": round(asleep, 1),
				"core_min": round(core, 1), "deep_min": round(deep, 1), "rem_min": round(rem, 1), "awake_min": round(awake, 1),
				"bedtime":   bed.UTC().Format(time.RFC3339),
				"wake_time": wake.UTC().Format(time.RFC3339),
				"sessions":  syntheticSessions(bed, deep, core, rem, awake),
			},
			"body":        map[string]any{"weight_kg": round(weight, 1), "body_fat_pct": round(18+r.NormFloat64()*0.3, 1)},
			"vitals":      map[string]any{"respiratory_rate_bpm": round(14+r.NormFloat64()*0.6, 1), "oxygen_saturation_pct": round(97+r.NormFloat64()*0.8, 1), "sleeping_wrist_temperature_c": round(35.6+r.NormFloat64()*0.2, 2)},
			"nutrition":   map[string]any{"water_ml": math.Round(1800 + r.Float64()*800), "caffeine_mg": math.Round(r.Float64() * 240)},
			"mindfulness": map[string]any{"mindful_min": math.Round(r.Float64() * 15)},
		}
		dayMap[date] = day
		if i%2 == 0 || weekend {
			workouts = append(workouts, syntheticWorkout(r, d, i, weekend))
		}
	}
	return map[string]any{
		"schema":       1,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"timezone":     loc.String(),
		"source":       map[string]any{"app_version": "simulator", "os_version": "synthetic", "device_model": "Simulator1,1"},
		"days":         dayMap,
		"workouts":     workouts,
	}
}

func syntheticSessions(bed time.Time, deep, core, rem, awake float64) []any {
	var out []any
	t := bed
	add := func(stage string, minutes float64) {
		end := t.Add(time.Duration(minutes * float64(time.Minute)))
		out = append(out, map[string]any{"stage": stage, "start": t.UTC().Format(time.RFC3339), "end": end.UTC().Format(time.RFC3339)})
		t = end
	}
	for cycle := 0; cycle < 4; cycle++ {
		add("core", core/8)
		add("deep", deep/4)
		add("core", core/8)
		add("rem", rem/4)
		if cycle == 1 {
			add("awake", awake)
		}
	}
	return out
}

func syntheticWorkout(r *rand.Rand, day time.Time, i int, weekend bool) map[string]any {
	kinds := []struct {
		name     string
		speed    float64 // m/s, 0 for stationary
		avgBPM   float64
		indoor   bool
		minutes  float64
		kcalRate float64
	}{
		{"running", 2.9, 152, false, 42, 11},
		{"cycling", 7.2, 138, false, 65, 9},
		{"traditional_strength_training", 0, 118, true, 50, 6},
		{"walking", 1.4, 102, false, 55, 4.5},
		{"yoga", 0, 92, true, 35, 3},
	}
	k := kinds[(i+r.Intn(2))%len(kinds)]
	if weekend {
		k = kinds[1]
	}
	minutes := k.minutes + r.NormFloat64()*8
	start := time.Date(day.Year(), day.Month(), day.Day(), 17, 30, 0, 0, day.Location()).Add(time.Duration(r.Intn(60)) * time.Minute)
	end := start.Add(time.Duration(minutes * float64(time.Minute)))
	maxRef := 190.0
	var samples []any
	zones := make([]float64, 5)
	minB, maxB, sum := 300.0, 0.0, 0.0
	n := 0
	for t := 0.0; t <= minutes*60; t += 5 {
		warm := math.Min(1, t/300)
		bpm := 85 + (k.avgBPM-85)*warm + 6*math.Sin(t/90) + r.NormFloat64()*3
		bpm = math.Round(bpm)
		samples = append(samples, map[string]any{"t": t, "bpm": bpm})
		pct := bpm / maxRef
		zone := int(math.Floor((pct - 0.5) / 0.1))
		if zone >= 0 {
			zones[min(zone, 4)] += 5
		}
		minB, maxB, sum = math.Min(minB, bpm), math.Max(maxB, bpm), sum+bpm
		n++
	}
	var zoneList []any
	for z := 0; z < 5; z++ {
		lo := maxRef * (0.5 + 0.1*float64(z))
		zoneList = append(zoneList, map[string]any{"zone": z + 1, "lower_bpm": math.Round(lo), "upper_bpm": math.Round(lo + maxRef*0.1), "seconds": zones[z]})
	}
	w := map[string]any{
		"id":          fmt.Sprintf("00000000-0000-4000-8000-%012d", day.Year()*100000000+int(day.Month())*1000000+day.Day()*10000+i),
		"type":        k.name,
		"start":       start.UTC().Format(time.RFC3339),
		"end":         end.UTC().Format(time.RFC3339),
		"duration_s":  math.Round(minutes * 60),
		"energy_kcal": round(minutes*k.kcalRate, 1),
		"indoor":      k.indoor,
		"source":      "HealthBridge Simulator",
		"heart_rate": map[string]any{
			"avg_bpm": round(sum/float64(n), 1), "min_bpm": minB, "max_bpm": maxB,
			"recovery_1min_bpm": math.Round(22 + r.Float64()*15), "max_reference_bpm": maxRef,
			"zones": zoneList, "samples": samples,
		},
	}
	if k.speed > 0 {
		w["distance_m"] = math.Round(k.speed * minutes * 60)
		w["elevation_gain_m"] = math.Round(r.Float64() * 120)
	}
	return w
}
