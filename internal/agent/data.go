package agent

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
)

// SeriesPoint is one day of a metric series.
type SeriesPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// Series is the output of `healthbridge series`.
type Series struct {
	Metric string        `json:"metric"`
	Label  string        `json:"label"`
	Unit   string        `json:"unit"`
	From   string        `json:"from"`
	To     string        `json:"to"`
	Points []SeriesPoint `json:"points"`
	Notice string        `json:"notice"`
}

// ResolveMetric accepts "group.key", "key" or a case-insensitive label.
func ResolveMetric(name string) (healthdata.Metric, error) {
	name = strings.TrimSpace(name)
	if m, ok := healthdata.Lookup(name); ok {
		return m, nil
	}
	var matches []healthdata.Metric
	for _, m := range healthdata.Catalog {
		if m.Key == name || strings.EqualFold(m.Label, name) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return healthdata.Metric{}, fmt.Errorf("unknown metric %q; run `healthbridge metrics` to list metrics", name)
}

// MetricSeries returns daily values of one metric inside the window.
func MetricSeries(ctx context.Context, db *DB, m healthdata.Metric, w Window) (Series, error) {
	days, err := db.Days(ctx, w.FromString(), w.ToString())
	if err != nil {
		return Series{}, err
	}
	s := Series{Metric: m.Path(), Label: m.Label, Unit: m.Unit, From: w.FromString(), To: w.ToString(), Points: []SeriesPoint{}, Notice: Notice}
	for _, date := range SortedDates(days) {
		if v, ok := days[date].Number(m); ok {
			s.Points = append(s.Points, SeriesPoint{date, v})
		}
	}
	return s, nil
}

// RenderSeries renders a series as an aligned table with a text bar.
func RenderSeries(s Series) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) %s → %s\n", s.Label, s.Unit, s.From, s.To)
	maxV := 0.0
	for _, p := range s.Points {
		maxV = math.Max(maxV, p.Value)
	}
	for _, p := range s.Points {
		bar := 0
		if maxV > 0 {
			bar = int(math.Round(p.Value / maxV * 30))
		}
		fmt.Fprintf(&b, "  %s  %10s  %s\n", p.Date, formatNumber(p.Value, metricPlaces(s.Unit)), strings.Repeat("▇", bar))
	}
	if len(s.Points) == 0 {
		b.WriteString("  no data in this window\n")
	}
	return b.String()
}

// WorkoutList is the output of `healthbridge workouts`.
type WorkoutList struct {
	Timezone string               `json:"timezone"`
	From     string               `json:"from"`
	To       string               `json:"to"`
	Workouts []healthdata.Workout `json:"workouts"`
	Notice   string               `json:"notice"`
}

// ListWorkouts loads workouts; heart-rate samples are dropped unless requested.
func ListWorkouts(ctx context.Context, db *DB, w Window, samples bool) (WorkoutList, error) {
	ws, err := db.Workouts(ctx, w.FromString(), w.ToString())
	if err != nil {
		return WorkoutList{}, err
	}
	for i := range ws {
		ws[i].Source = "" // free text is never handed to agents
		if !samples && ws[i].HeartRate != nil {
			ws[i].HeartRate.Samples = nil
		}
	}
	if ws == nil {
		ws = []healthdata.Workout{}
	}
	loc, err := db.Location(ctx)
	if err != nil {
		return WorkoutList{}, err
	}
	return WorkoutList{Timezone: loc.String(), From: w.FromString(), To: w.ToString(), Workouts: ws, Notice: Notice}, nil
}

// RenderWorkouts renders workouts as a table.
func RenderWorkouts(l WorkoutList) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workouts %s → %s (%s)\n", l.From, l.To, l.Timezone)
	loc, err := time.LoadLocation(l.Timezone)
	if err != nil {
		loc = time.UTC
	}
	for _, w := range l.Workouts {
		line := fmt.Sprintf("  %s  %-30s %s", w.StartTime().In(loc).Format("Mon 2006-01-02 15:04"), workoutLabel(w.Type), formatDuration(workoutMinutes(w)))
		if w.DistanceM != nil && *w.DistanceM > 0 {
			line += fmt.Sprintf(" · %s km", formatNumber(*w.DistanceM/1000, 2))
		}
		if w.EnergyKcal != nil {
			line += fmt.Sprintf(" · %s kcal", formatNumber(*w.EnergyKcal, 0))
		}
		if hr := w.HeartRate; hr != nil && hr.AvgBPM != nil {
			line += fmt.Sprintf(" · HR avg %s", formatNumber(*hr.AvgBPM, 0))
			if hr.MaxBPM != nil {
				line += fmt.Sprintf(" max %s", formatNumber(*hr.MaxBPM, 0))
			}
			if hr.Recovery1MinBPM != nil {
				line += fmt.Sprintf(" · recovery −%s bpm/1 min", formatNumber(*hr.Recovery1MinBPM, 0))
			}
		}
		b.WriteString(line + "\n")
	}
	if len(l.Workouts) == 0 {
		b.WriteString("  no workouts in this window\n")
	}
	return b.String()
}

// Export renders days as NDJSON or CSV (one row per day).
func Export(ctx context.Context, db *DB, w Window, format string) ([]byte, error) {
	days, err := db.Days(ctx, w.FromString(), w.ToString())
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	switch format {
	case "ndjson", "jsonl":
		enc := json.NewEncoder(&buf)
		for _, date := range SortedDates(days) {
			if err := enc.Encode(map[string]any{"date": date, "data": days[date]}); err != nil {
				return nil, err
			}
		}
	case "csv":
		cw := csv.NewWriter(&buf)
		header := []string{"date"}
		for _, m := range healthdata.Catalog {
			header = append(header, m.Path())
		}
		if err := cw.Write(header); err != nil {
			return nil, err
		}
		for _, date := range SortedDates(days) {
			row := []string{date}
			for _, m := range healthdata.Catalog {
				cell := ""
				if v, ok := days[date].Number(m); ok {
					cell = strconv.FormatFloat(v, 'f', -1, 64)
				}
				row = append(row, cell)
			}
			if err := cw.Write(row); err != nil {
				return nil, err
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown export format %q (use csv or ndjson)", format)
	}
	return buf.Bytes(), nil
}
