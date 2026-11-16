package main

import (
	"testing"
	"time"
)

func countState(t *testing.T) *State {
	s := testState(t)
	s.prev = map[string]connPrev{}
	s.pending = map[usageKey][2]int64{}
	s.hosts = map[[3]string]int64{}
	s.wirePending = map[int64]int64{}
	s.selfIP = map[string]bool{"127.0.0.1": true}
	return s
}

func cn(id, src string, down, up int64) clashConn {
	var c clashConn
	c.ID, c.Download, c.Upload = id, down, up
	c.Metadata.SourceIP, c.Metadata.DestinationIP, c.Metadata.Host, c.Metadata.Type = src, "1.1.1.1", "example.com", "tproxy/tcp"
	return c
}

func reading(down, up int64, cs ...clashConn) clashConns {
	return clashConns{DownloadTotal: down, UploadTotal: up, Connections: cs}
}

// per device: down+up in pending
func counted(s *State) map[string]int64 {
	out := map[string]int64{}
	for k, v := range s.pending {
		out[k.Dev] += v[0] + v[1]
	}
	return out
}

func TestCountsGrowthAndNewConnections(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	s.countReading(reading(1000, 100, cn("a", "192.168.1.20", 1000, 100)), t0)
	if n := counted(s)["ip:192.168.1.20"]; n != 0 {
		t.Fatalf("first reading counted %d bytes from before Junction was watching", n)
	}
	s.countReading(reading(1500, 150, cn("a", "192.168.1.20", 1300, 120), cn("b", "192.168.1.21", 200, 30)), t0.Add(time.Second))
	c := counted(s)
	if c["ip:192.168.1.20"] != 320 || c["ip:192.168.1.21"] != 230 {
		t.Fatalf("got %v, want .20=320 .21=230", c)
	}
}

func TestClosedConnectionKeepsItsLastBytes(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	s.countReading(reading(0, 0), t0)
	s.countReading(reading(1100, 110, cn("a", "192.168.1.20", 1000, 100), cn("b", "192.168.1.21", 100, 10)), t0.Add(time.Second))
	// a closes after moving 400 down / 40 up more; b grows by 50 down.
	s.countReading(reading(1550, 150, cn("b", "192.168.1.21", 150, 10)), t0.Add(2*time.Second))
	c := counted(s)
	if c["ip:192.168.1.20"] != 1100+440 {
		t.Fatalf(".20 got %d, want 1540 (its last 440 bytes from sing-box's totals)", c["ip:192.168.1.20"])
	}
	if c["ip:192.168.1.21"] != 110+50 {
		t.Fatalf(".21 got %d, want 160", c["ip:192.168.1.21"])
	}
	if h := s.hosts[[3]string{"2026-11-16", "ip:192.168.1.20", "example.com"}]; h != 1540 {
		t.Fatalf("site bytes %d, want 1540", h)
	}
}

func TestClosedConnectionShareFollowsWhatEachMoved(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	s.countReading(reading(0, 0), t0)
	s.countReading(reading(4000, 0, cn("a", "192.168.1.20", 3000, 0), cn("b", "192.168.1.21", 1000, 0)), t0.Add(time.Second))
	before := counted(s)
	s.countReading(reading(4000+800, 0), t0.Add(2*time.Second)) // both closed; 800 more moved
	c := counted(s)
	da, db := c["ip:192.168.1.20"]-before["ip:192.168.1.20"], c["ip:192.168.1.21"]-before["ip:192.168.1.21"]
	if da+db < 798 || da < 590 || da > 610 {
		t.Fatalf("shares %d / %d, want about 600 / 200", da, db)
	}
}

func TestProbeConnectionsStayUncounted(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	p := cn("p", "127.0.0.1", 500, 0)
	p.Metadata.Type = "mixed/probe-azure"
	s.countReading(reading(0, 0), t0)
	s.countReading(reading(500, 0, p), t0.Add(time.Second))
	s.countReading(reading(900, 0), t0.Add(2*time.Second))
	if len(counted(s)) != 0 {
		t.Fatalf("probe bytes counted: %v", counted(s))
	}
}

func TestRestartPicksUpWhereItLeftOff(t *testing.T) {
	s := countState(t)
	t0 := time.Now().Add(-time.Minute).Truncate(time.Second)
	s.countReading(reading(0, 0), t0)
	s.countReading(reading(1500, 0, cn("a", "192.168.1.20", 1000, 0), cn("b", "192.168.1.21", 500, 0)), t0.Add(time.Second))
	s.flush()

	// A new run on the same database.
	s2 := countState(t)
	s2.store = s.store
	s2.resume(time.Now())
	if !s2.primed {
		t.Fatal("did not pick up the saved reading")
	}
	// While it was down: a grew by 2000, b closed after 300 more, c opened with 700.
	s2.countReading(reading(1500+2000+300+700, 0, cn("a", "192.168.1.20", 3000, 0), cn("c", "192.168.1.22", 700, 0)), t0.Add(20*time.Second))
	c := counted(s2)
	if c["ip:192.168.1.20"] != 2000 || c["ip:192.168.1.21"] != 300 || c["ip:192.168.1.22"] != 700 {
		t.Fatalf("got %v, want .20=2000 .21=300 .22=700", c)
	}
}

func TestStaleOrMissingReadingIsNotResumed(t *testing.T) {
	s := countState(t)
	s.store.setSetting(snapshotKey, `{"at":1,"g":[5,5],"c":{"a":{"d":5,"u":5}}}`)
	s.resume(time.Now())
	if s.primed {
		t.Fatal("resumed a reading from 1970")
	}
	s.store.setSetting(snapshotKey, `not json`)
	s.resume(time.Now())
	if s.primed {
		t.Fatal("resumed a broken reading")
	}
}

func TestSingBoxRestartCountsNewConnectionsOnly(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	s.countReading(reading(0, 0), t0)
	s.countReading(reading(9000, 0, cn("a", "192.168.1.20", 9000, 0)), t0.Add(time.Second))
	before := counted(s)["ip:192.168.1.20"]
	// sing-box restarted: totals start again from 0, every connection is new.
	s.countReading(reading(300, 0, cn("x", "192.168.1.20", 300, 0)), t0.Add(3*time.Second))
	if d := counted(s)["ip:192.168.1.20"] - before; d != 300 {
		t.Fatalf("after a sing-box restart counted %d, want 300", d)
	}
}

func TestRateUsesTimeBetweenReadings(t *testing.T) {
	s := countState(t)
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	s.countReading(reading(0, 0, cn("a", "192.168.1.20", 0, 0)), t0)
	// 10 s without a reading (sing-box's API was slow): 10 MB moved.
	s.countReading(reading(10e6, 0, cn("a", "192.168.1.20", 10e6, 0)), t0.Add(10*time.Second))
	if r := s.devs["ip:192.168.1.20"].rateDown; r > 1e6 {
		t.Fatalf("rate %.0f B/s: a gap read as a burst (want at most 1 MB/s smoothed)", r)
	}
}
