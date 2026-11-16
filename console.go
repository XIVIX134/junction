package main

// Console (v1.2) endpoints: whole-network hourly traffic and exit latency history.

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type exitSample struct {
	At    string              `json:"at"`
	Exits map[string]*float64 `json:"exits"` // exit key (e.g. "vpn1", "direct") -> latency ms, null = down/unknown
}

// exitHistory keeps one sample a minute for 24 h, in memory only.
type exitHistory struct {
	mu      sync.Mutex
	samples []exitSample
}

const exitHistoryMax = 24 * 60

func (s *State) sampleExits(context.Context) {
	ex, _ := s.exitsView(nil)
	m := map[string]*float64{}
	for _, e := range ex {
		m[e.Key] = e.LatencyMs
	}
	s.mu.RLock()
	if s.directMs != nil {
		v := *s.directMs
		m["direct"] = &v
	} else {
		m["direct"] = nil
	}
	s.mu.RUnlock()
	h := &s.exHist
	h.mu.Lock()
	h.samples = append(h.samples, exitSample{At: time.Now().UTC().Format(time.RFC3339), Exits: m})
	if len(h.samples) > exitHistoryMax {
		h.samples = append([]exitSample(nil), h.samples[len(h.samples)-exitHistoryMax:]...)
	}
	h.mu.Unlock()
}

// GET /api/exits/history?minutes=60 -> { samples: [...] } oldest first
func (a *API) exitsHistory(w http.ResponseWriter, r *http.Request) {
	n := 60
	if v := r.URL.Query().Get("minutes"); v != "" {
		x, err := strconv.Atoi(v)
		if err != nil || x < 1 || x > exitHistoryMax {
			fail(w, 400, "minutes must be a number from 1 to 1440.")
			return
		}
		n = x
	}
	cut := time.Now().Add(-time.Duration(n) * time.Minute).UTC().Format(time.RFC3339)
	h := &a.s.exHist
	h.mu.Lock()
	out := []exitSample{}
	for _, x := range h.samples {
		if x.At >= cut {
			out = append(out, x)
		}
	}
	h.mu.Unlock()
	send(w, map[string]any{"samples": out})
}

type trafficHour struct {
	Hour string `json:"hour"`
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
	// Whole network only: what the ISP bills for the hour (whole packets on the wire,
	// the gateway's own traffic included; wire.go). Devices add up to down+up.
	Billed *int64 `json:"billed,omitempty"`
}

// GET /api/traffic?hours=24[&device=id] -> { hours: [...] } oldest first, the current hour last
func (a *API) traffic(w http.ResponseWriter, r *http.Request) {
	n := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		x, err := strconv.Atoi(v)
		if err != nil || x < 1 || x > 168 {
			fail(w, 400, "hours must be a number from 1 to 168.")
			return
		}
		n = x
	}
	now := time.Now()
	from := now.Truncate(time.Hour).Add(-time.Duration(n-1) * time.Hour)
	out := make([]trafficHour, n)
	for i := range out {
		out[i].Hour = from.Add(time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
	}
	dev := r.URL.Query().Get("device")
	content := map[int64]int64{}
	err := a.s.sumUsage(from, now.Add(time.Hour), dev, func(h int64, _, _ string, down, up int64) {
		i := int((h - from.Unix()) / 3600)
		if i >= 0 && i < n {
			out[i].Down += down
			out[i].Up += up
			content[h] += down + up
		}
	})
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	if dev == "" {
		billed, _ := a.s.billed(content, from, now.Add(time.Hour))
		f := a.s.ispFactor()
		for i := range out {
			h := from.Add(time.Duration(i) * time.Hour).Unix()
			b := int64(float64(billed[h]) * f)
			out[i].Billed = &b
		}
	}
	send(w, map[string]any{"hours": out})
}
