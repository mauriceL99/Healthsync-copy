package training

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

// ErrNotActivity is returned for valid FIT files that hold something other
// than a recorded activity (settings, sleep, monitoring, courses, …).
var ErrNotActivity = errors.New("not an activity file")

// maxUTCOffset bounds the local-time offset a file may claim.
const maxUTCOffset = 14 * time.Hour

// maxStreamS drops samples more than a week after the start: no recording
// is that long, and a damaged timestamp must not make the per-second
// analyses allocate years of samples.
const maxStreamS = 7 * 24 * 3600

// MaxRecords bounds the per-second samples kept from one file (about 41
// hours at one per second). Decoded messages take far more memory than the
// file, so this keeps a crafted or huge file from exhausting a small server;
// the session summary still covers the whole activity.
const MaxRecords = 150_000

// Parse decodes a FIT activity file and runs all per-activity analyses.
func Parse(data []byte) (*Activity, error) {
	c, err := decode(data)
	var warnings []string
	if err != nil {
		switch {
		case len(c.act.Sessions) > 0:
			// Files copied from a watch are occasionally cut short or
			// damaged; with the summary intact the readable part is kept.
			warnings = append(warnings, "the file is damaged or incomplete (checksum or end of file); the readable part was analysed")
		case c.mesgs == 0 && errors.Is(err, decoder.ErrCRCChecksumMismatch):
			// A damaged file header stops decoding before any message.
			if c, err = decode(data, decoder.WithIgnoreChecksum()); err != nil && len(c.act.Sessions) == 0 {
				return nil, fmt.Errorf("decode FIT: %w", err)
			}
			warnings = append(warnings, "the file's checksum does not match; it may be damaged")
		case c.mesgs == 0:
			return nil, fmt.Errorf("not a FIT file: %w", err)
		case c.act.FileId.Type != typedef.FileActivity && c.act.FileId.Type != typedef.FileInvalid:
			return nil, ErrNotActivity
		default:
			return nil, fmt.Errorf("decode FIT: %w (the recording may be incomplete)", err)
		}
	}
	f := c.act
	if c.mesgs == 0 {
		return nil, errors.New("not a FIT file")
	}
	if f.FileId.Type != typedef.FileActivity {
		return nil, ErrNotActivity
	}
	if len(f.Sessions) == 0 {
		return nil, errors.New("activity file has no session summary; the recording may be incomplete")
	}
	if c.dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("only the first %d of %d samples were analysed", MaxRecords, MaxRecords+c.dropped))
	}

	a := &Activity{ID: activityID(f.FileId, data), Version: AnalysisVersion, Device: deviceName(f.FileId), Warnings: warnings}
	first := f.Sessions[0]
	a.Start = firstTime(first.StartTime, recordStart(f.Records), first.Timestamp)
	a.UTCOffsetMin = utcOffset(f.Activity)
	a.Sport, a.SubSport = enumName(first.Sport, first.Sport != typedef.SportInvalid), subSportName(first.SubSport)
	a.Name = profileName(f)
	a.Athlete = athlete(f)

	if len(f.Sessions) == 1 {
		a.Summary = sessionSummary(first)
	} else {
		a.Summary, a.Segments = multisport(f.Sessions, a.Start)
		a.Sport, a.SubSport = "multisport", ""
	}
	a.Kind = classify(first.Sport, first.SubSport, a.Name, len(f.Sessions))
	sportAt := sessionSports(f.Sessions)
	a.Laps = laps(f, a.Start, sportAt)
	a.Sets = sets(f.UnrelatedMessages, a.Start)
	for _, s := range a.Sets {
		if s.Rest {
			continue
		}
		a.Summary.ActiveSets++
		a.Summary.TotalReps += s.Reps
		if s.WeightKg != nil {
			a.Summary.VolumeKg += float64(s.Reps) * *s.WeightKg
		}
	}
	a.HRZones, a.PowerZones = zones(f)
	a.HRV = hrvSummary(f.HRVs)
	a.Stream = stream(f.Records, a.Start, sportAt)
	if a.Stream != nil {
		a.Stream.Pauses = pauses(f.Events, a.Start)
	}
	analyze(a)
	return a, nil
}

