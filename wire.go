package main

// Bytes on the wire: whole IP packets between the gateway and the internet, the
// way the ISP bills them (headers, acknowledgements and retransmissions included). The
// content sing-box reports is about 6% less (measured 14 Nov: 514.9 MB content,
// 546.4 MB of packets). The bundle uses this count.
//
// Two counting-only firewall chains do the counting:
//   filter INPUT  -> JUNCTION_WIRE_IN   packets from a public address (the internet answering)
//   filter OUTPUT -> JUNCTION_WIRE_OUT  packets to a public address (the gateway sending out)
// Traffic between devices and the gateway (private addresses) returns before the
// counting rule, so nothing is counted twice. Every rule is a RETURN: the chains
// never drop or change a packet. The VPN tunnel and the gateway's own traffic are
// counted, because the ISP bills them too.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	wireIn   = "JUNCTION_WIRE_IN"
	wireOut  = "JUNCTION_WIRE_OUT"
	wireMark = "junction-wire"
	// Overhead to assume for hours Junction didn't count on the wire, until it has
	// measured its own (14 Nov, 10 min of traffic: 6.1%).
	defaultOverhead = 1.061
)

// Not the internet: never billed, and the device side of every connection.
var notInternet = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"}

var iptables = func(args ...string) ([]byte, error) {
	return exec.Command("iptables", append([]string{"-w", "5"}, args...)...).CombinedOutput()
}

var iptablesSave = func() ([]byte, error) {
	return exec.Command("iptables-save", "-c", "-t", "filter").Output()
}

// wireRules lists every rule of one counting chain, in order.
func wireRules(chain, flag string) [][]string {
	var rs [][]string
	for _, n := range notInternet {
		rs = append(rs, []string{flag, n, "-j", "RETURN"})
	}
	return append(rs, []string{"-m", "comment", "--comment", wireMark, "-j", "RETURN"})
}

// ensureWireRules makes sure both chains exist, complete, and hooked in. Chains
// that are already right are left alone, so their counters keep running.
func ensureWireRules() error {
	for _, c := range []struct{ chain, hook, flag string }{{wireIn, "INPUT", "-s"}, {wireOut, "OUTPUT", "-d"}} {
		rules := wireRules(c.chain, c.flag)
		intact := true
		if _, err := iptables("-t", "filter", "-n", "-L", c.chain); err != nil {
			if out, err := iptables("-t", "filter", "-N", c.chain); err != nil {
				return fmt.Errorf("create %s: %v %s", c.chain, err, bytes.TrimSpace(out))
			}
			intact = false
		} else {
			for _, r := range rules {
				if _, err := iptables(append([]string{"-t", "filter", "-C", c.chain}, r...)...); err != nil {
					intact = false
					break
				}
			}
		}
		if !intact {
			if out, err := iptables("-t", "filter", "-F", c.chain); err != nil {
				return fmt.Errorf("flush %s: %v %s", c.chain, err, bytes.TrimSpace(out))
			}
			for _, r := range rules {
				if out, err := iptables(append([]string{"-t", "filter", "-A", c.chain}, r...)...); err != nil {
					return fmt.Errorf("fill %s: %v %s", c.chain, err, bytes.TrimSpace(out))
				}
			}
		}
		if _, err := iptables("-t", "filter", "-C", c.hook, "-j", c.chain); err != nil {
			if out, err := iptables("-t", "filter", "-I", c.hook, "1", "-j", c.chain); err != nil {
				return fmt.Errorf("hook %s: %v %s", c.chain, err, bytes.TrimSpace(out))
			}
		}
	}
	return nil
}

// readWireCounters returns the byte counters of the two counting rules.
func readWireCounters() (in, out int64, err error) {
	b, err := iptablesSave()
	if err != nil {
		return 0, 0, err
	}
	found := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := sc.Text()
		if !strings.Contains(l, "--comment "+wireMark) || !strings.HasPrefix(l, "[") {
			continue
		}
		cnt, rest, ok := strings.Cut(l[1:], "]") // [packets:bytes] -A CHAIN ...
		_, bs, ok2 := strings.Cut(cnt, ":")
		n, err := strconv.ParseInt(bs, 10, 64)
		if !ok || !ok2 || err != nil {
			continue
		}
		switch {
		case strings.Contains(rest, "-A "+wireIn+" "):
			in, found = n, found|1
		case strings.Contains(rest, "-A "+wireOut+" "):
			out, found = n, found|2
		}
	}
	if found != 3 {
		return 0, 0, fmt.Errorf("counting rules not found")
	}
	return in, out, nil
}

