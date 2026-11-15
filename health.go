package main

// Health (v1.6): one screen that says whether each part of the gateway works,
// and the installed version with its update history. The checks run at most
// every 30 s however often the app asks.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type HealthCheck struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Status string  `json:"status"` // ok | warn | bad | off
	Detail string  `json:"detail"`
	Since  *string `json:"since"`
	Link   *string `json:"link"`
}

type HealthUpdate struct {
	Version string  `json:"version"`
	At      string  `json:"at"`
	Result  string  `json:"result"` // healthy | rolled-back | pending
	Note    *string `json:"note"`
}

type HealthVersion struct {
	Current     string         `json:"current"`
	InstalledAt *string        `json:"installedAt"`
	Available   *string        `json:"available"`
	History     []HealthUpdate `json:"history"`
}

type Health struct {
	CheckedAt string        `json:"checkedAt"`
	Checks    []HealthCheck `json:"checks"`
	Version   HealthVersion `json:"version"`
}

type healthState struct {
	mu     sync.Mutex
	at     time.Time
	checks []HealthCheck
	since  map[string]healthSince // when each check entered its current status
}

type healthSince struct {
	status string
	at     time.Time // zero: unchanged since Junction started
}

const healthEvery = 30 * time.Second

// Commands and files the checks read (vars for tests).
var (
	iptablesSaveMangle = func() ([]byte, error) { return exec.Command("iptables-save", "-t", "mangle").Output() }
	ipRuleShow         = func() ([]byte, error) { return exec.Command("ip", "-4", "rule", "show").Output() }
)

func hc(key, name, status, detail string) HealthCheck {
	return HealthCheck{Key: key, Name: name, Status: status, Detail: detail}
}

func ptr[T any](v T) *T { return &v }

// singboxVersion asks the Clash API for its version, timing the answer.
func (s *State) singboxVersion(ctx context.Context) (string, time.Duration, error) {
	var v struct {
		Version string `json:"version"`
	}
	start := time.Now()
	err := getJSON(ctx, s.cfg.ClashAPI+"/version", s.secret, &v)
	return strings.TrimSpace(strings.TrimPrefix(v.Version, "sing-box")), time.Since(start), err
}

func (s *State) checkRouting(ctx context.Context) HealthCheck {
	ver, took, err := s.singboxVersion(ctx)
	if err != nil {
		return hc("routing", "Traffic routing", "bad", "sing-box isn't answering ("+err.Error()+"). Devices go direct while it's down; restart sing-box if this lasts.")
	}
	s.mu.RLock()
	conns := len(s.prev)
	s.mu.RUnlock()
	name := "sing-box"
	if ver != "" {
		name += " " + ver
	}
	detail := fmt.Sprintf("%s running, %d connections, answered in %d ms", name, conns, took.Milliseconds())
	if took > time.Second {
		return hc("routing", "Traffic routing", "warn", detail+" (slow: it may be overloaded)")
	}
	return hc("routing", "Traffic routing", "ok", detail)
}

// rulesCheck: the firewall sends devices' traffic to sing-box (TPROXY) and the
// policy route delivers it locally.
func rulesCheck() (status, detail string) {
	mangle, err := iptablesSaveMangle()
	if err != nil {
		return "warn", "Couldn't read the firewall (" + err.Error() + ")."
	}
	var chain, tproxy, hooked bool
	sc := bufio.NewScanner(bytes.NewReader(mangle))
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, ":SING_BOX_TPROXY "):
			chain = true
		case strings.HasPrefix(l, "-A SING_BOX_TPROXY ") && strings.Contains(l, "-j TPROXY"):
			tproxy = true
		case strings.HasPrefix(l, "-A PREROUTING ") && strings.Contains(l, "-j SING_BOX_TPROXY"):
			hooked = true
		}
	}
	rules, err := ipRuleShow()
	route := err == nil && bytes.Contains(rules, []byte("fwmark 0x1 lookup"))
	switch {
	case !chain || !tproxy:
		return "bad", "The TPROXY rules are missing, so devices' traffic doesn't reach sing-box. Restart junction-tproxy."
	case !route:
		return "bad", "The policy route (fwmark 0x1) is missing, so traffic for sing-box is lost. Restart junction-tproxy."
	case !hooked:
		return "warn", "TPROXY is switched off: devices go direct, without VPN or controls. junction-tproxy does this while sing-box is unhealthy."
	}
	return "ok", "TPROXY rules and policy route in place"
}

func (s *State) checkCounting() HealthCheck {
	oh := (s.overhead() - 1) * 100
	if _, _, err := readWireCounters(); err != nil {
		return hc("counting", "Usage counting", "warn", fmt.Sprintf("Not counting on the wire (%v): the bundle uses content + %.1f%%", err, oh))
	}
	return hc("counting", "Usage counting", "ok", fmt.Sprintf("Content and on-the-wire counters running · overhead %.1f%%", oh))
}

func joinNames(ns []string) string {
	if len(ns) <= 1 {
		return strings.Join(ns, "")
	}
	return strings.Join(ns[:len(ns)-1], ", ") + " and " + ns[len(ns)-1]
}

