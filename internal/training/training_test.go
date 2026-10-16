package training

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/training/fitsample"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
)

var sampleStart = time.Date(2026, 10, 12, 6, 30, 0, 0, time.UTC)

func mustParse(t *testing.T, data []byte) *Activity {
	t.Helper()
	a, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func best(a *Activity, label string) *Best {
	for i := range a.Bests {
		if a.Bests[i].Label == label {
			return &a.Bests[i]
		}
	}
	return nil
}

func TestParseRun(t *testing.T) {
	a := mustParse(t, fitsample.Run(sampleStart))
	if a.Kind != KindRun || a.Sport != "running" || a.Name != "Run" || a.Device != "garmin fenix8" {
		t.Fatalf("identity: %+v", a)
	}
	if a.ID != "fit-3999999001-1791786600" || a.UTCOffsetMin == nil || *a.UTCOffsetMin != 120 {
		t.Fatalf("id/offset: %s %v", a.ID, a.UTCOffsetMin)
	}
	if got := a.Local(time.UTC).Format("15:04"); got != "08:30" {
		t.Fatalf("local start %s, want 08:30 (watch time zone)", got)
	}
	s := a.Summary
	if s.TimerS != 2400 || math.Abs(s.DistanceM-8166.66) > 0.1 || s.AvgCadence != 176 || s.TrainingLoad != 120 || s.AerobicTE != 3.4 {
		t.Fatalf("summary: %+v", s)
	}
	if s.Running == nil || s.Running.GroundContactMS != 245 || s.Running.StrideLengthM != 1.15 {
		t.Fatalf("running dynamics: %+v", s.Running)
	}
	if s.TRIMP <= 0 || s.EfficiencyFactor <= 0 || s.NormalizedPower == 0 {
		t.Fatalf("derived: TRIMP %v EF %v NP %v", s.TRIMP, s.EfficiencyFactor, s.NormalizedPower)
	}
	if b := best(a, "1 km"); b == nil || b.Seconds != 250 {
		t.Fatalf("best 1 km: %+v (the fast kilometre takes 250 s)", b)
	}
	if b := best(a, "5 km"); b == nil || b.Seconds != 1450 {
		t.Fatalf("best 5 km: %+v", b)
	}
	if best(a, "10 km") != nil {
		t.Fatal("no 10 km best in an 8 km run")
	}
	if b := best(a, "1 min"); b == nil || b.Watts != 300 {
		t.Fatalf("best 1 min power: %+v", b)
	}
	if a.Decoupling == nil || a.Decoupling.Percent <= 0 || a.Decoupling.Basis != "pace" {
		t.Fatalf("decoupling with rising heart rate: %+v", a.Decoupling)
	}
	if a.HRV == nil || a.HRV.Beats != 300 || a.HRV.MeanRRms != 430 {
		t.Fatalf("hrv: %+v", a.HRV)
	}
	if len(a.Laps) != 3 || a.Laps[1].Intensity != "interval" || a.Laps[1].TimerS != 250 {
		t.Fatalf("laps: %+v", a.Laps)
	}
	if len(a.HRZones) != 7 || a.HRZones[3].Seconds != 1500 || a.HRZones[3].High != 152 {
		t.Fatalf("zones: %+v", a.HRZones)
	}
	if a.Athlete == nil || a.Athlete.MaxHR != fitsample.MaxHR || a.Athlete.RestingHR != fitsample.RestingHR {
		t.Fatalf("athlete: %+v", a.Athlete)
	}
	if a.Stream == nil || a.Stream.Len() != 2401 {
		t.Fatalf("stream: %v", a.Stream)
	}
}

func TestParseStrength(t *testing.T) {
	a := mustParse(t, fitsample.Strength(sampleStart))
	if a.Kind != KindStrength || a.Summary.ActiveSets != 9 || a.Summary.TotalReps != 69 || a.Summary.VolumeKg != 3180 {
		t.Fatalf("strength summary: %s %+v", a.Kind, a.Summary)
	}
	st := StrengthSummary(a)
	if st == nil || len(st.Exercises) != 3 || st.Sets != 9 || st.AvgRestS != 90 {
		t.Fatalf("strength: %+v", st)
	}
	squat := st.Exercises[0]
	if squat.Exercise != "barbell_back_squat" || squat.Sets != 3 || squat.Reps != 15 || squat.VolumeKg != 1500 ||
		squat.BestSet != "5 × 100 kg" || squat.Est1RMKg != 116.7 {
		t.Fatalf("squat: %+v", squat)
	}
	if pull := st.Exercises[2]; pull.Exercise != "pull_up" || pull.MaxWeightKg != 0 || pull.Reps != 30 || pull.Est1RMKg != 0 {
		t.Fatalf("pull-ups are bodyweight: %+v", pull)
	}
}

func TestParseHyrox(t *testing.T) {
	a := mustParse(t, fitsample.Hyrox(sampleStart))
	if a.Kind != KindHyrox {
		t.Fatalf("kind %q, want hyrox (from the activity name)", a.Kind)
	}
	h := HyroxSplits(a)
	if h == nil || h.Source != "laps" || h.Runs != 8 || h.Stations != 8 || len(h.Splits) != 16 {
		t.Fatalf("hyrox: %+v", h)
	}
	if h.Splits[1].Name != "SkiErg" || !h.Splits[1].NameAssumed || h.Splits[15].Name != "Wall Balls" {
		t.Fatalf("station names: %+v", h.Splits)
	}
	if h.FastestRun != 1 || h.SlowestRun != 8 || h.RunFadePct == nil || *h.RunFadePct <= 0 {
		t.Fatalf("runs: fastest %d slowest %d fade %v", h.FastestRun, h.SlowestRun, h.RunFadePct)
	}
	if h.SlowestStage != "Wall Balls" || h.AvgRunPace == 0 {
		t.Fatalf("slowest station %q, avg pace %v", h.SlowestStage, h.AvgRunPace)
	}
	if want := 8*285 + 5*28; h.RunS != float64(want) {
		t.Fatalf("run time %v, want %d", h.RunS, want)
	}
}

func TestParseRejectsOtherFiles(t *testing.T) {
	if _, err := Parse(fitsample.Settings()); !errors.Is(err, ErrNotActivity) {
		t.Fatalf("settings file: %v, want ErrNotActivity", err)
	}
	if _, err := Parse([]byte("definitely not a FIT file")); err == nil || errors.Is(err, ErrNotActivity) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestParseKeepsDamagedFile(t *testing.T) {
	data := fitsample.Run(sampleStart)
	data[len(data)-1] ^= 0xFF // break the file checksum
	a, err := Parse(data)
	if err != nil {
		t.Fatalf("a file with a wrong checksum should still be read: %v", err)
	}
	if len(a.Warnings) != 1 {
		t.Fatalf("expected a warning about the checksum, got %v", a.Warnings)
	}
}

func TestReportAndRecords(t *testing.T) {
	var acts []*Activity
	// Six weeks of the same run every other day, plus a strength session
	// and a HYROX in the last week.
	for d := 0; d < 42; d += 2 {
		acts = append(acts, mustParse(t, fitsample.Run(sampleStart.AddDate(0, 0, d))))
	}
	last := sampleStart.AddDate(0, 0, 41)
	acts = append(acts, mustParse(t, fitsample.Strength(last)), mustParse(t, fitsample.Hyrox(last.Add(-3*time.Hour))))
	to := last.Format(dateLayout)
	from := last.AddDate(0, 0, -13).Format(dateLayout)

	r := BuildReport(acts, from, to, time.UTC)
	if r.Activities != 9 {
		t.Fatalf("activities in the last 14 days: %d", r.Activities)
	}
	if len(r.Weeks) != 2 || r.Totals[0].Kind != KindRun {
		t.Fatalf("weeks/totals: %+v %+v", r.Weeks, r.Totals)
	}
	l := r.Load
	if l == nil || l.Ratio == nil || l.Basis != LoadGarmin || l.HistoryDays != 42 {
		t.Fatalf("load: %+v", l)
	}
	// The last week has 3 runs (360) + strength (60) + HYROX (260) = 680.
	if l.Acute7 != 680 || *l.Ratio <= 1 {
		t.Fatalf("acute %v ratio %v", l.Acute7, *l.Ratio)
	}
	if r.Intensity == nil || r.TrainingEffect["improving"] != 7 {
		t.Fatalf("intensity %+v te %+v", r.Intensity, r.TrainingEffect)
	}

	recs := Records(acts, time.UTC)
	find := func(cat, label string) *Record {
		for i := range recs {
			if recs[i].Category == cat && recs[i].Label == label {
				return &recs[i]
			}
		}
		return nil
	}
	if r := find("run_distance", "1 km"); r == nil || r.Value != 250 || r.DistanceM != 1000 {
		t.Fatalf("1 km record: %+v", r)
	}
	if r := find("strength_1rm", "barbell_back_squat"); r == nil || r.Value != 116.7 {
		t.Fatalf("squat 1RM: %+v", r)
	}
	if r := find("hyrox", "Total time"); r == nil || r.Value != 4420 {
		t.Fatalf("hyrox: %+v", r)
	}
	if recs[0].Category != "run_distance" {
		t.Fatalf("records are grouped by category: %+v", recs[0])
	}
}

func TestEpley(t *testing.T) {
	for _, c := range []struct {
		w    float64
		reps int
		want float64
	}{{100, 1, 100}, {100, 5, 116.67}, {100, 13, 0}, {0, 5, 0}} {
		if got := round(Epley(c.w, c.reps), 2); got != c.want {
			t.Errorf("Epley(%v, %d) = %v, want %v", c.w, c.reps, got, c.want)
		}
	}
}

func TestRRSummaryDropsArtifacts(t *testing.T) {
	rr := make([]float64, 0, 100)
	for i := range 100 {
		rr = append(rr, 800+float64(i%2)*20)
	}
	rr[50] = 2000 // a missed beat
	h := rrSummary(rr)
	// 97 differences of 20 ms and one of 0 ms (beats 49 and 51 are equal).
	if h == nil || h.Beats != 99 || h.RMSSDms != 19.9 {
		t.Fatalf("hrv: %+v", h)
	}
}

func running(time.Time) typedef.Sport { return typedef.SportRunning }

func TestStreamDropsImplausibleTimestamps(t *testing.T) {
	recs := []*mesgdef.Record{
		mesgdef.NewRecord(nil).SetTimestamp(sampleStart.Add(-90 * time.Second)).SetHeartRate(100), // before the start
		mesgdef.NewRecord(nil).SetTimestamp(sampleStart).SetHeartRate(120),
		mesgdef.NewRecord(nil).SetTimestamp(sampleStart.Add(time.Second)).SetHeartRate(121),
		mesgdef.NewRecord(nil).SetTimestamp(sampleStart.AddDate(10, 0, 0)).SetHeartRate(122), // damaged
	}
	s := stream(recs, sampleStart, running)
	if s.Len() != 2 || s.T[0] != 0 {
		t.Fatalf("stream has %d samples from %v, want 2 from 0", s.Len(), s.T)
	}
	if g := grid(s, s.HR); len(g) != 2 {
		t.Fatalf("grid has %d seconds, want 2", len(g))
	}
	if stream(nil, sampleStart, running) != nil {
		t.Fatal("no records, no stream")
	}
}

// Garmin's smart recording writes a sample every few seconds; that must not
// count as pausing, while a stopped timer must.
func TestSmartRecordingAndPauses(t *testing.T) {
	s := &Stream{}
	for x := 0.0; x <= 7200; x += 8 {
		s.T = append(s.T, x)
		s.HR = append(s.HR, 140)
		s.Power = append(s.Power, 200)
		nan := math.NaN()
		s.Speed, s.Distance, s.Cadence, s.Altitude = append(s.Speed, 8), append(s.Distance, x*8), append(s.Cadence, nan), append(s.Altitude, nan)
	}
	a := &Activity{Kind: KindBike, Stream: s, Athlete: &Athlete{MaxHR: 190, RestingHR: 50}, Summary: Summary{AvgHR: 140}}
	analyze(a)
	if a.Summary.NormalizedPower != 200 {
		t.Fatalf("normalized power %v, want 200", a.Summary.NormalizedPower)
	}
	if len(a.Bests) != len(powerEfforts) {
		t.Fatalf("power bests: %d of %d", len(a.Bests), len(powerEfforts))
	}
	full := a.Summary.TRIMP

	s.Pauses = [][2]float64{{3600, 5400}} // 30 minutes stopped
	g := grid(s, s.Power)
	if !math.IsNaN(g[4000]) || math.IsNaN(g[3500]) || math.IsNaN(g[5500]) {
		t.Fatal("seconds in a pause must be NaN, around it not")
	}
	b := &Activity{Kind: KindBike, Stream: s, Athlete: a.Athlete, Summary: Summary{AvgHR: 140}}
	analyze(b)
	if want := full * 0.75; math.Abs(b.Summary.TRIMP-want) > 1 {
		t.Fatalf("TRIMP with a quarter paused: %v, want about %v", b.Summary.TRIMP, want)
	}
}

func TestGPSJumpDoesNotMakeRecords(t *testing.T) {
	s := &Stream{}
	dist := 0.0
	for x := 0; x <= 1800; x++ {
		dist += 3
		if x == 900 {
			dist += 150 // jump after a tunnel
		}
		s.T = append(s.T, float64(x))
		s.Distance = append(s.Distance, dist)
		s.Speed = append(s.Speed, 3)
		s.HR = append(s.HR, 150)
	}
	for _, b := range distanceBests(s) {
		if pace := b.Seconds / b.DistanceM * 1000; pace < 330 {
			t.Fatalf("%s in %v s (%.0f s/km) is faster than the steady 3 m/s run", b.Label, b.Seconds, pace)
		}
	}
}

func TestLapWithoutSportUsesSessionSport(t *testing.T) {
	f := filedef.NewActivity()
	f.Sessions = []*mesgdef.Session{mesgdef.NewSession(nil).SetSport(typedef.SportRunning).SetStartTime(sampleStart).SetTimestamp(sampleStart.Add(time.Hour))}
	f.Laps = []*mesgdef.Lap{mesgdef.NewLap(nil).SetStartTime(sampleStart).SetTimestamp(sampleStart.Add(time.Minute)).SetAvgCadence(88)}
	l := laps(f, sampleStart, sessionSports(f.Sessions))
	if l[0].Cadence != 176 || l[0].Sport != "running" {
		t.Fatalf("lap: %+v", l[0])
	}
}

func TestTruncatedFileIsSalvaged(t *testing.T) {
	data := fitsample.Run(sampleStart)
	a, err := Parse(data[:len(data)-2]) // the file checksum is missing
	if err != nil || len(a.Warnings) != 1 || a.Summary.TimerS != 2400 {
		t.Fatalf("file without checksum: %v %+v", err, a)
	}
	if _, err := Parse(data[:len(data)/2]); err == nil {
		t.Fatal("half a file has no summary and cannot be used")
	}
}

func TestRecordCap(t *testing.T) {
	f := filedef.NewActivity()
	f.FileId.SetSerialNumber(1).SetTimeCreated(sampleStart)
	for i := range MaxRecords + 10 {
		f.Records = append(f.Records, mesgdef.NewRecord(nil).SetTimestamp(sampleStart.Add(time.Duration(i)*time.Second)).SetHeartRate(120))
	}
	f.Sessions = []*mesgdef.Session{mesgdef.NewSession(nil).SetSport(typedef.SportRunning).SetStartTime(sampleStart).
		SetTimestamp(sampleStart.Add((MaxRecords + 10) * time.Second)).SetTotalTimerTimeScaled(MaxRecords + 10)}
	fit := f.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&fit); err != nil {
		t.Fatal(err)
	}
	a := mustParse(t, buf.Bytes())
	if a.Stream.Len() != MaxRecords || len(a.Warnings) != 1 || a.Summary.TimerS != MaxRecords+10 {
		t.Fatalf("stream %d, warnings %v, timer %v", a.Stream.Len(), a.Warnings, a.Summary.TimerS)
	}
}