// wireLoop adds what crossed the wire since the last read to the current hour.
func (s *State) wireLoop(ctx context.Context) {
	if err := ensureWireRules(); err != nil {
		log.Printf("wire: can't count bytes on the wire, using content + %.1f%%: %v", (defaultOverhead-1)*100, err)
		return
	}
	if _, ok := s.store.getSetting("wire_since"); !ok {
		s.store.setSetting("wire_since", strconv.FormatInt(time.Now().Unix(), 10))
	}
	// The counters keep running while Junction is down, so a restart loses nothing.
	var lastIn, lastOut int64
	primed := false
	if v, ok := s.store.getSetting("wire_counters"); ok {
		if a, b, ok := strings.Cut(v, "|"); ok {
			x, e1 := strconv.ParseInt(a, 10, 64)
			y, e2 := strconv.ParseInt(b, 10, 64)
			lastIn, lastOut, primed = x, y, e1 == nil && e2 == nil
		}
	}
	failing := false
	s.every(ctx, 30*time.Second, func(context.Context) {
		in, out, err := readWireCounters()
		if err != nil {
			if !failing {
				log.Printf("wire: %v; putting the rules back", err)
				failing = true
			}
			if err := ensureWireRules(); err != nil {
				log.Printf("wire: %v", err)
			}
			return
		}
		if failing {
			log.Printf("wire: counting again")
			failing = false
		}
		d := func(cur, last int64) int64 {
			if cur < last { // the rules were rebuilt (a reboot): count from zero
				return cur
			}
			return cur - last
		}
		var delta int64
		if primed {
			delta = d(in, lastIn) + d(out, lastOut)
		}
		lastIn, lastOut, primed = in, out, true
		hour := time.Now().Truncate(time.Hour).Unix()
		s.mu.Lock()
		s.wirePending[hour] += delta
		s.wireTotal += delta
		s.wireLast = fmt.Sprintf("%d|%d", in, out)
		s.mu.Unlock()
	})
}

// wireHours returns bytes on the wire per hour in [from, to), stored and pending,
// and the first hour that was counted in full (0: none yet).
func (s *State) wireHours(from, to time.Time) (map[int64]int64, int64) {
	m, err := s.store.wireRows(from, to)
	if err != nil {
		m = map[int64]int64{}
	}
	s.mu.RLock()
	for h, b := range s.wirePending {
		if h >= from.Unix() && h < to.Unix() {
			m[h] += b
		}
	}
	s.mu.RUnlock()
	var full int64
	if v, ok := s.store.getSetting("wire_since"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			full = time.Unix(n, 0).Truncate(time.Hour).Add(time.Hour).Unix()
		}
	}
	return m, full
}

// billed turns content per hour into what the ISP bills per hour: the wire count for
// hours Junction counted in full, content plus the measured overhead before that.
// It also returns the overhead factor used.
func (s *State) billed(content map[int64]int64, from, to time.Time) (map[int64]int64, float64) {
	wire, full := s.wireHours(from, to)
	// Hours before wire counting: content plus the 6.1% measured on the wire and
	// checked against the ISP's own counter (40.68 vs 40.83 GB). Not the live
	// measurement: that follows tonight's traffic mix and would quietly rewrite
	// last week's hours (and "left") whenever the mix changed.
	f := defaultOverhead
	out := make(map[int64]int64, len(content))
	for h, n := range content {
		if full > 0 && h >= full {
			if w, ok := wire[h]; ok {
				out[h] = w
				continue
			}
		}
		out[h] = int64(float64(n) * f)
	}
	for h, w := range wire { // hours with traffic on the wire but no device content (the gateway's own)
		if _, ok := out[h]; !ok && full > 0 && h >= full {
			out[h] = w
		}
	}
	return out, f
}

// overhead is the live measurement (wire / content over the last 7 days of
// fully counted hours), for the Health page only. Cached: it sums 7 days.
func (s *State) overhead() float64 {
	s.ohMu.Lock()
	defer s.ohMu.Unlock()
	if !s.ohAt.IsZero() && time.Since(s.ohAt) < 10*time.Minute {
		return s.ohF
	}
	s.ohF, _ = s.measureOverhead()
	s.ohAt = time.Now()
	return s.ohF
}

// measureOverhead returns wire / content and how much content it's based on.
func (s *State) measureOverhead() (float64, int64) {
	now := time.Now()
	from := now.AddDate(0, 0, -7).Truncate(time.Hour)
	wire, full := s.wireHours(from, now)
	if full == 0 {
		return defaultOverhead, 0
	}
	content := map[int64]int64{}
	s.sumUsage(from, now, "", func(h int64, _, _ string, down, up int64) { content[h] += down + up })
	cur := now.Truncate(time.Hour).Unix()
	var w, c int64
	for h, n := range content {
		if h >= full && h < cur && n >= 50e6 {
			if x, ok := wire[h]; ok {
				w, c = w+x, c+n
			}
		}
	}
	if c < 1e9 {
		return defaultOverhead, c
	}
	return min(max(float64(w)/float64(c), 1.0), 1.25), c
}
