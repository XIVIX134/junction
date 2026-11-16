package main

import (
	"math"
	"time"
)

// bundle works out the current data bundle from the renewal/check log.
// The latest renewal starts the bundle; the latest entry at or after it (a
// renewal or a "set what's left" check) is the anchor that "left" counts down from.
func (s *State) bundle(now time.Time) (*Bundle, error) {
	evs, err := s.store.bundleEvents(200)
	if err != nil {
		return nil, err
	}
	return s.bundleAt(now, evs, nil)
}

// bundleAt works the bundle out as of now from the given log entries (newest first).
// end, when set, is an entry logged at now: only the part of its clock hour before
// it counts (what Junction showed at that moment).
func (s *State) bundleAt(now time.Time, evs []bundleEvent, end *bundleEvent) (*Bundle, error) {
	var renewal *bundleEvent
	for i := range evs { // newest first
		if evs[i].Kind == "renewal" && !evs[i].At.After(now) {
			renewal = &evs[i]
			break
		}
	}
	if renewal == nil {
		return nil, nil
	}
	anchor := *renewal
	for _, e := range evs {
		if !e.At.Before(renewal.At) && !e.At.After(now) && e.At.After(anchor.At) {
			anchor = e
			break
		}
	}

	type row struct {
		h, n   int64
		direct bool
	}
	var rows []row
	hourTotal := map[int64]int64{}
	// Up to the end of now's clock hour, not into the next one (now can be a past moment).
	upto := now.Truncate(time.Hour).Add(time.Hour)
	err := s.sumUsage(renewal.At.Truncate(time.Hour), upto, "", func(h int64, _, exit string, down, up int64) {
		rows = append(rows, row{h, down + up, exit == "direct"})
		hourTotal[h] += down + up
	})
	if err != nil {
		return nil, err
	}
	// Usage is stored per clock hour; an entry logged mid-hour only counts the
	// part of that hour after it (exactly if we know what was counted before it).
	share := func(e bundleEvent) (int64, float64) {
		hs := e.At.Truncate(time.Hour).Unix()
		tot := hourTotal[hs]
		if tot <= 0 {
			return hs, 1
		}
		if e.HourBefore >= 0 {
			return hs, clamp01(float64(tot-e.HourBefore) / float64(tot))
		}
		end := hs + 3600
		if n := now.Unix(); n < end {
			end = n
		}
		if end <= hs {
			return hs, 1
		}
		return hs, clamp01(float64(end-e.At.Unix()) / float64(end-hs))
	}
	rh, rf := share(*renewal)
	ah, af := share(anchor)
	// The hour of end: only what came before it.
	eh, ef := int64(-1), 1.0
	if end != nil {
		eh = end.At.Truncate(time.Hour).Unix()
		if tot := hourTotal[eh]; end.HourBefore >= 0 && tot > 0 {
			ef = clamp01(float64(end.HourBefore) / float64(tot))
		} else {
			ef = clamp01(float64(end.At.Unix()-eh) / 3600)
		}
	}
	part := func(h, n int64) int64 {
		f := 1.0
		if h == rh {
			f *= rf
		}
		if h == eh {
			f *= ef
		}
		if f == 1 {
			return n
		}
		return int64(float64(n) * f)
	}
	// VPN / direct: the content each exit carried.
	var tunnel, direct int64
	for _, r := range rows {
		n := part(r.h, r.n)
		if r.direct {
			direct += n
		} else {
			tunnel += n
		}
	}
	// Used and left: what the ISP bills, whole packets (wire.go).
	billed, f := s.billed(hourTotal, renewal.At.Truncate(time.Hour), upto)
	var used, sinceAnchor int64
	for h, n := range billed {
		used += part(h, n)
		switch {
		case h > ah:
			sinceAnchor += part(h, n)
		case h == ah:
			sinceAnchor += int64(float64(part(h, n)) * af)
		}
	}

	// Counted the way your ISP counts, once your logged figures say how (calibrate.go).
	cal := s.calibrate(evs, now)
	if cal != nil {
		used = int64(float64(used) * cal.Factor)
		sinceAnchor = int64(float64(sinceAnchor) * cal.Factor)
	}
	b := &Bundle{
		RenewedAt: renewal.At.UTC().Format(time.RFC3339), Size: renewal.Amount,
		Anchor:           Anchor{Kind: anchor.Kind, At: anchor.At.UTC().Format(time.RFC3339), Amount: anchor.Amount},
		UsedSinceRenewal: used, Tunnel: tunnel, Direct: direct,
		Left:        anchor.Amount - sinceAnchor,
		Calibration: cal,
		OverheadPct: math.Round((f-1)*1000) / 10,
	}
	if renewal.Expires != nil {
		x := renewal.Expires.UTC().Format(time.RFC3339)
		b.ExpiresAt = &x
	}
	// Pace: what the bundle says is used (your ISP's own figure at the last check, then
	// Junction's count) over the time since the renewal. Junction's own count alone
	// misses whatever it didn't see, like traffic from before a device used the gateway.
	spent := used
	if anchor.Kind == "check" {
		spent = renewal.Amount - b.Left
	}
	if days := now.Sub(renewal.At).Hours() / 24; days >= 0.25 && spent > 0 {
		b.PacePerDay = int64(float64(spent) / days)
		if b.Left > 0 {
			out := now.Add(time.Duration(float64(b.Left) / float64(b.PacePerDay) * 24 * float64(time.Hour)))
			o := out.UTC().Format(time.RFC3339)
			b.RunsOut = &o
		}
	}
	return b, nil
}

func (s *State) bundleInfo(now time.Time) (BundleInfo, error) {
	cur, err := s.bundle(now)
	if err != nil {
		return BundleInfo{}, err
	}
	evs, err := s.store.bundleEvents(50)
	if err != nil {
		return BundleInfo{}, err
	}
	info := BundleInfo{Current: cur, Events: []BundleEvent{}}
	for i, e := range evs {
		be := BundleEvent{ID: e.ID, Kind: e.Kind, At: e.At.UTC().Format(time.RFC3339), Amount: e.Amount}
		if e.Kind == "check" && i < 10 {
			be.Expected = s.expectedAt(e, evs[i+1:])
		}
		if e.Expires != nil {
			x := e.Expires.UTC().Format(time.RFC3339)
			be.ExpiresAt = &x
		}
		info.Events = append(info.Events, be)
	}
	return info, nil
}

// expectedAt is what Junction said was left when a check was logged: the bundle
// worked out from the entries before it, up to that moment. nil when there was no
// bundle to count down from.
func (s *State) expectedAt(e bundleEvent, older []bundleEvent) *int64 {
	b, err := s.bundleAt(e.At, older, &e)
	if err != nil || b == nil {
		return nil
	}
	return &b.Left
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// hourBefore returns the bytes already counted in at's clock hour, when at is in
// the current hour (the usual "I just read it" case); -1 otherwise.
func (s *State) hourBefore(at time.Time) int64 {
	now := time.Now()
	hs := now.Truncate(time.Hour)
	if at.Before(hs) || at.After(now.Add(time.Minute)) {
		return -1
	}
	var n int64
	if err := s.sumUsage(hs, hs.Add(time.Hour), "", func(_ int64, _, _ string, down, up int64) { n += down + up }); err != nil {
		return -1
	}
	return n
}
