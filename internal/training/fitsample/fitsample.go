// Package fitsample writes synthetic FIT activity files – a run, a strength
// session and a HYROX simulation, as a Garmin fenix 8 would record them – for
// tests and for trying the Garmin features without a watch. All values are
// made up.
package fitsample

import (
	"bytes"
	"math"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

// Serial is the made-up serial number of the sample watch.
const Serial = 3999999001

// Athlete settings of the samples.
const (
	RestingHR = 50
	MaxHR     = 190
)

// Run is a 40-minute run at 5:00 /km with one fast kilometre (4:10 /km)
// after 10 minutes and a slowly rising heart rate.
func Run(start time.Time) []byte {
	f := newActivity(start)
	const total = 2400
	fastFrom, fastTo := 600.0, 850.0
	var dist float64
	var records []*mesgdef.Record
	var hrSum, hrMax float64
	for t := 0; t <= total; t++ {
		speed, power, cadence := 10.0/3, uint16(250), uint8(88)
		hr := 140 + float64(t)/total*15
		if ft := float64(t); ft > fastFrom && ft <= fastTo {
			speed, power, cadence, hr = 4.0, 300, 92, hr+5
		}
		if t > 0 {
			dist += speed
		}
		hrSum += hr
		hrMax = math.Max(hrMax, hr)
		records = append(records, mesgdef.NewRecord(nil).SetTimestamp(at(start, t)).SetDistanceScaled(dist).
			SetEnhancedSpeedScaled(speed).SetHeartRate(uint8(math.Round(hr))).SetPower(power).SetCadence(cadence).
			SetEnhancedAltitudeScaled(100))
	}
	f.Records = records
	f.Laps = []*mesgdef.Lap{
		lap(start, 0, 600, 2000, 145, 150, typedef.SportRunning, typedef.IntensityWarmup),
		lap(start, 600, 850, 1000, 158, 162, typedef.SportRunning, typedef.IntensityInterval),
		lap(start, 850, total, dist-3000, 151, 155, typedef.SportRunning, typedef.IntensityActive),
	}
	f.Sessions = []*mesgdef.Session{session(start, total, dist, hrSum/float64(total+1), hrMax, typedef.SportRunning, typedef.SubSportGeneric, "Run").
		SetTotalTrainingEffectScaled(3.4).SetTotalAnaerobicTrainingEffectScaled(1.2).SetTrainingLoadPeakScaled(120).
		SetAvgPower(256).SetMaxPower(300).SetAvgCadence(88).SetMaxCadence(92).SetTotalAscent(12).SetTotalDescent(12).
		SetAvgStanceTimeScaled(245).SetAvgVerticalOscillationScaled(85).SetAvgStanceTimeBalanceScaled(49.8).
		SetAvgVerticalRatioScaled(7.9).SetAvgStepLengthScaled(1150).SetEnhancedAvgRespirationRateScaled(38)}
	f.TimeInZones = []*mesgdef.TimeInZone{zones(total)}
	for i := 0; i < 300; i += 5 {
		rr := make([]float64, 5)
		for j := range rr {
			rr[j] = 0.42 + 0.01*float64((i+j)%3)
		}
		f.HRVs = append(f.HRVs, mesgdef.NewHrv(nil).SetTimeScaled(rr))
	}
	return encode(f, start, total)
}

// Strength is a session of back squats (3 × 5 at 100 kg), bench presses
// (3 × 8 at 70 kg) and pull-ups (3 × 10, bodyweight) with rests between sets.
func Strength(start time.Time) []byte {
	f := newActivity(start)
	type ex struct {
		cat    typedef.ExerciseCategory
		sub    uint16
		reps   uint16
		weight float64
	}
	plan := []ex{
		{typedef.ExerciseCategorySquat, uint16(typedef.SquatExerciseNameBarbellBackSquat), 5, 100},
		{typedef.ExerciseCategoryBenchPress, uint16(typedef.BenchPressExerciseNameBarbellBenchPress), 8, 70},
		{typedef.ExerciseCategoryPullUp, uint16(typedef.PullUpExerciseNamePullUp), 10, 0},
	}
	t, idx := 0, uint16(0)
	for _, e := range plan {
		for range 3 {
			set := mesgdef.NewSet(nil).SetStartTime(at(start, t)).SetTimestamp(at(start, t+30)).SetDurationScaled(30).
				SetRepetitions(e.reps).SetSetType(typedef.SetTypeActive).SetMessageIndex(typedef.MessageIndex(idx)).
				SetCategory([]typedef.ExerciseCategory{e.cat}).SetCategorySubtype([]uint16{e.sub})
			if e.weight > 0 {
				set.SetWeightScaled(e.weight)
			}
			rest := mesgdef.NewSet(nil).SetStartTime(at(start, t+30)).SetTimestamp(at(start, t+120)).SetDurationScaled(90).
				SetSetType(typedef.SetTypeRest).SetMessageIndex(typedef.MessageIndex(idx + 1))
			f.UnrelatedMessages = append(f.UnrelatedMessages, set.ToMesg(nil), rest.ToMesg(nil))
			t += 120
			idx += 2
		}
	}
	for s := 0; s <= t; s += 5 {
		f.Records = append(f.Records, mesgdef.NewRecord(nil).SetTimestamp(at(start, s)).SetHeartRate(uint8(105+s%40)))
	}
	f.Laps = []*mesgdef.Lap{lap(start, 0, t, 0, 115, 145, typedef.SportTraining, typedef.IntensityActive)}
	f.Sessions = []*mesgdef.Session{session(start, t, 0, 115, 145, typedef.SportTraining, typedef.SubSportStrengthTraining, "Strength").
		SetTotalTrainingEffectScaled(2.8).SetTotalAnaerobicTrainingEffectScaled(1.9).SetTrainingLoadPeakScaled(60)}
	return encode(f, start, t)
}

// Hyrox is a HYROX simulation recorded as a run with a lap per part: eight
// 1 km runs that get slower and eight stations.
func Hyrox(start time.Time) []byte {
	f := newActivity(start)
	stations := []int{240, 180, 200, 300, 270, 120, 330, 360}
	t := 0
	var dist float64
	for i := range 8 {
		run := 285 + i*5
		f.Laps = append(f.Laps, lap(start, t, t+run, 1000, float64(160+i), float64(170+i), typedef.SportRunning, typedef.IntensityActive))
		for s := t; s < t+run; s += 5 {
			f.Records = append(f.Records, mesgdef.NewRecord(nil).SetTimestamp(at(start, s)).SetHeartRate(uint8(160+i)).
				SetDistanceScaled(dist+float64(s-t)*1000/float64(run)).SetEnhancedSpeedScaled(1000/float64(run)))
		}
		t += run
		dist += 1000
		f.Laps = append(f.Laps, lap(start, t, t+stations[i], 25, 165, 175, typedef.SportRunning, typedef.IntensityActive))
		for s := t; s < t+stations[i]; s += 5 {
			f.Records = append(f.Records, mesgdef.NewRecord(nil).SetTimestamp(at(start, s)).SetHeartRate(165).SetDistanceScaled(dist))
		}
		t += stations[i]
		dist += 25
	}
	f.Sessions = []*mesgdef.Session{session(start, t, dist, 164, 178, typedef.SportRunning, typedef.SubSportIndoorRunning, "HYROX").
		SetTotalTrainingEffectScaled(4.4).SetTotalAnaerobicTrainingEffectScaled(3.1).SetTrainingLoadPeakScaled(260)}
	return encode(f, start, t)
}

// Settings is a valid FIT file that is not an activity.
func Settings() []byte {
	f := filedef.NewSettings()
	f.FileId.SetManufacturer(typedef.ManufacturerGarmin).SetProduct(uint16(typedef.GarminProductFenix8)).SetSerialNumber(Serial)
	f.UserProfiles = []*mesgdef.UserProfile{mesgdef.NewUserProfile(nil).SetRestingHeartRate(RestingHR)}
	fit := f.ToFIT(nil)
	return encodeFIT(&fit)
}

func newActivity(start time.Time) *filedef.Activity {
	f := filedef.NewActivity()
	f.FileId.SetManufacturer(typedef.ManufacturerGarmin).SetProduct(uint16(typedef.GarminProductFenix8)).
		SetSerialNumber(Serial).SetTimeCreated(start)
	f.UserProfile = mesgdef.NewUserProfile(nil).SetRestingHeartRate(RestingHR).SetDefaultMaxHeartRate(MaxHR).
		SetGender(typedef.GenderMale).SetWeightScaled(75)
	return f
}

func at(start time.Time, sec int) time.Time { return start.Add(time.Duration(sec) * time.Second) }

func lap(start time.Time, from, to int, dist, avgHR, maxHR float64, sport typedef.Sport, intensity typedef.Intensity) *mesgdef.Lap {
	l := mesgdef.NewLap(nil).SetStartTime(at(start, from)).SetTimestamp(at(start, to)).
		SetTotalElapsedTimeScaled(float64(to - from)).SetTotalTimerTimeScaled(float64(to - from)).
		SetAvgHeartRate(uint8(avgHR)).SetMaxHeartRate(uint8(maxHR)).SetSport(sport).SetIntensity(intensity).
		SetEvent(typedef.EventLap).SetEventType(typedef.EventTypeStop)
	if dist > 0 {
		l.SetTotalDistanceScaled(dist).SetEnhancedAvgSpeedScaled(dist / float64(to-from))
	}
	return l
}

func session(start time.Time, total int, dist, avgHR, maxHR float64, sport typedef.Sport, sub typedef.SubSport, name string) *mesgdef.Session {
	s := mesgdef.NewSession(nil).SetStartTime(start).SetTimestamp(at(start, total)).
		SetTotalElapsedTimeScaled(float64(total)).SetTotalTimerTimeScaled(float64(total)).
		SetAvgHeartRate(uint8(math.Round(avgHR))).SetMaxHeartRate(uint8(math.Round(maxHR))).SetSport(sport).SetSubSport(sub).
		SetSportProfileName(name).SetTotalCalories(uint16(total / 6)).SetEvent(typedef.EventSession).SetEventType(typedef.EventTypeStop)
	if dist > 0 {
		s.SetTotalDistanceScaled(dist).SetEnhancedAvgSpeedScaled(dist / float64(total))
	}
	return s
}

func zones(total int) *mesgdef.TimeInZone {
	return mesgdef.NewTimeInZone(nil).SetReferenceMesg(typedef.MesgNumSession).SetReferenceIndex(0).
		SetHrZoneHighBoundary([]uint8{95, 114, 133, 152, 171, 190}).
		SetTimeInHrZoneScaled([]float64{0, 60, 300, float64(total) - 900, 540, 0, 0}).
		SetMaxHeartRate(MaxHR).SetRestingHeartRate(RestingHR).SetHrCalcType(typedef.HrZoneCalcPercentMaxHr)
}

// encode adds the activity message (watch time zone UTC+2) and writes the file.
func encode(f *filedef.Activity, start time.Time, total int) []byte {
	end := at(start, total)
	f.Activity = mesgdef.NewActivity(nil).SetTimestamp(end).SetLocalTimestamp(end.Add(2 * time.Hour)).
		SetNumSessions(uint16(len(f.Sessions))).SetType(typedef.ActivityManual).SetEvent(typedef.EventActivity).SetEventType(typedef.EventTypeStop)
	fit := f.ToFIT(nil)
	return encodeFIT(&fit)
}

func encodeFIT(fit *proto.FIT) []byte {
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(fit); err != nil {
		panic("fitsample: " + err.Error()) // the samples are fixed; failing is a programming error
	}
	return buf.Bytes()
}
