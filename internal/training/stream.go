package training

import (
	"math"
)

// maxGap is the longest gap between samples (s) bridged by holding the last
// value. Garmin's "smart" recording writes a sample only every few seconds;
// real pauses come from the timer events (Stream.Pauses).
const maxGap = 30.0

// maxRunSpeed (m/s) is faster than any human runs; a larger distance step
// between two samples is a GPS jump.
const maxRunSpeed = 12.0

// minDecouplingS is the shortest steady recording decoupling is computed for.
const minDecouplingS = 20 * 60

// minHRVBeats is the fewest clean beats an HRV summary is computed from.
const minHRVBeats = 30

// rrArtifact is the largest change between successive beats (as a fraction)
// that is still treated as a real beat rather than a sensor artifact.
const rrArtifact = 0.2

// rrResync is how many beats in a row may be rejected before the previous
// beat is assumed to be the artifact instead.
const rrResync = 5

// npWindowS is the rolling window of the normalized-power algorithm.
const npWindowS = 30

var distanceEfforts = []struct {
	label  string
	meters float64
}{
	{"400 m", 400}, {"1 km", 1000}, {"1 mile", 1609.344}, {"3 km", 3000}, {"5 km", 5000},
	{"10 km", 10000}, {"Half marathon", 21097.5}, {"Marathon", 42195},
}

var powerEfforts = []struct {
	label   string
	seconds int
}{
	{"5 s", 5}, {"30 s", 30}, {"1 min", 60}, {"5 min", 300}, {"10 min", 600},
	{"20 min", 1200}, {"30 min", 1800}, {"60 min", 3600},
}

// analyze derives everything that needs the per-second stream.
func analyze(a *Activity) {
	s := a.Stream
	if s == nil || s.Len() < 2 {
		return
	}
	hr := grid(s, s.HR)
	power := grid(s, s.Power)
	hasPower := validCount(power) > 0

	if a.Kind == KindRun {
		a.Bests = append(a.Bests, distanceBests(s)...)
	}
	if hasPower {
		a.Bests = append(a.Bests, powerBests(power, hr)...)
		if a.Summary.NormalizedPower == 0 && validCount(power) >= 10*60 {
			a.Summary.NormalizedPower = math.Round(normalizedPower(power))
		}
	}
	sum := &a.Summary
	if at := a.Athlete; at != nil && at.FTP > 0 && sum.NormalizedPower > 0 && a.Kind == KindBike {
		if sum.IntensityFactor == 0 {
			sum.IntensityFactor = round(sum.NormalizedPower/at.FTP, 3)
		}
		if sum.TSS == 0 {
			sum.TSS = round(sum.TimerS*sum.NormalizedPower*sum.IntensityFactor/(at.FTP*3600)*100, 1)
		}
	}
	if sum.AvgHR > 0 {
		switch {
		case a.Kind == KindBike && sum.NormalizedPower > 0:
			sum.EfficiencyFactor = round(sum.NormalizedPower/sum.AvgHR, 2)
		case a.Kind == KindBike && sum.AvgPower > 0:
			sum.EfficiencyFactor = round(sum.AvgPower/sum.AvgHR, 2)
		case a.Kind == KindRun && sum.AvgSpeed > 0:
			sum.EfficiencyFactor = round(sum.AvgSpeed*60/sum.AvgHR, 2)
		}
	}
	switch a.Kind {
	case KindRun:
		a.Decoupling = decoupling(grid(s, s.Speed), hr, "pace", 0.5)
	case KindBike:
		if hasPower {
			a.Decoupling = decoupling(power, hr, "power", math.Inf(-1))
		}
	}
	if at := a.Athlete; at != nil && at.MaxHR > at.RestingHR && at.RestingHR > 0 {
		sum.TRIMP = round(trimp(hr, at.RestingHR, at.MaxHR, at.Female), 1)
	}
}

