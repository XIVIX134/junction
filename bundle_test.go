package main

import (
	"math"
	"testing"
	"time"
)

func TestBundleExpectedAndPace(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	u := map[usageKey][2]int64{}
	for h := 0; h < 12; h++ {
		u[usageKey{Hour: t0.Add(time.Duration(h) * time.Hour).Unix(), Dev: "aa", Exit: "direct"}] = [2]int64{1e9, 0}
	}
	if err := s.store.addUsage(u); err != nil {
		t.Fatal(err)
	}
	s.store.addBundleEvent("renewal", t0, 100e9, nil, -1)
	s.store.addBundleEvent("check", t0.Add(10*time.Hour), 85e9, nil, -1)

	info, err := s.bundleInfo(t0.Add(12 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var check *BundleEvent
	for i := range info.Events {
		if info.Events[i].Kind == "check" {
			check = &info.Events[i]
		}
	}
	// 10 hours of 1 GB content, plus the 6.1% assumed for hours not counted on the wire.
	if check == nil || check.Expected == nil || math.Abs(float64(*check.Expected)-(100e9-10*1.061e9)) > 1e6 {
		t.Fatalf("expected at the check: %v", check)
	}
	b := info.Current
	if math.Abs(float64(b.Left)-(85e9-2*1.061e9)) > 1e6 {
		t.Fatalf("left %d", b.Left)
	}
	// Pace follows what the bundle says is used (100 - left), not only what Junction counted.
	if want := (100e9 - float64(b.Left)) / 0.5; math.Abs(float64(b.PacePerDay)-want) > 1e6 {
		t.Fatalf("pace %d, want %.0f", b.PacePerDay, want)
	}
	for _, e := range info.Events {
		if e.Kind == "renewal" && e.Expected != nil {
			t.Fatal("a renewal has no expected figure")
		}
	}
}

func TestExpectedOnlyCountsTheHourBeforeTheCheck(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	s.store.addUsage(map[usageKey][2]int64{{Hour: t0.Unix(), Dev: "aa", Exit: "direct"}: {4e9, 0}, {Hour: t0.Unix() + 3600, Dev: "aa", Exit: "direct"}: {7e9, 0}}) // the next hour must not count
	s.store.addBundleEvent("renewal", t0, 100e9, nil, 0)
	s.store.addBundleEvent("check", t0.Add(30*time.Minute), 90e9, nil, 1e9) // 1 of the hour's 4 GB came before it
	info, _ := s.bundleInfo(t0.Add(2 * time.Hour))
	for _, e := range info.Events {
		if e.Kind == "check" {
			if e.Expected == nil || math.Abs(float64(*e.Expected)-(100e9-1.061e9)) > 1e6 {
				t.Fatalf("expected %v, want 100 GB - 1 GB x 1.061", e.Expected)
			}
			return
		}
	}
	t.Fatal("no check")
}

func TestCountsTheWayTheISPDoesOnceKnown(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	u := map[usageKey][2]int64{}
	for h := 0; h < 30; h++ { // up to now
		u[usageKey{Hour: t0.Add(time.Duration(h) * time.Hour).Unix(), Dev: "aa", Exit: "direct"}] = [2]int64{2e9, 0}
	}
	s.store.addUsage(u)
	hour := 2e9 * 1.061 // billed per hour (content + the assumed overhead)
	isp := func(hours float64) int64 { return int64(hours * hour * 0.976) }
	s.store.addBundleEvent("renewal", t0, 200e9, nil, -1)
	s.store.addBundleEvent("check", t0.Add(10*time.Hour), 200e9-isp(10), nil, -1)
	s.store.addBundleEvent("check", t0.Add(20*time.Hour), 200e9-isp(20), nil, -1)

	info, err := s.bundleInfo(t0.Add(30 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b := info.Current
	if b.Calibration == nil || math.Abs(b.Calibration.Factor-0.976) > 0.0005 || b.Calibration.Intervals != 2 {
		t.Fatalf("calibration %+v", b.Calibration)
	}
	if want := float64(200e9 - isp(30)); math.Abs(float64(b.Left)-want) > 50e6 {
		t.Fatalf("left %.2f GB, want %.2f", float64(b.Left)/1e9, want/1e9)
	}
	// The second check was predicted with the ratio learnt from the first interval.
	c := info.Events[0]
	if c.Expected == nil || math.Abs(float64(*c.Expected-c.Amount)) > 50e6 {
		t.Fatalf("expected at the second check %v, ISP said %d", c.Expected, c.Amount)
	}
}

func TestCalibrationIgnoresTimesJunctionMissedTraffic(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	u := map[usageKey][2]int64{}
	for h := 0; h < 20; h++ {
		u[usageKey{Hour: t0.Add(time.Duration(h) * time.Hour).Unix(), Dev: "aa", Exit: "direct"}] = [2]int64{1e9, 0}
	}
	s.store.addUsage(u)
	s.store.addBundleEvent("renewal", t0, 250e9, nil, -1)
	s.store.addBundleEvent("check", t0.Add(15*time.Hour), 190e9, nil, -1) // the ISP saw 60 GB, Junction ~16
	b, _ := s.bundle(t0.Add(18 * time.Hour))
	if b.Calibration != nil {
		t.Fatalf("calibrated from an interval where most traffic bypassed the gateway: %+v", b.Calibration)
	}
}

func TestFrequentFiguresStillCalibrate(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	u := map[usageKey][2]int64{}
	for h := 0; h < 30; h++ {
		u[usageKey{Hour: t0.Add(time.Duration(h) * time.Hour).Unix(), Dev: "aa", Exit: "direct"}] = [2]int64{2e9, 0}
	}
	s.store.addUsage(u)
	hour := 2e9 * 1.061
	s.store.addBundleEvent("renewal", t0, 200e9, nil, -1)
	// A figure every 2 hours (about 4 GB apart): no single interval reaches 10 GB.
	for h := 2; h <= 28; h += 2 {
		s.store.addBundleEvent("check", t0.Add(time.Duration(h)*time.Hour), 200e9-int64(float64(h)*hour*0.976), nil, -1)
	}
	b, _ := s.bundle(t0.Add(29 * time.Hour))
	if b.Calibration == nil || math.Abs(b.Calibration.Factor-0.976) > 0.001 {
		t.Fatalf("calibration %+v", b.Calibration)
	}
}
