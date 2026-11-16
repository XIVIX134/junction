package main

// Matching the ISP's own count. Junction counts whole packets on the wire, but an
// ISP may bill a little differently (on the gateway this was measured at about
// 2.4% less than the wire count, 16 Nov). Every "what's left" figure you log from
// the ISP's app says how much the ISP counted since the entry before it; set
// against what Junction counted over the same time, that gives the ISP's ratio.
// Junction then counts down from your last figure at that ratio, and shows
// "as your ISP counts it" totals the same way.

import (
	"math"
	"sync"
	"time"
)

const (
	calibMinBytes  = 10e9 // an interval this small says too little (±100 MB in the ISP's app is 1%)
	calibMinGap    = time.Hour
	calibLowRatio  = 0.85 // outside this, Junction wasn't seeing everything (a device not on the gateway yet)
	calibHighRatio = 1.15
	calibKeep      = 3 // the latest intervals that count
	calibMaxAge    = 45 * 24 * time.Hour
)

// Calibration is how the ISP's count compares to Junction's.
type Calibration struct {
	Factor    float64 `json:"factor"`    // ISP / Junction: 0.976 = the ISP counts 2.4% less
	ISP       int64   `json:"isp"`       // bytes the ISP counted over the intervals used
	Junction  int64   `json:"junction"`  // bytes Junction counted over them
	Intervals int     `json:"intervals"` // how many intervals between your entries
	Since     string  `json:"since"`     // start of the oldest one
}

// billedBetween is what Junction counted on the wire (whole packets) between two
// log entries, with only the part of their clock hours after a and before b.
func (s *State) billedBetween(a, b bundleEvent, now time.Time) (int64, error) {
	from, to := a.At.Truncate(time.Hour), b.At.Truncate(time.Hour).Add(time.Hour)
	content := map[int64]int64{}
	if err := s.sumUsage(from, to, "", func(h int64, _, _ string, down, up int64) { content[h] += down + up }); err != nil {
		return 0, err
	}
	billed, _ := s.billed(content, from, to)
	// before: the share of e's hour that came before e.
	before := func(e bundleEvent) float64 {
		hs := e.At.Truncate(time.Hour).Unix()
		if tot := content[hs]; e.HourBefore >= 0 && tot > 0 {
			return clamp01(float64(e.HourBefore) / float64(tot))
		}
		end := hs + 3600
		if n := now.Unix(); n < end {
			end = n
		}
		if end <= hs {
			return 1
		}
		return clamp01(float64(e.At.Unix()-hs) / float64(end-hs))
	}
	ah, bh := a.At.Truncate(time.Hour).Unix(), b.At.Truncate(time.Hour).Unix()
	var n float64
	for h, x := range billed {
		f := 1.0
		switch {
		case h == ah && h == bh:
			f = max(0, before(b)-before(a))
		case h == ah:
			f = 1 - before(a)
		case h == bh:
			f = before(b)
		}
		n += float64(x) * f
	}
	return int64(n), nil
}

// calibrate works the ratio out from log entries (newest first) up to now.
// nil: not enough to go on yet.
func (s *State) calibrate(evs []bundleEvent, now time.Time) *Calibration {
	var past []bundleEvent // oldest first
	for i := len(evs) - 1; i >= 0; i-- {
		if e := evs[i]; !e.At.After(now) && now.Sub(e.At) <= calibMaxAge {
			past = append(past, e)
		}
	}
	c := &Calibration{}
	for i := len(past) - 1; i >= 1 && c.Intervals < calibKeep; i-- {
		a, b := past[i-1], past[i]
		if b.Kind != "check" || b.At.Sub(a.At) < calibMinGap {
			continue
		}
		isp := a.Amount - b.Amount
		j, err := s.billedBetween(a, b, now)
		if err != nil || j < calibMinBytes || isp <= 0 {
			continue
		}
		if r := float64(isp) / float64(j); r < calibLowRatio || r > calibHighRatio {
			continue
		}
		c.ISP += isp
		c.Junction += j
		c.Intervals++
		c.Since = a.At.UTC().Format(time.RFC3339)
	}
	if c.Intervals == 0 {
		return nil
	}
	c.Factor = math.Round(min(max(float64(c.ISP)/float64(c.Junction), calibLowRatio), calibHighRatio)*10000) / 10000
	return c
}

// ispFactor is the current ratio for "as your ISP counts it" totals (1 until known).
// Cached: it sums usage over the intervals.
type ispCache struct {
	mu sync.Mutex
	at time.Time
	f  float64
}

func (s *State) ispFactor() float64 {
	c := &s.isp
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < 5*time.Minute {
		return c.f
	}
	c.f, c.at = 1, time.Now()
	if evs, err := s.store.bundleEvents(50); err == nil {
		if cal := s.calibrate(evs, time.Now()); cal != nil {
			c.f = cal.Factor
		}
	}
	return c.f
}

// forgetISPFactor drops the cached ratio (an entry was logged or removed).
func (s *State) forgetISPFactor() {
	s.isp.mu.Lock()
	s.isp.at = time.Time{}
	s.isp.mu.Unlock()
}
