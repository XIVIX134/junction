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
	err = s.sumUsage(renewal.At.Truncate(time.Hour), now.Add(time.Hour), "", func(h int64, _, exit string, down, up int64) {
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
	// VPN / direct: the content each exit carried.
	var tunnel, direct int64
	for _, r := range rows {
		n := r.n
		if r.h == rh {
			n = int64(float64(n) * rf)
		}
		if r.direct {
			direct += n
		} else {
			tunnel += n
		}
	}
	// Used and left: what the ISP bills, whole packets (wire.go).
	billed, f := s.billed(hourTotal, renewal.At.Truncate(time.Hour), now.Add(time.Hour))
	var used, sinceAnchor int64
	for h, n := range billed {
		if h == rh {
			used += int64(float64(n) * rf)
		} else {
			used += n
		}
		switch {
		case h > ah:
			sinceAnchor += n
		case h == ah:
			sinceAnchor += int64(float64(n) * af)
		}
	}

	b := &Bundle{
		RenewedAt: renewal.At.UTC().Format(time.RFC3339), Size: renewal.Amount,
		Anchor:           Anchor{Kind: anchor.Kind, At: anchor.At.UTC().Format(time.RFC3339), Amount: anchor.Amount},
		UsedSinceRenewal: used, Tunnel: tunnel, Direct: direct,
		Left:        anchor.Amount - sinceAnchor,
		OverheadPct: math.Round((f-1)*1000) / 10,
	}
	if renewal.Expires != nil {
		x := renewal.Expires.UTC().Format(time.RFC3339)
		b.ExpiresAt = &x
	}
	if days := now.Sub(renewal.At).Hours() / 24; days >= 0.25 && used > 0 {
		b.PacePerDay = int64(float64(used) / days)
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
	for _, e := range evs {
		be := BundleEvent{ID: e.ID, Kind: e.Kind, At: e.At.UTC().Format(time.RFC3339), Amount: e.Amount}
		if e.Expires != nil {
			x := e.Expires.UTC().Format(time.RFC3339)
			be.ExpiresAt = &x
		}
		info.Events = append(info.Events, be)
	}
	return info, nil
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
