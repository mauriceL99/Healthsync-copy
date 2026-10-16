package agent

import (
	"fmt"
	"strings"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
)

// metricPlaces chooses display precision per unit.
func metricPlaces(unit string) int {
	switch unit {
	case "steps", "floors", "m", "kcal", "min", "ml", "mg", "bpm", "h", "mmHg", "mg/dL":
		return 0
	case "°C":
		return 2
	}
	return 1
}

func trendArrow(s MetricStat) string {
	if s.ChangePct == nil {
		return ""
	}
	arrow := map[string]string{"up": "↑", "down": "↓", "flat": "→"}[s.Trend]
	return fmt.Sprintf("%s %s%%", arrow, signed(*s.ChangePct, 0))
}

// RenderText renders a report for terminals.
func RenderText(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HealthBridge summary: %s (%s → %s, %d days, %d with data)\n\n", r.Period, r.From, r.To, r.Days, r.DaysWithData)
	b.WriteString("Highlights\n")
	for _, h := range r.Highlights {
		fmt.Fprintf(&b, "  • %s\n", h)
	}
	for _, group := range healthdata.Groups {
		rows := groupRows(r, group)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n", healthdata.GroupLabels[group])
		for _, m := range rows {
			p := metricPlaces(m.Unit)
			line := fmt.Sprintf("  %-32s avg %s %s", m.Label, formatNumber(m.Average, p), m.Unit)
			if m.Total != nil && r.Days > 1 {
				line += fmt.Sprintf(" · total %s", formatNumber(*m.Total, p))
			}
			if m.DaysWithData > 1 {
				line += fmt.Sprintf(" · range %s–%s", formatNumber(m.Min, p), formatNumber(m.Max, p))
			}
			if t := trendArrow(m); t != "" {
				line += " · " + t
			}
			b.WriteString(line + "\n")
		}
	}
	if r.Sleep != nil {
		s := r.Sleep
		line := fmt.Sprintf("  %d nights · avg asleep %s", s.Nights, formatDuration(s.AvgAsleepMin))
		for _, stage := range []struct {
			name string
			v    *float64
		}{{"deep", s.AvgDeepMin}, {"REM", s.AvgREMMin}, {"core", s.AvgCoreMin}, {"awake", s.AvgAwakeMin}} {
			if stage.v != nil {
				line += fmt.Sprintf(" · %s %s", stage.name, formatDuration(*stage.v))
			}
		}
		fmt.Fprintf(&b, "\nSleep nights\n%s\n", line)
		if s.AvgBedtime != "" && s.AvgWakeTime != "" {
			fmt.Fprintf(&b, "  typical bedtime %s · wake-up %s · %d nights under 7 h\n", s.AvgBedtime, s.AvgWakeTime, s.NightsUnder7h)
		}
	}
	w := r.Workouts
	fmt.Fprintf(&b, "\nWorkouts\n  %d workouts · %s · %s kcal · %s km\n", w.Count, formatDuration(w.Minutes), formatNumber(w.EnergyKcal, 0), formatNumber(w.DistanceKm, 1))
	for _, t := range w.ByType {
		line := fmt.Sprintf("  - %-30s ×%d · %s", workoutLabel(t.Type), t.Count, formatDuration(t.Minutes))
		if t.DistanceKm > 0 {
			line += fmt.Sprintf(" · %s km", formatNumber(t.DistanceKm, 1))
		}
		if t.AvgHR != nil {
			line += fmt.Sprintf(" · avg HR %s bpm", formatNumber(*t.AvgHR, 0))
		}
		b.WriteString(line + "\n")
	}
	if len(w.ZoneMinutes) == healthdata.HeartRateZones {
		fmt.Fprintf(&b, "  heart-rate zones (min): Z1 %s · Z2 %s · Z3 %s · Z4 %s · Z5 %s\n",
			formatNumber(w.ZoneMinutes[0], 0), formatNumber(w.ZoneMinutes[1], 0), formatNumber(w.ZoneMinutes[2], 0), formatNumber(w.ZoneMinutes[3], 0), formatNumber(w.ZoneMinutes[4], 0))
	}
	fmt.Fprintf(&b, "\n%s\n", r.Notice)
	return b.String()
}

// RenderMarkdown renders a report as Markdown (for chats, notes and agents).
func RenderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Health summary: %s (%s → %s)\n\n", r.Period, r.From, r.To)
	fmt.Fprintf(&b, "_%d days, %d with data · time zone %s_\n\n", r.Days, r.DaysWithData, r.Timezone)
	for _, h := range r.Highlights {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	for _, group := range healthdata.Groups {
		rows := groupRows(r, group)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n| Metric | Avg | Min | Max | Latest | vs. previous |\n| --- | ---: | ---: | ---: | ---: | ---: |\n", healthdata.GroupLabels[group])
		for _, m := range rows {
			p := metricPlaces(m.Unit)
			unit := ""
			if m.Unit != "" {
				unit = " " + m.Unit
			}
			fmt.Fprintf(&b, "| %s | %s%s | %s | %s | %s | %s |\n", m.Label, formatNumber(m.Average, p), unit,
				formatNumber(m.Min, p), formatNumber(m.Max, p), formatNumber(m.Latest, p), firstNonEmpty(trendArrow(m), "–"))
		}
	}
	if w := r.Workouts; w.Count > 0 {
		fmt.Fprintf(&b, "\n### Workouts\n\n| Type | Count | Time | Distance | Avg HR |\n| --- | ---: | ---: | ---: | ---: |\n")
		for _, t := range w.ByType {
			hr := "–"
			if t.AvgHR != nil {
				hr = formatNumber(*t.AvgHR, 0) + " bpm"
			}
			dist := "–"
			if t.DistanceKm > 0 {
				dist = formatNumber(t.DistanceKm, 1) + " km"
			}
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", workoutLabel(t.Type), t.Count, formatDuration(t.Minutes), dist, hr)
		}
	}
	fmt.Fprintf(&b, "\n> %s\n", r.Notice)
	return b.String()
}

// groupRows returns the metrics of one group, without activity goals (they
// stay in JSON output but add noise to human-readable reports).
func groupRows(r Report, group string) []MetricStat {
	var rows []MetricStat
	for _, m := range r.Metrics {
		if m.Group == group && !strings.Contains(m.Metric, "_goal_") {
			rows = append(rows, m)
		}
	}
	return rows
}

// workoutLabel turns a schema workout type (snake_case) into display text.
func workoutLabel(workoutType string) string { return strings.ReplaceAll(workoutType, "_", " ") }
