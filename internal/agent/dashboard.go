package agent

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
)

type chartSeries struct {
	Name   string
	Class  string
	Values []float64 // NaN = missing
}

type tile struct {
	Label, Value, Sub, Trend string
}

type dashboardData struct {
	Report   Report
	Tiles    []tile
	Charts   []template.HTML
	Workouts []workoutRow
}

type workoutRow struct {
	Date, Type, Duration, Distance, HR string
	Zones                              template.HTML
}

// Dashboard loads the window from db and renders a self-contained HTML
// dashboard (no external resources).
func Dashboard(ctx context.Context, db *DB, w Window, r Report) ([]byte, error) {
	days, err := db.Days(ctx, w.FromString(), w.ToString())
	if err != nil {
		return nil, err
	}
	workouts, err := db.Workouts(ctx, w.FromString(), w.ToString())
	if err != nil {
		return nil, err
	}
	loc, err := db.Location(ctx)
	if err != nil {
		return nil, err
	}
	return renderDashboard(w, r, days, workouts, loc)
}

// renderDashboard turns loaded data into HTML; it does no I/O.
func renderDashboard(w Window, r Report, days map[string]healthdata.Day, workouts []healthdata.Workout, loc *time.Location) ([]byte, error) {
	dates := make([]string, 0, w.Days())
	for d := w.From; !d.After(w.To); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	series := func(group, key string) []float64 {
		out := make([]float64, len(dates))
		for i, date := range dates {
			out[i] = math.NaN()
			if v, ok := days[date].Value(group, key); ok {
				out[i] = v
			}
		}
		return out
	}
	data := dashboardData{Report: r}
	stat := func(path string) *MetricStat {
		for i := range r.Metrics {
			if r.Metrics[i].Metric == path {
				return &r.Metrics[i]
			}
		}
		return nil
	}
	addTile := func(label, path string, places int, unit string) {
		if s := stat(path); s != nil {
			data.Tiles = append(data.Tiles, tile{label, formatNumber(s.Average, places) + unit, "avg per day", trendArrow(*s)})
		}
	}
	addTile("Steps", "activity.steps", 0, "")
	if r.Sleep != nil {
		data.Tiles = append(data.Tiles, tile{"Sleep", formatDuration(r.Sleep.AvgAsleepMin), fmt.Sprintf("%d nights", r.Sleep.Nights), ""})
	}
	addTile("Resting HR", "heart.resting_bpm", 0, " bpm")
	addTile("HRV", "heart.hrv_sdnn_ms", 0, " ms")
	addTile("Active energy", "activity.active_energy_kcal", 0, " kcal")
	data.Tiles = append(data.Tiles, tile{"Workouts", fmt.Sprint(r.Workouts.Count), formatDuration(r.Workouts.Minutes), ""})

	add := func(title, unit string, kind string, s ...chartSeries) {
		for _, one := range s {
			for _, v := range one.Values {
				if !math.IsNaN(v) {
					data.Charts = append(data.Charts, chart(title, unit, kind, dates, s))
					return
				}
			}
		}
	}
	// metric charts one catalog metric; title and unit come from the catalog.
	metric := func(path, kind, class string) {
		m, ok := healthdata.Lookup(path)
		if !ok {
			return
		}
		add(m.Label, m.Unit, kind, chartSeries{m.Label, class, series(m.Group, m.Key)})
	}
	metric("activity.steps", "bar", "c1")
	add("Sleep stages", "min", "stack",
		chartSeries{"Deep", "c3", series("sleep", "deep_min")}, chartSeries{"Core", "c1", series("sleep", "core_min")},
		chartSeries{"REM", "c2", series("sleep", "rem_min")}, chartSeries{"Awake", "c4", series("sleep", "awake_min")})
	metric("heart.resting_bpm", "line", "c4")
	metric("heart.hrv_sdnn_ms", "line", "c2")
	metric("activity.active_energy_kcal", "bar", "c4")
	metric("body.weight_kg", "line", "c3")

	for i := len(workouts) - 1; i >= 0; i-- {
		wk := workouts[i]
		row := workoutRow{Date: wk.StartTime().In(loc).Format("Mon 02 Jan 15:04"), Type: workoutLabel(wk.Type), Duration: formatDuration(workoutMinutes(wk)), Distance: "–", HR: "–"}
		if wk.DistanceM != nil && *wk.DistanceM > 0 {
			row.Distance = formatNumber(*wk.DistanceM/1000, 2) + " km"
		}
		if wk.HeartRate != nil {
			if wk.HeartRate.AvgBPM != nil {
				row.HR = formatNumber(*wk.HeartRate.AvgBPM, 0)
				if wk.HeartRate.MaxBPM != nil {
					row.HR += " / " + formatNumber(*wk.HeartRate.MaxBPM, 0)
				}
				row.HR += " bpm"
			}
			row.Zones = zoneBar(wk.HeartRate.Zones)
		}
		data.Workouts = append(data.Workouts, row)
	}
	var buf bytes.Buffer
	if err := dashboardTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func zoneBar(zones []healthdata.HRZone) template.HTML {
	total := 0.0
	for _, z := range zones {
		total += z.Seconds
	}
	if total == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="zones" role="img" aria-label="time in heart-rate zones">`)
	for _, z := range zones {
		if z.Seconds <= 0 {
			continue
		}
		fmt.Fprintf(&b, `<span class="z%d" style="width:%.2f%%" title="Zone %d: %s"></span>`, z.Zone, z.Seconds/total*100, z.Zone, html.EscapeString(formatDuration(z.Seconds/60)))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// chart draws a minimal SVG bar, stacked bar or line chart.
func chart(title, unit, kind string, dates []string, series []chartSeries) template.HTML {
	const width, height, left, bottom, top = 640.0, 200.0, 44.0, 22.0, 10.0
	plotW, plotH := width-left-8, height-bottom-top
	maxV, minV := 0.0, math.Inf(1)
	for i := range dates {
		stack := 0.0
		for _, s := range series {
			v := s.Values[i]
			if math.IsNaN(v) {
				continue
			}
			if kind == "stack" {
				stack += v
			} else {
				maxV = math.Max(maxV, v)
			}
			minV = math.Min(minV, v)
		}
		maxV = math.Max(maxV, stack)
	}
	lo := 0.0
	if kind == "line" && !math.IsInf(minV, 1) {
		pad := math.Max((maxV-minV)*0.15, 1)
		lo, maxV = math.Max(0, minV-pad), maxV+pad
	}
	if maxV <= lo {
		maxV = lo + 1
	}
	y := func(v float64) float64 { return top + plotH - (v-lo)/(maxV-lo)*plotH }
	n := float64(len(dates))
	slot := plotW / n
	x := func(i int) float64 { return left + slot*(float64(i)+0.5) }
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="card"><figcaption>%s <small>%s</small></figcaption><svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`,
		html.EscapeString(title), html.EscapeString(unit), width, height, html.EscapeString(title))
	tickPlaces := 0
	if maxV-lo < 10 {
		tickPlaces = 1
	}
	for t := 0; t <= 3; t++ {
		v := lo + (maxV-lo)*float64(t)/3
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`,
			left, width-8, y(v), y(v), left-6, y(v)+4, formatNumber(v, tickPlaces))
	}
	step := int(math.Ceil(n / 8))
	last := len(dates) - 1
	for i, d := range dates {
		// Label every step-th day plus the last one unless it would collide.
		if i%step == 0 && (i == last || last-i >= step/2+1) || i == last {
			fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(i), height-6, d[5:])
		}
	}
	barW := math.Max(1, math.Min(slot*0.7, 28))
	switch kind {
	case "bar", "stack":
		for i, d := range dates {
			base := lo
			for _, s := range series {
				v := s.Values[i]
				if math.IsNaN(v) || v <= 0 {
					continue
				}
				fmt.Fprintf(&b, `<rect class="%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s %s: %s %s</title></rect>`,
					s.Class, x(i)-barW/2, y(base+v), barW, y(base)-y(base+v), d, html.EscapeString(s.Name), formatNumber(v, 0), html.EscapeString(unit))
				base += v
			}
		}
	case "line":
		for _, s := range series {
			var path strings.Builder
			pen := "M"
			for i, v := range s.Values {
				if math.IsNaN(v) {
					pen = "M"
					continue
				}
				fmt.Fprintf(&path, "%s%.1f %.1f ", pen, x(i), y(v))
				pen = "L"
				fmt.Fprintf(&b, `<circle class="%s" cx="%.1f" cy="%.1f" r="3"><title>%s: %s %s</title></circle>`, s.Class, x(i), y(v), dates[i], formatNumber(v, 1), html.EscapeString(unit))
			}
			fmt.Fprintf(&b, `<path class="line %s" d="%s"/>`, s.Class, strings.TrimSpace(path.String()))
		}
	}
	b.WriteString(`</svg>`)
	if len(series) > 1 {
		b.WriteString(`<div class="legend">`)
		for _, s := range series {
			fmt.Fprintf(&b, `<span><i class="%s"></i>%s</span>`, s.Class, html.EscapeString(s.Name))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</figure>`)
	return template.HTML(b.String())
}

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Health Dashboard</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--text:#16181d;--muted:#5f6673;--grid:#e3e6eb;--c1:#3b82f6;--c2:#8b5cf6;--c3:#0ea5a4;--c4:#f97316;--z1:#93c5fd;--z2:#34d399;--z3:#fbbf24;--z4:#fb923c;--z5:#ef4444}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--card:#181b22;--text:#e8eaef;--muted:#9aa3b2;--grid:#2a2f3a}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1100px;margin:0 auto;padding:24px 16px 48px}h1{font-size:24px;margin:0 0 4px}.sub{color:var(--muted);margin:0 0 20px}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin-bottom:16px}
.tile,.card{background:var(--card);border-radius:14px;padding:14px 16px;box-shadow:0 1px 2px rgba(0,0,0,.06)}
.tile b{display:block;font-size:22px}.tile span,.tile small{color:var(--muted);font-size:13px}.tile em{font-style:normal;font-size:13px;margin-left:6px}
.grid2{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,480px),1fr));gap:12px}
.card{margin:0}figcaption{font-weight:600;margin-bottom:6px}figcaption small{color:var(--muted);font-weight:400}
svg{width:100%;height:auto;display:block}.grid{stroke:var(--grid)}.axis{fill:var(--muted);font-size:11px}
rect.c1,circle.c1,i.c1{fill:var(--c1);background:var(--c1)}rect.c2,circle.c2,i.c2{fill:var(--c2);background:var(--c2)}
rect.c3,circle.c3,i.c3{fill:var(--c3);background:var(--c3)}rect.c4,circle.c4,i.c4{fill:var(--c4);background:var(--c4)}
path.line{fill:none;stroke-width:2.2}path.c1{stroke:var(--c1)}path.c2{stroke:var(--c2)}path.c3{stroke:var(--c3)}path.c4{stroke:var(--c4)}
.legend{display:flex;gap:14px;flex-wrap:wrap;color:var(--muted);font-size:13px}.legend i{display:inline-block;width:10px;height:10px;border-radius:3px;margin-right:5px}
ul.hl{margin:0;padding-left:20px}section{margin-top:16px}h2{font-size:17px;margin:0 0 8px}
.table{overflow-x:auto}table{width:100%;border-collapse:collapse;font-size:14px}th,td{text-align:left;padding:7px 8px;border-bottom:1px solid var(--grid);white-space:nowrap}
th{color:var(--muted);font-weight:500}.zones{display:flex;width:140px;height:10px;border-radius:5px;overflow:hidden;background:var(--grid)}
.z1{background:var(--z1)}.z2{background:var(--z2)}.z3{background:var(--z3)}.z4{background:var(--z4)}.z5{background:var(--z5)}
footer{color:var(--muted);font-size:13px;margin-top:24px}
</style></head><body><main>
<h1>Health dashboard</h1>
<p class="sub">{{.Report.From}} → {{.Report.To}} · {{.Report.Days}} days ({{.Report.DaysWithData}} with data) · {{.Report.Timezone}}</p>
<div class="tiles">{{range .Tiles}}<div class="tile"><span>{{.Label}}</span><b>{{.Value}}</b><small>{{.Sub}}</small>{{if .Trend}}<em>{{.Trend}}</em>{{end}}</div>{{end}}</div>
<div class="card"><h2>Highlights</h2><ul class="hl">{{range .Report.Highlights}}<li>{{.}}</li>{{end}}</ul></div>
<section class="grid2">{{range .Charts}}{{.}}{{end}}</section>
{{if .Workouts}}<section class="card"><h2>Workouts</h2><div class="table"><table><thead><tr><th>Date</th><th>Type</th><th>Duration</th><th>Distance</th><th>Avg / max HR</th><th>HR zones</th></tr></thead><tbody>
{{range .Workouts}}<tr><td>{{.Date}}</td><td>{{.Type}}</td><td>{{.Duration}}</td><td>{{.Distance}}</td><td>{{.HR}}</td><td>{{.Zones}}</td></tr>{{end}}
</tbody></table></div></section>{{end}}
<footer>{{.Report.Notice}} Generated {{.Report.GeneratedAt}}.</footer>
</main></body></html>
`))
