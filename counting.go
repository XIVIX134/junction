package main

// Per-device usage comes from sing-box's connection list, read every second: each
// connection's byte counters grow, and the growth since the last reading is that
// device's traffic. Two things slipped through before:
//
//   - A connection that closes between two readings takes its last bytes with it:
//     they never show in a reading. sing-box's running totals (downloadTotal,
//     uploadTotal) do include them, so whatever the totals grew by beyond the
//     connections' own growth is shared out over the connections that just closed,
//     in proportion to what each was moving.
//   - A restart forgot where every connection's counters stood, so whatever moved
//     while Junction was down wasn't counted. The last reading is now saved with
//     every flush (together with the usage it accounts for) and picked up at start.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

// connPrev is one connection as of the last reading.
type connPrev struct {
	Down  int64  `json:"d"`
	Up    int64  `json:"u"`
	DDown int64  `json:"dd,omitempty"` // growth at the last reading: the weights for its last bytes when it closes
	DUp   int64  `json:"du,omitempty"`
	Src   string `json:"s,omitempty"`
	Exit  string `json:"e,omitempty"`
	Host  string `json:"h,omitempty"`
	Skip  bool   `json:"k,omitempty"` // never counted: a probe, the gateway itself, or LAN traffic
}

// connSnapshot is the last reading, saved so a restart picks up where it left off.
type connSnapshot struct {
	At     int64               `json:"at"` // unix seconds
	Global [2]int64            `json:"g"`
	Conns  map[string]connPrev `json:"c"`
}

const (
	snapshotKey = "conn_snapshot"
	// A saved reading older than this is not resumed: bytes moved since then would
	// all land in the current hour.
	resumeMax = time.Hour
	// Bytes of connections that opened and closed while Junction was away are only
	// shared out after a short gap; after a longer one nobody can say whose they were.
	residualMaxGap = 5 * time.Minute
)

// snapshotLocked captures the current reading. Caller holds mu.
func (s *State) snapshotLocked() []byte {
	if !s.primed || s.prevAt.IsZero() {
		return nil
	}
	b, err := json.Marshal(connSnapshot{At: s.prevAt.Unix(), Global: s.global, Conns: s.prev})
	if err != nil {
		return nil
	}
	return b
}

// resume loads the reading saved by the last run, if it is recent enough.
func (s *State) resume(now time.Time) {
	v, ok := s.store.getSetting(snapshotKey)
	if !ok {
		return
	}
	var sn connSnapshot
	if err := json.Unmarshal([]byte(v), &sn); err != nil || sn.Conns == nil {
		return
	}
	at := time.Unix(sn.At, 0)
	if age := now.Sub(at); age < 0 || age > resumeMax {
		return
	}
	s.mu.Lock()
	s.prev, s.prevAt, s.global, s.globalOK, s.primed = sn.Conns, at, sn.Global, true, true
	s.mu.Unlock()
	log.Printf("counting: picking up from the reading at %s (%d connections)", at.UTC().Format(time.RFC3339), len(sn.Conns))
}

func (s *State) skipConn(c clashConn) bool {
	m := c.Metadata
	return strings.HasPrefix(m.Type, "mixed/probe-") || s.selfIP[m.SourceIP] || m.SourceIP == "" || isPrivate(m.DestinationIP)
}

func (s *State) pollConnections(ctx context.Context) {
	var cc clashConns
	err := getJSON(ctx, s.cfg.ClashAPI+"/connections", s.secret, &cc)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clashErr = err
	if err != nil {
		return // the next reading counts what moved in between
	}
	s.lastClashOK = now
	s.countReading(cc, now)
}