// grid resamples a channel of s to one value per second from 0 to the last
// sample, holding each value until the next sample. Seconds in a pause, or
// more than maxGap after the previous sample, are NaN.
func grid(s *Stream, v []float64) []float64 {
	t := s.T
	n := int(t[len(t)-1]) + 1
	if n <= 0 {
		return nil
	}
	out := make([]float64, n)
	j, p := 0, 0
	for sec := range n {
		x := float64(sec)
		for j+1 < len(t) && t[j+1] <= x {
			j++
		}
		for p < len(s.Pauses) && s.Pauses[p][1] <= x {
			p++
		}
		paused := p < len(s.Pauses) && s.Pauses[p][0] <= x
		if paused || t[j] > x || x-t[j] > maxGap {
			out[sec] = math.NaN()
			continue
		}
		out[sec] = v[j]
	}
	return out
}

func validCount(v []float64) int {
	n := 0
	for _, x := range v {
		if !math.IsNaN(x) {
			n++
		}
	}
	return n
}

// bestWindow returns the highest average over d consecutive seconds that
// contain no gap.
func bestWindow(v []float64, d int) (avg float64, start int, ok bool) {
	if d <= 0 || d > len(v) {
		return 0, 0, false
	}
	var sum float64
	missing := 0
	for i, x := range v {
		if math.IsNaN(x) {
			missing++
		} else {
			sum += x
		}
		if i >= d {
			if old := v[i-d]; math.IsNaN(old) {
				missing--
			} else {
				sum -= old
			}
		}
		if i >= d-1 && missing == 0 {
			if a := sum / float64(d); !ok || a > avg {
				avg, start, ok = a, i-d+1, true
			}
		}
	}
	return avg, start, ok
}