func (s *State) checkExits() HealthCheck {
	ex, _ := s.exitsView(nil)
	c := hc("exits", "VPN exits", "ok", "")
	c.Link = ptr("/internet")
	if len(ex) == 0 {
		c.Status, c.Detail = "off", "No VPN exits are set up"
		return c
	}
	var up, down []string
	for _, e := range ex {
		if e.Healthy {
			if e.LatencyMs != nil {
				up = append(up, fmt.Sprintf("%s up (%.0f ms)", e.Name, *e.LatencyMs))
			} else {
				up = append(up, e.Name+" up")
			}
		} else {
			down = append(down, e.Name)
		}
	}
	switch {
	case len(up) == 0:
		c.Status, c.Detail = "warn", joinNames(down)+" down · everything goes direct"
	case len(down) == 0:
		c.Detail = strings.Join(up, " · ")
	default:
		c.Detail = strings.Join(up, " · ") + " · " + joinNames(down) + " down"
	}
	return c
}

func (s *State) checkBackup() HealthCheck {
	gs := readGatewayStatus()
	c := hc("backup", "Backup gateway", "ok", "")
	if gs == nil {
		c.Status, c.Detail = "off", "Not set up: if this box stops, the internet stops for devices using it"
		return c
	}
	name := s.base.BackupName
	if name == "" {
		name = "the backup"
	}
	switch gs.Role {
	case "master":
		c.Detail = fmt.Sprintf("This box holds %s · %s on standby", s.cfg.GatewayIP, name)
	case "backup":
		c.Status, c.Detail = "warn", fmt.Sprintf("%s is covering %s: internet works, without VPN or controls", name, s.cfg.GatewayIP)
		c.Since = gs.Since
	case "fault":
		c.Status, c.Detail = "bad", fmt.Sprintf("This box failed its health check; %s is covering (no VPN)", name)
		c.Since = gs.Since
	default:
		c.Status, c.Detail = "warn", "keepalived isn't running: if this box stops, nothing takes over"
	}
	return c
}

func (s *State) checkDHCP() HealthCheck {
	d := s.dhcpInfo()
	if d == nil || !d.On {
		return hc("dhcp", "DHCP", "off", "Not used here: the router hands out addresses")
	}
	n := 0
	if b, err := os.ReadFile(s.cfg.DnsmasqLeases); err == nil {
		now := time.Now().Unix()
		for _, l := range strings.Split(string(b), "\n") {
			var exp int64
			if _, err := fmt.Sscanf(l, "%d", &exp); err == nil && (exp == 0 || exp > now) {
				n++
			}
		}
	}
	detail := fmt.Sprintf("dnsmasq · %d leases", n)
	if d.Pool != "" {
		detail += " · pool " + d.Pool
	}
	return hc("dhcp", "DHCP", "ok", detail)
}

func (s *State) checkClock(now time.Time) HealthCheck {
	switch {
	case s.clockWait.Load():
		return hc("clock", "Clock", "warn", "Waiting for the clock to be set: usage isn't recorded until then (traffic flows)")
	case now.Year() < 2026:
		return hc("clock", "Clock", "warn", "The clock says "+now.In(s.loc).Format("2 Jan 2006")+", which can't be right. Check the time server (NTP).")
	}
	return hc("clock", "Clock", "ok", "Looks right: "+now.In(s.loc).Format("Mon 2 Jan 15:04"))
}

func (s *State) checkStorage() HealthCheck {
	var db int64
	for _, p := range []string{s.cfg.DB, s.cfg.DB + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			db += st.Size()
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	mem := m.Sys - m.HeapReleased
	parts := []string{fmt.Sprintf("Database %s", mb(db))}
	status := "ok"
	if free, total, err := diskSpace(filepath.Dir(s.cfg.DB)); err == nil && total > 0 {
		pct := float64(free) / float64(total) * 100
		parts = append(parts, fmt.Sprintf("disk %.0f%% free", pct))
		switch {
		case free < 100<<20:
			status = "bad"
			parts[len(parts)-1] += " (almost full: usage can't be saved soon)"
		case pct < 10 || free < 500<<20:
			status = "warn"
		}
	}
	parts = append(parts, "memory "+mb(int64(mem)))
	return hc("storage", "Storage", status, strings.Join(parts, " · "))
}

func mb(b int64) string {
	if b >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(b)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(b)/1e6)
}

// healthChecks runs every check, or returns the last result if it is fresh.
func (s *State) healthChecks(ctx context.Context) ([]HealthCheck, time.Time) {
	h := &s.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < healthEvery {
		return append([]HealthCheck(nil), h.checks...), h.at
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rs, rd := rulesCheck()
	checks := []HealthCheck{
		s.checkRouting(ctx),
		hc("rules", "Routing rules", rs, rd),
		s.checkCounting(),
		s.checkExits(),
		s.checkBackup(),
		s.checkDHCP(),
		s.checkClock(now),
		s.checkStorage(),
	}
	if h.since == nil {
		h.since = map[string]healthSince{}
	}
	for i := range checks {
		c := &checks[i]
		prev, seen := h.since[c.Key]
		if !seen || prev.status != c.Status {
			at := time.Time{}
			if seen {
				at = now
			}
			prev = healthSince{status: c.Status, at: at}
			h.since[c.Key] = prev
		}
		if c.Since == nil && !prev.at.IsZero() {
			t := prev.at.UTC().Format(time.RFC3339)
			c.Since = &t
		}
	}
	h.checks, h.at = checks, now
	return append([]HealthCheck(nil), checks...), now
}

func (s *State) healthNow(ctx context.Context) Health {
	checks, at := s.healthChecks(ctx)
	return Health{CheckedAt: at.UTC().Format(time.RFC3339), Checks: checks, Version: s.versionInfo()}
}

func (a *API) getHealth(w http.ResponseWriter, r *http.Request) {
	send(w, a.s.healthNow(r.Context()))
}