// countReading turns one reading of the connection list into usage. Caller holds mu.
func (s *State) countReading(cc clashConns, now time.Time) {
	hour := now.Truncate(time.Hour).Unix()
	day := now.In(s.loc).Format("2006-01-02")
	type agg struct {
		down, up int64
		conns    int
		exit     string
		best     int64
		routed   bool // routed through the gateway (TPROXY), not just using its proxy port
	}
	per := map[string]*agg{}
	at := func(src string) *agg {
		a := per[src]
		if a == nil {
			a = &agg{}
			per[src] = a
		}
		return a
	}
	count := func(src, exit, host string, dDown, dUp int64) {
		if dDown+dUp <= 0 {
			return
		}
		id := s.idForIP(src)
		k := usageKey{Hour: hour, Dev: id, Exit: exit}
		v := s.pending[k]
		s.pending[k] = [2]int64{v[0] + dDown, v[1] + dUp}
		s.totalBytes += dDown + dUp
		s.hosts[[3]string{day, id, host}] += dDown + dUp
	}

	next := make(map[string]connPrev, len(cc.Connections))
	var grewDown, grewUp int64 // every connection's growth, counted or not
	for _, c := range cc.Connections {
		m := c.Metadata
		exit := "direct"
		if len(c.Chains) > 0 {
			exit = c.Chains[0]
		}
		host := m.Host
		if host == "" {
			host = m.DestinationIP
		}
		var dDown, dUp int64
		if p, had := s.prev[c.ID]; had {
			dDown, dUp = c.Download-p.Down, c.Upload-p.Up
		} else if s.primed {
			dDown, dUp = c.Download, c.Upload // opened since the last reading: all of it is new
		} // else: first reading with nothing to go on; don't count bytes from before we were watching
		dDown, dUp = max(dDown, 0), max(dUp, 0)
		skip := s.skipConn(c)
		next[c.ID] = connPrev{Down: c.Download, Up: c.Upload, DDown: dDown, DUp: dUp, Src: m.SourceIP, Exit: exit, Host: host, Skip: skip}
		grewDown += dDown
		grewUp += dUp
		if skip {
			continue
		}
		a := at(m.SourceIP)
		a.conns++
		a.down += dDown
		a.up += dUp
		if strings.HasPrefix(m.Type, "tproxy/") {
			a.routed = true
		}
		if dDown+dUp >= a.best {
			a.best, a.exit = dDown+dUp, exit
		}
		count(m.SourceIP, exit, host, dDown, dUp)
	}

	// The last bytes of connections that closed since the last reading.
	gap := now.Sub(s.prevAt)
	if s.primed && s.globalOK && gap <= residualMaxGap && cc.DownloadTotal >= s.global[0] && cc.UploadTotal >= s.global[1] {
		restDown := cc.DownloadTotal - s.global[0] - grewDown
		restUp := cc.UploadTotal - s.global[1] - grewUp
		var gone []connPrev
		var wDown, wUp int64
		for id, p := range s.prev {
			if _, open := next[id]; !open {
				gone = append(gone, p)
				wDown += p.DDown + 1
				wUp += p.DUp + 1
			}
		}
		if len(gone) > 0 && (restDown > 0 || restUp > 0) {
			restDown, restUp = max(restDown, 0), max(restUp, 0)
			for _, p := range gone {
				d := restDown * (p.DDown + 1) / wDown
				u := restUp * (p.DUp + 1) / wUp
				if p.Skip || d+u == 0 {
					continue
				}
				a := at(p.Src)
				a.down += d
				a.up += u
				count(p.Src, p.Exit, p.Host, d, u)
			}
		}
	}
	s.prev, s.prevAt, s.primed = next, now, true
	s.global, s.globalOK = [2]int64{cc.DownloadTotal, cc.UploadTotal}, true

	// Rates: bytes per second, also when readings were missed.
	secs := 1.0
	if gap > time.Second && gap < time.Hour {
		secs = gap.Seconds()
	}
	const alpha = 0.4 // ~3 s smoothing at 1 s polls
	var totDown, totUp float64
	seen := map[string]bool{}
	for ip, a := range per {
		id := s.idForIP(ip)
		mac := ""
		if !strings.HasPrefix(id, "ip:") {
			mac = id
		}
		d := s.device(id, ip, mac, now)
		seen[id] = true
		rd, ru := float64(a.down)/secs, float64(a.up)/secs
		d.rateDown = alpha*rd + (1-alpha)*d.rateDown
		d.rateUp = alpha*ru + (1-alpha)*d.rateUp
		d.conns = a.conns
		if a.conns > 0 {
			d.exit = a.exit
			d.LastSeen = now
		}
		if a.routed {
			d.LastGateway = now // only real routing counts: proxy use doesn't make the switches work
		}
		totDown += rd
		totUp += ru
	}
	for id, d := range s.devs {
		if !seen[id] {
			d.rateDown *= 1 - alpha
			d.rateUp *= 1 - alpha
			d.conns = 0
		}
	}
	s.liveDown = alpha*totDown + (1-alpha)*s.liveDown
	s.liveUp = alpha*totUp + (1-alpha)*s.liveUp
}