func windowMean(v []float64, from, to int) float64 {
	var sum float64
	n := 0
	for i := max(from, 0); i < min(to, len(v)); i++ {
		if !math.IsNaN(v[i]) {
			sum += v[i]
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func powerBests(power, hr []float64) []Best {
	var out []Best
	for _, e := range powerEfforts {
		avg, start, ok := bestWindow(power, e.seconds)
		if !ok || avg <= 0 {
			continue
		}
		out = append(out, Best{Kind: "power", Label: e.label, Seconds: float64(e.seconds), Watts: math.Round(avg),
			AvgHR: math.Round(windowMean(hr, start, start+e.seconds)), StartS: float64(start)})
	}
	return out
}

// distanceBests finds the fastest time for each standard distance, with the
// start interpolated between samples.
func distanceBests(s *Stream) []Best {
	t, d, hr := runDistance(s)
	if len(d) < 2 {
		return nil
	}
	var out []Best
	for _, e := range distanceEfforts {
		if d[len(d)-1]-d[0] < e.meters {
			break
		}
		best, bestStart, bestEnd := math.Inf(1), 0.0, 0
		i := 0
		for j := 1; j < len(d); j++ {
			target := d[j] - e.meters
			if target < d[0] {
				continue
			}
			for i+1 < j && d[i+1] <= target {
				i++
			}
			start := t[i]
			if d[i+1] > d[i] {
				start += (t[i+1] - t[i]) * (target - d[i]) / (d[i+1] - d[i])
			}
			if el := t[j] - start; el > 0 && el < best && e.meters/el <= maxRunSpeed {
				best, bestStart, bestEnd = el, start, j
			}
		}
		if math.IsInf(best, 1) {
			continue
		}
		var hrSum float64
		n := 0
		for k := 0; k <= bestEnd; k++ {
			if t[k] >= bestStart && !math.IsNaN(hr[k]) {
				hrSum += hr[k]
				n++
			}
		}
		b := Best{Kind: "distance", Label: e.label, DistanceM: e.meters, Seconds: math.Round(best*10) / 10, StartS: math.Round(bestStart)}
		if n > 0 {
			b.AvgHR = math.Round(hrSum / float64(n))
		}
		out = append(out, b)
	}
	return out
}

// runDistance returns the samples with a distance, with GPS jumps (steps
// faster than maxRunSpeed) replaced by the recorded speed, so a jump after a
// tunnel cannot produce an impossible best effort.
func runDistance(s *Stream) (t, d, hr []float64) {
	removed := 0.0
	for i := range s.T {
		dist := s.Distance[i]
		if math.IsNaN(dist) {
			continue
		}
		dist -= removed
		if n := len(d); n > 0 {
			step, dt := dist-d[n-1], s.T[i]-t[n-1]
			if step < 0 {
				continue
			}
			if step > maxRunSpeed*dt {
				keep := 0.0
				if sp := s.Speed[i]; !math.IsNaN(sp) && sp <= maxRunSpeed {
					keep = sp * dt
				}
				removed += step - keep
				dist = d[n-1] + keep
			}
		}
		t, d, hr = append(t, s.T[i]), append(d, dist), append(hr, s.HR[i])
	}
	return t, d, hr
}

// normalizedPower is the fourth-power mean of 30-second rolling averages.
func normalizedPower(power []float64) float64 {
	var sum4 float64
	n := 0
	var roll float64
	missing := 0
	for i, x := range power {
		if math.IsNaN(x) {
			missing++
		} else {
			roll += x
		}
		if i >= npWindowS {
			if old := power[i-npWindowS]; math.IsNaN(old) {
				missing--
			} else {
				roll -= old
			}
		}
		if i >= npWindowS-1 && missing == 0 {
			avg := roll / npWindowS
			sum4 += avg * avg * avg * avg
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Pow(sum4/float64(n), 0.25)
}

// decoupling compares output per heartbeat in the two halves of the
// recording. Samples with output at or below floor (standing still) are left
// out.
func decoupling(output, hr []float64, basis string, floor float64) *Decoupling {
	var out, beats []float64
	for i := range min(len(output), len(hr)) {
		if !math.IsNaN(output[i]) && !math.IsNaN(hr[i]) && output[i] > floor {
			out, beats = append(out, output[i]), append(beats, hr[i])
		}
	}
	if len(out) < minDecouplingS {
		return nil
	}
	half := len(out) / 2
	ef1 := meanOf(out[:half]) / meanOf(beats[:half])
	ef2 := meanOf(out[half:]) / meanOf(beats[half:])
	if ef1 <= 0 || math.IsNaN(ef1) || math.IsNaN(ef2) {
		return nil
	}
	scale := 1.0
	if basis == "pace" {
		scale = 60 // m/min per beat reads better than m/s
	}
	return &Decoupling{Percent: round((ef1-ef2)/ef1*100, 1), Basis: basis,
		FirstHalfEF: round(ef1*scale, 3), SecondHalfEF: round(ef2*scale, 3)}
}

// trimp is Banister's training impulse over the heart-rate grid.
func trimp(hr []float64, rest, maxHR float64, female bool) float64 {
	a, b := 0.64, 1.92
	if female {
		a, b = 0.86, 1.67
	}
	var total float64
	for _, x := range hr {
		if math.IsNaN(x) {
			continue
		}
		r := math.Min(math.Max((x-rest)/(maxHR-rest), 0), 1)
		total += r * a * math.Exp(b*r) / 60
	}
	return total
}

// rrSummary computes HRV from beat-to-beat intervals in milliseconds, leaving
// out beats that jump by more than rrArtifact.
func rrSummary(rr []float64) *HRVSummary {
	var clean []float64
	var sq float64
	diffs, rejected := 0, 0
	for _, x := range rr {
		if n := len(clean); n > 0 && rejected < rrResync {
			prev := clean[n-1]
			if math.Abs(x-prev) > rrArtifact*prev {
				rejected++
				continue
			}
			d := x - prev
			sq += d * d
			diffs++
		}
		// After rrResync rejections in a row the reference beat itself was
		// the artifact: restart from this beat without a difference.
		rejected = 0
		clean = append(clean, x)
	}
	if len(clean) < minHRVBeats || diffs == 0 {
		return nil
	}
	m := meanOf(clean)
	var v float64
	for _, x := range clean {
		v += (x - m) * (x - m)
	}
	return &HRVSummary{Beats: len(clean), RMSSDms: round(math.Sqrt(sq/float64(diffs)), 1),
		SDNNms: round(math.Sqrt(v/float64(len(clean))), 1), MeanRRms: round(m, 0)}
}

func meanOf(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