// collector builds the activity while the file is decoded, keeping at most
// MaxRecords samples.
type collector struct {
	act     *filedef.Activity
	mesgs   int
	records int
	dropped int
}

func (c *collector) OnMesg(m proto.Message) {
	c.mesgs++
	if m.Num == mesgnum.Record {
		if c.records >= MaxRecords {
			c.dropped++
			return
		}
		c.records++
	}
	c.act.Add(m)
}

// decode reads all FIT sequences of a (possibly chained) file. Whatever was
// decoded before an error stays in the collector.
func decode(data []byte, opts ...decoder.Option) (*collector, error) {
	c := &collector{act: filedef.NewActivity()}
	c.act.FileId.Type = typedef.FileInvalid // set by the file's own file_id message
	opts = append(opts, decoder.WithMesgListener(c), decoder.WithBroadcastOnly(), decoder.WithBroadcastMesgCopy())
	dec := decoder.New(bytes.NewReader(data), opts...)
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// sessionSports returns the sport at a moment: that of the session it falls
// in, else the first session's.
func sessionSports(sessions []*mesgdef.Session) func(time.Time) typedef.Sport {
	return func(t time.Time) typedef.Sport {
		for _, s := range sessions {
			if s.Sport != typedef.SportInvalid && !t.Before(s.StartTime) && !t.After(s.Timestamp) {
				return s.Sport
			}
		}
		return sessions[0].Sport
	}
}

// pauses returns the stretches (offsets from start) during which the timer
// was stopped, manually or by auto-pause.
func pauses(events []*mesgdef.Event, start time.Time) [][2]float64 {
	var out [][2]float64
	pausedAt := -1.0
	for _, e := range events {
		if e.Event != typedef.EventTimer || e.Timestamp.IsZero() {
			continue
		}
		t := e.Timestamp.Sub(start).Seconds()
		switch e.EventType {
		case typedef.EventTypeStop, typedef.EventTypeStopAll, typedef.EventTypeStopDisable, typedef.EventTypeStopDisableAll:
			if pausedAt < 0 {
				pausedAt = t
			}
		case typedef.EventTypeStart:
			if pausedAt >= 0 && t > pausedAt {
				out = append(out, [2]float64{pausedAt, t})
			}
			pausedAt = -1
		}
	}
	return out
}

// activityID is stable for the same recording, whether it comes straight
// from the watch or from a Garmin Connect "Export Original" download.
func activityID(fid mesgdef.FileId, data []byte) string {
	if fid.SerialNumber != basetype.Uint32zInvalid && !fid.TimeCreated.IsZero() {
		return fmt.Sprintf("fit-%d-%d", fid.SerialNumber, fid.TimeCreated.Unix())
	}
	sum := sha256.Sum256(data)
	return "fit-" + hex.EncodeToString(sum[:8])
}

func deviceName(fid mesgdef.FileId) string {
	maker := enumName(fid.Manufacturer, fid.Manufacturer != typedef.ManufacturerInvalid)
	product := strings.TrimSpace(fid.ProductName)
	if product == "" && fid.Product != basetype.Uint16Invalid {
		_, v := fid.GetProduct()
		product = fmt.Sprint(v)
		if strings.Contains(product, "Invalid(") {
			product = fmt.Sprintf("product %d", fid.Product)
		}
	}
	return strings.TrimSpace(maker + " " + product)
}

func profileName(f *filedef.Activity) string {
	for _, s := range f.Sessions {
		if n := strings.TrimSpace(s.SportProfileName); n != "" {
			return n
		}
	}
	for _, s := range f.Sports {
		if n := strings.TrimSpace(s.Name); n != "" {
			return n
		}
	}
	for _, w := range f.Workouts {
		if n := strings.TrimSpace(w.WktName); n != "" {
			return n
		}
	}
	return ""
}

func classify(sport typedef.Sport, sub typedef.SubSport, name string, sessions int) string {
	if strings.Contains(strings.ToLower(name), "hyrox") {
		return KindHyrox
	}
	if sessions > 1 {
		return KindMultisport
	}
	switch sport {
	case typedef.SportRunning:
		return KindRun
	case typedef.SportCycling, typedef.SportEBiking:
		return KindBike
	case typedef.SportWalking, typedef.SportHiking:
		return KindWalk
	case typedef.SportSwimming:
		return KindSwim
	case typedef.SportHiit, typedef.SportRowing:
		return KindCardio
	case typedef.SportTraining, typedef.SportFitnessEquipment:
		switch sub {
		case typedef.SubSportStrengthTraining:
			return KindStrength
		case typedef.SubSportYoga, typedef.SubSportPilates, typedef.SubSportFlexibilityTraining, typedef.SubSportBreathing:
			return KindOther
		}
		return KindCardio
	}
	return KindOther
}

// stepSport reports whether cadence is stored as strides per minute (and is
// shown as steps per minute, i.e. doubled).
func stepSport(s typedef.Sport) bool {
	return s == typedef.SportRunning || s == typedef.SportWalking || s == typedef.SportHiking
}

func sessionSummary(s *mesgdef.Session) Summary {
	steps := stepSport(s.Sport)
	sum := Summary{
		ElapsedS:        num(s.TotalElapsedTimeScaled()),
		TimerS:          num(s.TotalTimerTimeScaled()),
		MovingS:         num(s.TotalMovingTimeScaled()),
		DistanceM:       num(s.TotalDistanceScaled()),
		Calories:        u16(s.TotalCalories),
		AscentM:         u16(s.TotalAscent),
		DescentM:        u16(s.TotalDescent),
		AvgHR:           u8(s.AvgHeartRate),
		MaxHR:           u8(s.MaxHeartRate),
		MinHR:           u8(s.MinHeartRate),
		AvgSpeed:        firstNum(s.EnhancedAvgSpeedScaled(), s.AvgSpeedScaled()),
		MaxSpeed:        firstNum(s.EnhancedMaxSpeedScaled(), s.MaxSpeedScaled()),
		AvgPower:        u16(s.AvgPower),
		MaxPower:        u16(s.MaxPower),
		NormalizedPower: u16(s.NormalizedPower),
		AvgCadence:      cadence(s.AvgCadence, s.AvgFractionalCadenceScaled(), steps),
		MaxCadence:      cadence(s.MaxCadence, s.MaxFractionalCadenceScaled(), steps),
		AerobicTE:       num(s.TotalTrainingEffectScaled()),
		AnaerobicTE:     num(s.TotalAnaerobicTrainingEffectScaled()),
		TrainingLoad:    num(s.TrainingLoadPeakScaled()),
		TSS:             num(s.TrainingStressScoreScaled()),
		IntensityFactor: num(s.IntensityFactorScaled()),
		AvgRespiration:  firstNum(s.EnhancedAvgRespirationRateScaled(), u8(s.AvgRespirationRate)),
		MaxRespiration:  firstNum(s.EnhancedMaxRespirationRateScaled(), u8(s.MaxRespirationRate)),
		AvgTempC:        i8(s.AvgTemperature),
		MaxTempC:        i8(s.MaxTemperature),
		RPE:             u8(s.WorkoutRpe) / 10,
		Feel:            u8(s.WorkoutFeel),
	}
	if s.Sport == typedef.SportRunning {
		rd := RunningDynamics{
			VerticalOscillationMM: num(s.AvgVerticalOscillationScaled()),
			GroundContactMS:       num(s.AvgStanceTimeScaled()),
			GCTBalanceLeftPct:     num(s.AvgStanceTimeBalanceScaled()),
			VerticalRatioPct:      num(s.AvgVerticalRatioScaled()),
			StrideLengthM:         num(s.AvgStepLengthScaled()) / 1000,
		}
		if rd != (RunningDynamics{}) {
			sum.Running = &rd
		}
	}
	cd := CyclingDynamics{
		BalanceLeftPct:         balanceLeft(s.LeftRightBalance),
		TorqueEffectivenessPct: mean(num(s.AvgLeftTorqueEffectivenessScaled()), num(s.AvgRightTorqueEffectivenessScaled())),
		PedalSmoothnessPct:     firstNum(s.AvgCombinedPedalSmoothnessScaled(), mean(num(s.AvgLeftPedalSmoothnessScaled()), num(s.AvgRightPedalSmoothnessScaled()))),
	}
	if cd != (CyclingDynamics{}) {
		sum.Cycling = &cd
	}
	return sum
}

// multisport sums the sessions of a multisport activity and lists them.
func multisport(sessions []*mesgdef.Session, start time.Time) (Summary, []Segment) {
	var total Summary
	var hrWeighted, hrTime float64
	segs := make([]Segment, 0, len(sessions))
	for i, s := range sessions {
		sum := sessionSummary(s)
		segs = append(segs, Segment{
			Index: i + 1, Sport: enumName(s.Sport, s.Sport != typedef.SportInvalid), SubSport: subSportName(s.SubSport),
			Name: strings.TrimSpace(s.SportProfileName), StartS: offset(s.StartTime, start), TimerS: sum.TimerS,
			DistanceM: sum.DistanceM, AvgHR: sum.AvgHR, MaxHR: sum.MaxHR, Calories: sum.Calories,
		})
		total.ElapsedS += sum.ElapsedS
		total.TimerS += sum.TimerS
		total.MovingS += sum.MovingS
		total.DistanceM += sum.DistanceM
		total.Calories += sum.Calories
		total.AscentM += sum.AscentM
		total.DescentM += sum.DescentM
		// The watch writes load and training effect as running totals of
		// the whole activity, so the last (largest) value is the activity's.
		total.TrainingLoad = math.Max(total.TrainingLoad, sum.TrainingLoad)
		total.TSS += sum.TSS
		total.MaxHR = math.Max(total.MaxHR, sum.MaxHR)
		total.MaxPower = math.Max(total.MaxPower, sum.MaxPower)
		total.AerobicTE = math.Max(total.AerobicTE, sum.AerobicTE)
		total.AnaerobicTE = math.Max(total.AnaerobicTE, sum.AnaerobicTE)
		if sum.MinHR > 0 && (total.MinHR == 0 || sum.MinHR < total.MinHR) {
			total.MinHR = sum.MinHR
		}
		if sum.AvgHR > 0 {
			hrWeighted += sum.AvgHR * sum.TimerS
			hrTime += sum.TimerS
		}
		total.RPE = math.Max(total.RPE, sum.RPE)
		total.Feel = math.Max(total.Feel, sum.Feel)
	}
	if hrTime > 0 {
		total.AvgHR = math.Round(hrWeighted / hrTime)
	}
	if total.TimerS > 0 {
		total.AvgSpeed = round(total.DistanceM/total.TimerS, 3)
	}
	return total, segs
}

func laps(f *filedef.Activity, start time.Time, sportAt func(time.Time) typedef.Sport) []Lap {
	stepNames := map[typedef.MessageIndex]string{}
	for _, s := range f.WorkoutSteps {
		name := strings.TrimSpace(s.WktStepName)
		if name == "" {
			name = exerciseName(s.ExerciseCategory, s.ExerciseName)
		}
		if name != "" {
			stepNames[s.MessageIndex&typedef.MessageIndexMask] = name
		}
	}
	out := make([]Lap, 0, len(f.Laps))
	for i, l := range f.Laps {
		sport := l.Sport
		if sport == typedef.SportInvalid {
			sport = sportAt(firstTime(l.StartTime, l.Timestamp))
		}
		steps := stepSport(sport)
		lap := Lap{
			Index:     i + 1,
			Sport:     enumName(sport, sport != typedef.SportInvalid),
			Intensity: enumName(l.Intensity, l.Intensity != typedef.IntensityInvalid),
			Trigger:   enumName(l.LapTrigger, l.LapTrigger != typedef.LapTriggerInvalid),
			StartS:    offset(l.StartTime, start),
			TimerS:    num(l.TotalTimerTimeScaled()),
			DistanceM: num(l.TotalDistanceScaled()),
			AvgSpeed:  firstNum(l.EnhancedAvgSpeedScaled(), l.AvgSpeedScaled()),
			MaxSpeed:  firstNum(l.EnhancedMaxSpeedScaled(), l.MaxSpeedScaled()),
			AvgHR:     u8(l.AvgHeartRate),
			MaxHR:     u8(l.MaxHeartRate),
			AvgPower:  u16(l.AvgPower),
			MaxPower:  u16(l.MaxPower),
			NormPower: u16(l.NormalizedPower),
			Cadence:   cadence(l.AvgCadence, l.AvgFractionalCadenceScaled(), steps),
			AscentM:   u16(l.TotalAscent),
			DescentM:  u16(l.TotalDescent),
			Calories:  u16(l.TotalCalories),

			GroundContactMS:       num(l.AvgStanceTimeScaled()),
			VerticalOscillationMM: num(l.AvgVerticalOscillationScaled()),
			StrideLengthM:         num(l.AvgStepLengthScaled()) / 1000,
			Respiration:           firstNum(l.EnhancedAvgRespirationRateScaled(), u8(l.AvgRespirationRate)),
		}
		if l.WktStepIndex != typedef.MessageIndexInvalid {
			lap.Name = stepNames[l.WktStepIndex&typedef.MessageIndexMask]
		}
		out = append(out, lap)
	}
	return out
}

func sets(mesgs []proto.Message, start time.Time) []Set {
	var out []Set
	for i := range mesgs {
		if mesgs[i].Num != mesgnum.Set {
			continue
		}
		s := mesgdef.NewSet(&mesgs[i])
		set := Set{
			Index:    len(out) + 1,
			Rest:     s.SetType == typedef.SetTypeRest,
			StartS:   offset(firstTime(s.StartTime, s.Timestamp), start),
			Duration: num(s.DurationScaled()),
		}
		if s.Repetitions != basetype.Uint16Invalid {
			set.Reps = int(s.Repetitions)
		}
		if w := num(s.WeightScaled()); w > 0 {
			set.WeightKg = &w
		}
		for j, c := range s.Category {
			if c == typedef.ExerciseCategoryInvalid || c == typedef.ExerciseCategoryUnknown {
				continue
			}
			set.Category = c.String()
			sub := uint16(basetype.Uint16Invalid)
			if j < len(s.CategorySubtype) {
				sub = s.CategorySubtype[j]
			}
			set.Exercise = exerciseName(c, sub)
			break
		}
		out = append(out, set)
	}
	return out
}

// exerciseName returns the exercise's name, or its category when the
// subtype is unknown to this FIT profile.
func exerciseName(c typedef.ExerciseCategory, sub uint16) string {
	if c == typedef.ExerciseCategoryInvalid || c == typedef.ExerciseCategoryUnknown {
		return ""
	}
	if names, ok := exerciseNames[c]; ok && sub != basetype.Uint16Invalid {
		if n := names(sub); !strings.Contains(n, "Invalid(") {
			return n
		}
	}
	if n := c.String(); !strings.Contains(n, "Invalid(") {
		return n
	}
	return ""
}

func zones(f *filedef.Activity) (hr, power []ZoneTime) {
	for _, z := range f.TimeInZones {
		if z.ReferenceMesg != typedef.MesgNumSession {
			continue
		}
		hr = addZones(hr, z.TimeInHrZoneScaled(), z.HrZoneHighBoundary)
		power = addZones(power, z.TimeInPowerZoneScaled(), z.PowerZoneHighBoundary)
	}
	if hr == nil {
		for _, s := range f.Sessions {
			hr = addZones(hr, s.TimeInHrZoneScaled(), []uint8(nil))
		}
	}
	if power == nil {
		for _, s := range f.Sessions {
			power = addZones(power, s.TimeInPowerZoneScaled(), []uint16(nil))
		}
	}
	return hr, power
}

// addZones adds the times of one session to the running totals; the
// boundaries of the first session win.
func addZones[T uint8 | uint16](acc []ZoneTime, seconds []float64, high []T) []ZoneTime {
	var total float64
	for _, s := range seconds {
		total += num(s)
	}
	if total == 0 {
		return acc
	}
	for i, s := range seconds {
		if i >= len(acc) {
			z := ZoneTime{Zone: i}
			if i < len(high) && float64(high[i]) != invalidOf(high[i]) {
				z.High = float64(high[i])
			}
			acc = append(acc, z)
		}
		acc[i].Seconds += num(s)
	}
	return acc
}

// invalidOf returns the FIT invalid value of v's type.
func invalidOf[T uint8 | uint16](v T) float64 {
	switch any(v).(type) {
	case uint8:
		return float64(basetype.Uint8Invalid)
	default:
		return float64(basetype.Uint16Invalid)
	}
}

func athlete(f *filedef.Activity) *Athlete {
	var a Athlete
	for _, z := range f.TimeInZones {
		a.MaxHR = firstNum(a.MaxHR, u8(z.MaxHeartRate))
		a.RestingHR = firstNum(a.RestingHR, u8(z.RestingHeartRate))
		a.ThresholdHR = firstNum(a.ThresholdHR, u8(z.ThresholdHeartRate))
		a.FTP = firstNum(a.FTP, u16(z.FunctionalThresholdPower))
	}
	for _, z := range f.ZonesTargets {
		a.MaxHR = firstNum(a.MaxHR, u8(z.MaxHeartRate))
		a.ThresholdHR = firstNum(a.ThresholdHR, u8(z.ThresholdHeartRate))
		a.FTP = firstNum(a.FTP, u16(z.FunctionalThresholdPower))
	}
	if p := f.UserProfile; p != nil {
		a.MaxHR = firstNum(a.MaxHR, u8(p.DefaultMaxHeartRate))
		a.RestingHR = firstNum(a.RestingHR, u8(p.RestingHeartRate))
		a.WeightKg = num(p.WeightScaled())
		a.Female = p.Gender == typedef.GenderFemale
	}
	if a == (Athlete{}) {
		return nil
	}
	return &a
}

func hrvSummary(hrvs []*mesgdef.Hrv) *HRVSummary {
	var rr []float64
	for _, h := range hrvs {
		for _, t := range h.TimeScaled() {
			// Plausible beats only (30–200 bpm); FIT pads unused slots.
			if t >= 0.3 && t <= 2.0 {
				rr = append(rr, t*1000)
			}
		}
	}
	return rrSummary(rr)
}

func stream(records []*mesgdef.Record, start time.Time, sportAt func(time.Time) typedef.Sport) *Stream {
	s := &Stream{}
	last := math.Inf(-1)
	for _, r := range records {
		if r.Timestamp.IsZero() {
			continue
		}
		t := r.Timestamp.Sub(start).Seconds()
		if t <= last || t < 0 || t > maxStreamS {
			continue // duplicate, out of order, before the start or implausible
		}
		last = t
		s.T = append(s.T, t)
		s.HR = append(s.HR, nanIfZero(u8(r.HeartRate)))
		s.Speed = append(s.Speed, nanIfInvalid(firstValid(r.EnhancedSpeedScaled(), r.SpeedScaled())))
		s.Distance = append(s.Distance, nanIfInvalid(r.DistanceScaled()))
		power := math.NaN()
		if r.Power != basetype.Uint16Invalid {
			power = float64(r.Power)
		}
		s.Power = append(s.Power, power)
		c := math.NaN()
		if r.Cadence != basetype.Uint8Invalid {
			c = cadence(r.Cadence, r.FractionalCadenceScaled(), stepSport(sportAt(r.Timestamp)))
		}
		s.Cadence = append(s.Cadence, c)
		s.Altitude = append(s.Altitude, nanIfInvalid(firstValid(r.EnhancedAltitudeScaled(), r.AltitudeScaled())))
	}
	if s.Len() == 0 {
		return nil
	}
	return s
}

func recordStart(records []*mesgdef.Record) time.Time {
	for _, r := range records {
		if !r.Timestamp.IsZero() {
			return r.Timestamp
		}
	}
	return time.Time{}
}

func utcOffset(a *mesgdef.Activity) *int {
	if a == nil || a.Timestamp.IsZero() || a.LocalTimestamp.IsZero() {
		return nil
	}
	off := a.LocalTimestamp.Sub(a.Timestamp).Round(15 * time.Minute)
	if off < -maxUTCOffset || off > maxUTCOffset {
		return nil
	}
	m := int(off / time.Minute)
	return &m
}

// --- value helpers ---------------------------------------------------------------

func enumName(v fmt.Stringer, valid bool) string {
	if !valid {
		return ""
	}
	s := v.String()
	if strings.Contains(s, "Invalid(") {
		return ""
	}
	return s
}

func subSportName(s typedef.SubSport) string {
	if s == typedef.SubSportGeneric {
		return ""
	}
	return enumName(s, s != typedef.SubSportInvalid)
}

// cadence combines integer and fractional cadence; running cadence is stored
// per stride and shown per step.
func cadence(whole uint8, frac float64, steps bool) float64 {
	if whole == basetype.Uint8Invalid {
		return 0
	}
	c := float64(whole) + num(frac)
	if steps {
		c *= 2
	}
	return c
}

func balanceLeft(b typedef.LeftRightBalance100) float64 {
	if b == typedef.LeftRightBalance100Invalid || b&typedef.LeftRightBalance100Right == 0 {
		return 0 // no balance, or the side is not known
	}
	return 100 - float64(b&typedef.LeftRightBalance100Mask)/100
}

func offset(t, start time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return math.Max(0, t.Sub(start).Seconds())
}

func firstTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// num turns FIT's NaN "invalid" marker into 0.
func num(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func firstNum(vs ...float64) float64 {
	for _, v := range vs {
		if v := num(v); v != 0 {
			return v
		}
	}
	return 0
}

func firstValid(vs ...float64) float64 {
	for _, v := range vs {
		if !math.IsNaN(v) {
			return v
		}
	}
	return math.NaN()
}

func nanIfZero(v float64) float64 {
	if v == 0 {
		return math.NaN()
	}
	return v
}

func nanIfInvalid(v float64) float64 {
	if math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}

func u8(v uint8) float64 {
	if v == basetype.Uint8Invalid {
		return 0
	}
	return float64(v)
}

func u16(v uint16) float64 {
	if v == basetype.Uint16Invalid {
		return 0
	}
	return float64(v)
}

func i8(v int8) *float64 {
	if v == basetype.Sint8Invalid {
		return nil
	}
	f := float64(v)
	return &f
}

func mean(vs ...float64) float64 {
	var sum float64
	var n int
	for _, v := range vs {
		if v != 0 {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
