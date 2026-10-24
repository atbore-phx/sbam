package optimize

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// Consumption profile limits (design review §Consumption profile, S-004,
// S-010).
const (
	ProfileMinCoverage   = 0.9
	ProfileMinSamples    = 3
	ProfileMaxHourWh     = 25000.0
	ProfileMinDailyRatio = 0.25
	ProfileMaxDailyRatio = 4.0
	ProfileMaxDays       = 30
)

// ErrProfileArgs is returned for invalid BuildProfile arguments.
var ErrProfileArgs = errors.New("invalid profile arguments")

// ProfileBuild is the detailed result of BuildProfileDetail.
type ProfileBuild struct {
	Profile Profile
	Reasons []Reason
	// StaticHours lists the hours of day (ascending) that use the static value.
	StaticHours []int
	// Samples is the number of usable slots per hour of day.
	Samples [24]int
}

// BuildProfile builds the expected household consumption per local hour of
// day (AC-1 … AC-3). See BuildProfileDetail.
func BuildProfile(load, ev []Sample, loadUnit, evUnit string, days int, now time.Time, loc *time.Location, staticWhPerHour float64) (Profile, []Reason, error) {
	b, err := BuildProfileDetail(load, ev, loadUnit, evUnit, days, now, loc, staticWhPerHour)
	if err != nil {
		return Profile{}, nil, err
	}
	return b.Profile, b.Reasons, nil
}

// BuildProfileDetail integrates the load and EV histories as step functions
// per absolute hour over the last `days` complete local days, and averages
// max(0, load − ev) per local hour of day.
//
//   - Values are converted to W from loadUnit/evUnit (W or kW); invalid,
//     non-finite and out-of-bounds samples are gaps.
//   - A slot is usable when the load has numeric coverage ≥ 90 %; its energy
//     is scaled to the full hour. EV gaps count as 0 W.
//   - Without EV samples and EV unit, EV is skipped (AC-3). An EV with an
//     unsupported unit is skipped with ReasonEVEntityNotFound.
//   - An hour with fewer than 3 usable slots, or a mean above 25 kWh, uses
//     staticWhPerHour (AC-2). If the daily sum is outside [0.25, 4] × the static
//     daily value, the whole profile is static. An unsupported load unit makes
//     the whole profile static. Both add ReasonConsumptionStaticFallback.
//
// DST days are handled because slots are absolute hours: the 25-hour day adds
// two samples to its repeated hour, the 23-hour day adds none to the skipped one.
func BuildProfileDetail(load, ev []Sample, loadUnit, evUnit string, days int, now time.Time, loc *time.Location, staticWhPerHour float64) (ProfileBuild, error) {
	if loc == nil {
		return ProfileBuild{}, fmt.Errorf("%w: nil location", ErrProfileArgs)
	}
	if days < 1 || days > ProfileMaxDays {
		return ProfileBuild{}, fmt.Errorf("%w: days %d outside 1..%d", ErrProfileArgs, days, ProfileMaxDays)
	}
	if math.IsNaN(staticWhPerHour) || math.IsInf(staticWhPerHour, 0) || staticWhPerHour <= 0 {
		return ProfileBuild{}, fmt.Errorf("%w: static Wh per hour must be finite and > 0", ErrProfileArgs)
	}

	var reasons []Reason
	loadW, err := SanitizePowerSamples(load, loadUnit)
	if err != nil {
		return profileAllStatic(staticWhPerHour, []Reason{ReasonConsumptionStaticFallback}), nil
	}
	var evW []Sample
	if len(ev) > 0 || evUnit != "" {
		evW, err = SanitizePowerSamples(ev, evUnit)
		if err != nil {
			evW = nil
			reasons = append(reasons, ReasonEVEntityNotFound)
		}
	}
	profileSortSamples(loadW)
	profileSortSamples(evW)

	l := now.In(loc)
	end := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	start := time.Date(l.Year(), l.Month(), l.Day()-days, 0, 0, 0, 0, loc)

	var sums [24]float64
	var b ProfileBuild
	for slot := start; slot.Before(end); slot = slot.Add(time.Hour) {
		slotEnd := slot.Add(time.Hour)
		loadWh, covered := profileIntegrate(loadW, slot, slotEnd)
		if covered < time.Duration(ProfileMinCoverage*float64(time.Hour)) {
			continue
		}
		loadWh *= float64(time.Hour) / float64(covered)
		evWh, _ := profileIntegrate(evW, slot, slotEnd)
		hod := slot.In(loc).Hour()
		sums[hod] += math.Max(0, loadWh-evWh)
		b.Samples[hod]++
	}

	static := false
	for h := 0; h < 24; h++ {
		mean := 0.0
		if b.Samples[h] > 0 {
			mean = sums[h] / float64(b.Samples[h])
		}
		if b.Samples[h] < ProfileMinSamples || mean > ProfileMaxHourWh {
			b.Profile[h] = staticWhPerHour
			b.StaticHours = append(b.StaticHours, h)
			static = true
			continue
		}
		b.Profile[h] = mean
	}

	daily := 0.0
	for _, v := range b.Profile {
		daily += v
	}
	staticDaily := 24 * staticWhPerHour
	if daily < ProfileMinDailyRatio*staticDaily || daily > ProfileMaxDailyRatio*staticDaily {
		samples := b.Samples
		b = profileAllStatic(staticWhPerHour, nil)
		b.Samples = samples
		static = true
	}
	if static {
		reasons = append([]Reason{ReasonConsumptionStaticFallback}, reasons...)
	}
	b.Reasons = reasons
	return b, nil
}

// profileAllStatic returns a profile that uses the static value for every hour.
func profileAllStatic(staticWhPerHour float64, reasons []Reason) ProfileBuild {
	var b ProfileBuild
	b.StaticHours = make([]int, 24)
	for h := 0; h < 24; h++ {
		b.Profile[h] = staticWhPerHour
		b.StaticHours[h] = h
	}
	b.Reasons = reasons
	return b
}

func profileSortSamples(s []Sample) {
	sort.SliceStable(s, func(i, j int) bool { return s[i].At.Before(s[j].At) })
}

// profileIntegrate integrates a sorted step function over [from, to). It
// returns the energy in Wh from valid spans and the duration covered by valid
// spans. Time before the first sample and invalid spans are gaps.
func profileIntegrate(s []Sample, from, to time.Time) (float64, time.Duration) {
	if len(s) == 0 {
		return 0, 0
	}
	// Index of the last sample at or before from (or -1).
	i := sort.Search(len(s), func(k int) bool { return s[k].At.After(from) }) - 1
	var (
		wh      float64
		covered time.Duration
	)
	cursor := from
	for {
		next := to
		if i+1 < len(s) && s[i+1].At.Before(to) {
			next = s[i+1].At
		}
		if i >= 0 && s[i].Valid && next.After(cursor) {
			d := next.Sub(cursor)
			wh += s[i].Value * d.Hours()
			covered += d
		}
		if !next.Before(to) {
			break
		}
		cursor = next
		i++
	}
	return wh, covered
}
