package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
)

func TestTrend(t *testing.T) {
	cases := []struct {
		change, previous float64
		want             string
	}{
		{5000, 0, "up"}, {-2, 0, "down"}, {0, 0, "flat"},
		{3, 100, "up"}, {-3, 100, "down"}, {2.9, 100, "flat"},
	}
	for _, c := range cases {
		if got := trend(c.change, c.previous); got != c.want {
			t.Errorf("trend(%v, %v) = %q, want %q", c.change, c.previous, got, c.want)
		}
	}
}

func applyDays(t *testing.T, db *DB, days map[string]healthdata.Day) {
	t.Helper()
	b := healthdata.Batch{Schema: 1, GeneratedAt: "2026-10-13T08:00:00Z", Timezone: "UTC", Days: days}
	if _, err := db.Apply(context.Background(), b, EnvelopeResult{Seq: 1, ID: "x", Kind: "sync", Created: b.GeneratedAt, Status: "ok"}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestReportHandlesZeroBaselineAndPartialSleep(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sleep := func(asleep float64, deep any) map[string]any {
		m := map[string]any{"asleep_min": asleep}
		if deep != nil {
			m["deep_min"] = deep
		}
		return m
	}
	applyDays(t, db, map[string]healthdata.Day{
		"2026-10-05": {"mindfulness": {"mindful_min": 0.0}},
		"2026-10-12": {"mindfulness": {"mindful_min": 10.0}, "sleep": sleep(400, 60.0)},
		"2026-10-13": {"mindfulness": {"mindful_min": 20.0}, "sleep": sleep(420, nil)},
	})
	w, _ := ResolveWindow("week", 0, "2026-10-13", "", time.Now())
	r, err := BuildReport(context.Background(), db, w, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var mindful *MetricStat
	for i := range r.Metrics {
		if r.Metrics[i].Metric == "mindfulness.mindful_min" {
			mindful = &r.Metrics[i]
		}
	}
	if mindful == nil || mindful.Trend != "up" || mindful.ChangePct != nil {
		t.Fatalf("zero baseline must read as up without a percentage: %+v", mindful)
	}
	if r.Sleep == nil || r.Sleep.AvgDeepMin == nil || *r.Sleep.AvgDeepMin != 60 || r.Sleep.AvgREMMin != nil {
		t.Fatalf("stage averages must ignore nights without the stage: %+v", r.Sleep)
	}
}
