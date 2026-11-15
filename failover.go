package main

// Failover: Junction's replacement for vpn-failoverd.
//
// It probes every endpoint x profile through sing-box's local probe inbounds,
// backs off on dead exits (every 10 s while healthy, up to every 5 min while
// down, so a dead server can't pile up connections), picks the sing-box
// "vpn-active" outbound according to the mode, and keeps the old daemon's
// state.json and :7070/api/status shapes so the host watchdog and anything
// else reading them keep working.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type foEndpoint struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	IP         string         `json:"ip"`
	ProbePorts map[string]int `json:"probe_ports"`
}

type foConfig struct {
	Listen           string            `json:"listen"`
	ProbeURL         string            `json:"probe_url"`
	ProbeInterval    int               `json:"probe_interval_seconds"`
	ProbeTimeout     int               `json:"probe_timeout_seconds"`
	FailureThreshold int               `json:"failure_threshold"`
	SuccessThreshold int               `json:"success_threshold"`
	RecoveryCooldown int               `json:"recovery_cooldown_seconds"`
	StatePath        string            `json:"state_path"`
	DefaultProfile   string            `json:"default_profile"`
	Profiles         map[string]string `json:"profiles"`
	Endpoints        []foEndpoint      `json:"endpoints"`
}

type probe struct {
	known, healthy        bool
	fails, oks            int
	latency               time.Duration
	lastSuccess, lastFail time.Time
	lastErr               string
	next                  time.Time
	running               bool
}

type Failover struct {
	cfg   foConfig
	s     *State
	mu    sync.Mutex
	pr    map[string]*probe // tag "vpn1-default" -> probe
	mode  string            // auto | manual | direct
	prof  string
	man   string // manual endpoint key
	sel   string // what we last set (or found) on vpn-active
	since time.Time
	why   string

	allDown time.Time            // when "every exit is down" was first seen (auto mode)
	subs    map[string]*selState // per-profile selectors
}

const maxBackoff = 5 * time.Minute

func loadFailover(path string, s *State) (*Failover, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := foConfig{ProbeURL: "http://cp.cloudflare.com", ProbeInterval: 10, ProbeTimeout: 3, FailureThreshold: 3,
		SuccessThreshold: 6, RecoveryCooldown: 600, StatePath: "/var/lib/vpn-failover/state.json", DefaultProfile: "psn"}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if len(c.Endpoints) == 0 {
		return nil, fmt.Errorf("%s lists no endpoints", path)
	}
	f := &Failover{cfg: c, s: s, pr: map[string]*probe{}, mode: "auto", prof: c.DefaultProfile, man: c.Endpoints[0].Key, subs: map[string]*selState{}}
	for _, e := range c.Endpoints {
		for p := range e.ProbePorts {
			f.pr[e.Key+"-"+p] = &probe{}
		}
	}
	// Carry on from the old daemon's last state, so a manual "direct" stays direct.
	if b, err := os.ReadFile(c.StatePath); err == nil {
		var st struct {
			Mode, Profile, ManualEndpoint, SelectedTag, LastReason string
			LastSwitch                                             time.Time
		}
		var raw map[string]any
		if json.Unmarshal(b, &raw) == nil {
			st.Mode, _ = raw["mode"].(string)
			st.Profile, _ = raw["profile"].(string)
			st.ManualEndpoint, _ = raw["manual_endpoint"].(string)
			st.SelectedTag, _ = raw["selected_tag"].(string)
			st.LastReason, _ = raw["last_reason"].(string)
			if v, ok := raw["last_switch"].(string); ok {
				st.LastSwitch, _ = time.Parse(time.RFC3339Nano, v)
			}
			if st.Mode == "auto" || st.Mode == "manual" || st.Mode == "direct" {
				f.mode = st.Mode
			}
			if _, ok := c.Profiles[st.Profile]; ok {
				f.prof = st.Profile
			}
			if f.endpoint(st.ManualEndpoint) != nil {
				f.man = st.ManualEndpoint
			}
			f.sel, f.since, f.why = st.SelectedTag, st.LastSwitch, st.LastReason
			f.restoreHealth(raw)
		}
	}
	return f, nil
}

func (f *Failover) endpoint(key string) *foEndpoint {
	for i := range f.cfg.Endpoints {
		if f.cfg.Endpoints[i].Key == key {
			return &f.cfg.Endpoints[i]
		}
	}
	return nil
}

func (f *Failover) run(ctx context.Context) {
	if f.cfg.Listen != "" {
		go f.serveCompat(ctx)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastWrite := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			f.startDueProbes(ctx, now)
			f.decide(ctx, now)
			if now.Sub(lastWrite) >= 10*time.Second {
				f.writeState(now)
				f.publish()
				lastWrite = now
			}
		}
	}
}

func (f *Failover) startDueProbes(ctx context.Context, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.cfg.Endpoints {
		for prof, port := range e.ProbePorts {
			p := f.pr[e.Key+"-"+prof]
			if p.running || now.Before(p.next) {
				continue
			}
			p.running = true
			go f.probe(ctx, p, port)
		}
	}
}

func (f *Failover) probe(ctx context.Context, p *probe, port int) {
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: time.Duration(f.cfg.ProbeTimeout) * time.Second}
	start := time.Now()
	var err error
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.cfg.ProbeURL, nil)
	resp, err := cl.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	lat := time.Since(start)

	f.s.mu.RLock()
	singboxStale := time.Since(f.s.lastClashOK) > 2500*time.Millisecond
	f.s.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	every := time.Duration(f.cfg.ProbeInterval) * time.Second
	if err != nil && singboxStale {
		// sing-box itself is stuck: the exit may be fine. Don't count it; look again soon.
		p.running = false
		p.next = now.Add(2 * time.Second)
		return
	}
	p.running, p.known = false, true
	if err == nil {
		p.oks++
		p.fails = 0
		p.latency, p.lastSuccess, p.lastErr = lat, now, ""
		if p.healthy || p.oks >= f.cfg.SuccessThreshold || p.lastFail.IsZero() {
			p.healthy = true
		}
		// While recovering, keep probing at the normal pace.
		p.next = now.Add(every)
		return
	}
	p.fails++
	p.oks = 0
	p.lastFail, p.lastErr = now, err.Error()
	if p.fails >= f.cfg.FailureThreshold {
		p.healthy = false
	}
	// Back off on a dead exit: 10 s, 20 s, 40 s ... up to 5 min.
	wait := every
	if extra := p.fails - f.cfg.FailureThreshold; extra > 0 {
		wait = every << min(extra, 5)
	}
	p.next = now.Add(min(wait, maxBackoff))
}

// selState is one sing-box selector Junction steers: "vpn-active" (the global
// profile) and one per profile ("vpn-psn", "vpn-moe", "vpn-gaming").
type selState struct {
	sel     string
	since   time.Time
	why     string
	allDown time.Time
}

// desiredFor returns the outbound the mode calls for on one profile's selector.
func (f *Failover) desiredFor(prof string, st *selState, now time.Time) (string, string) {
	switch f.mode {
	case "direct":
		return "direct", "manual control"
	case "manual":
		return f.man + "-" + prof, "manual control"
	}
	healthy := func(key string) *probe {
		if p := f.pr[key+"-"+prof]; p != nil && p.healthy {
			return p
		}
		return nil
	}
	cur := ""
	if e := f.endpoint(endpointKey(st.sel)); e != nil && st.sel != "direct" {
		cur = e.Key
	}
	// Prefer earlier endpoints, but only move back to one after it has proven
	// itself and the cooldown since the last switch has passed.
	for _, e := range f.cfg.Endpoints {
		p := healthy(e.Key)
		if p == nil {
			continue
		}
		if e.Key == cur {
			return st.sel, st.why
		}
		if cur != "" && healthy(cur) != nil {
			if p.oks < f.cfg.SuccessThreshold || now.Sub(st.since) < time.Duration(f.cfg.RecoveryCooldown)*time.Second {
				continue
			}
			return e.Key + "-" + prof, e.Name + " is healthy again"
		}
		if cur == "" {
			return e.Key + "-" + prof, e.Name + " is the first healthy exit"
		}
		return e.Key + "-" + prof, f.endpoint(cur).Name + " stopped answering"
	}
	if cur != "" && healthy(cur) != nil {
		return st.sel, st.why
	}
	return "direct", "every exit is down"
}

func endpointKey(tag string) string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == '-' {
			return tag[:i]
		}
	}
	return tag
}

func (f *Failover) decide(ctx context.Context, now time.Time) {
	f.s.mu.RLock()
	stale := time.Since(f.s.lastClashOK) > 2500*time.Millisecond
	actual := map[string]string{}
	for k, v := range f.s.selectors {
		actual[k] = v
	}
	f.s.mu.RUnlock()
	if stale {
		return // sing-box isn't answering; the gateway health check deals with that
	}

	type target struct {
		name, prof string
		st         *selState
	}
	f.mu.Lock()
	main := &selState{sel: f.sel, since: f.since, why: f.why, allDown: f.allDown}
	targets := []target{{"vpn-active", f.prof, main}}
	for _, p := range currentProfiles() {
		name := "vpn-" + p
		if _, exists := actual[name]; !exists {
			continue // that selector isn't in sing-box's config
		}
		if f.subs[name] == nil {
			f.subs[name] = &selState{sel: actual[name]}
		}
		targets = append(targets, target{name, p, f.subs[name]})
	}
	type change struct {
		name, from, want, why string
		st                    *selState
	}
	var changes []change
	for _, t := range targets {
		// In auto mode wait until this profile has been probed on every endpoint.
		if f.mode == "auto" {
			ready := true
			for _, e := range f.cfg.Endpoints {
				if p := f.pr[e.Key+"-"+t.prof]; p != nil && !p.known {
					ready = false
				}
			}
			if !ready {
				continue
			}
		}
		want, why := f.desiredFor(t.prof, t.st, now)
		if why == "every exit is down" && f.mode == "auto" {
			if t.st.allDown.IsZero() {
				t.st.allDown = now
			}
			if now.Sub(t.st.allDown) < 20*time.Second {
				continue
			}
		} else {
			t.st.allDown = time.Time{}
		}
		cur, known := actual[t.name]
		if !known || cur == "" {
			continue // haven't read that selector yet
		}
		if cur == want {
			if t.st.sel != want {
				t.st.sel, t.st.since, t.st.why = want, now, why
			}
			continue
		}
		changes = append(changes, change{t.name, cur, want, why, t.st})
	}
	f.sel, f.since, f.why, f.allDown = main.sel, main.since, main.why, main.allDown
	f.mu.Unlock()

	for _, c := range changes {
		body, _ := json.Marshal(map[string]string{"name": c.want})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, f.s.cfg.ClashAPI+"/proxies/"+c.name, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+f.s.secret)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpc.Do(req)
		if err != nil || resp.StatusCode/100 != 2 {
			if err == nil {
				resp.Body.Close()
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			log.Printf("failover: switching %s to %s failed: %v", c.name, c.want, err)
			continue
		}
		resp.Body.Close()
		log.Printf("failover: %s %s -> %s (%s)", c.name, c.from, c.want, c.why)
		f.s.store.addEvent(now, "exit", "", 0, exitEventText(c.name, c.from, c.want, c.why))
		f.s.mu.Lock()
		f.s.selectors[c.name] = c.want
		if c.name == "vpn-active" {
			f.s.selector.Now = c.want
		}
		f.s.mu.Unlock()
		f.mu.Lock()
		c.st.sel, c.st.since, c.st.why = c.want, now, c.why
		if c.name == "vpn-active" {
			f.sel, f.since, f.why = c.want, now, c.why
		}
		f.mu.Unlock()
	}
}

func exitEventText(name, from, want, why string) string {
	who := "VPN"
	if name != "vpn-active" {
		who = "VPN (" + strings.TrimPrefix(name, "vpn-") + " profile)"
	}
	if why == "manual control" {
		return fmt.Sprintf("%s switched to %s", who, want)
	}
	return fmt.Sprintf("%s moved from %s to %s: %s", who, from, want, why)
}

// set changes the mode from the API.
func (f *Failover) set(mode, endpoint, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if profile != "" {
		if _, ok := f.cfg.Profiles[profile]; !ok {
			return fmt.Errorf("unknown profile %q", profile)
		}
		f.prof = profile
	}
	switch mode {
	case "auto", "direct":
	case "manual":
		if endpoint == "" {
			endpoint = f.man
		}
		e := f.endpoint(endpoint)
		if e == nil {
			return fmt.Errorf("unknown exit %q", endpoint)
		}
		if p := f.pr[e.Key+"-"+f.prof]; p == nil || !p.healthy {
			return fmt.Errorf("%s isn't answering on the %s profile right now, so switching everyone to it would cut them off", e.Name, f.prof)
		}
		f.man = endpoint
	default:
		return fmt.Errorf("mode must be auto, manual or direct")
	}
	f.mode = mode
	return nil
}

// ---------- outputs ----------

func (f *Failover) healthJSON(p *probe) map[string]any {
	h := map[string]any{
		"known": p.known, "healthy": p.healthy,
		"consecutive_failures": p.fails, "consecutive_successes": p.oks,
		"latency_ms":   p.latency.Milliseconds(),
		"last_success": zeroTime(p.lastSuccess), "last_failure": zeroTime(p.lastFail),
	}
	if p.lastErr != "" {
		h["last_error"] = p.lastErr
	}
	return h
}

func zeroTime(t time.Time) string {
	if t.IsZero() {
		return "0001-01-01T00:00:00Z"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// compatState is the old daemon's state.json shape (the host watchdog reads it).
func (f *Failover) compatState(now time.Time) map[string]any {
	health := map[string]any{}
	for _, e := range f.cfg.Endpoints {
		if p := f.pr[e.Key+"-"+f.prof]; p != nil {
			health[e.Key] = f.healthJSON(p)
		}
	}
	return map[string]any{
		"mode": f.mode, "profile": f.prof, "manual_endpoint": f.man, "selected_tag": f.sel,
		"last_switch": zeroTime(f.since), "last_reason": f.why, "updated_at": now.UTC().Format(time.RFC3339Nano),
		"all_down_notified": false, "health": health,
	}
}

func (f *Failover) writeState(now time.Time) {
	f.mu.Lock()
	b, _ := json.MarshalIndent(f.compatState(now), "", "  ")
	f.mu.Unlock()
	tmp := f.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		os.Rename(tmp, f.cfg.StatePath)
	}
}

// publish hands the same view to Junction's own API (replaces polling :7070).
func (f *Failover) publish() {
	f.mu.Lock()
	var st failoverStatus
	for _, e := range f.cfg.Endpoints {
		st.Endpoints = append(st.Endpoints, struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		}{e.Key, e.Name})
	}
	b, _ := json.Marshal(f.compatState(time.Now()))
	json.Unmarshal(b, &st.State)
	profs := map[string][]ProfileHealth{}
	for _, e := range f.cfg.Endpoints {
		for prof := range e.ProbePorts {
			p := f.pr[e.Key+"-"+prof]
			ph := ProfileHealth{Profile: prof, Healthy: p.healthy, Known: p.known, NextCheck: p.next.UTC().Format(time.RFC3339)}
			if p.known && p.healthy {
				ms := float64(p.latency.Milliseconds())
				ph.LatencyMs = &ms
			}
			profs[e.Key] = append(profs[e.Key], ph)
		}
		sort.Slice(profs[e.Key], func(i, j int) bool { return profs[e.Key][i].Profile < profs[e.Key][j].Profile })
	}
	mode, prof, man := f.mode, f.prof, f.man
	f.mu.Unlock()

	f.s.mu.Lock()
	f.s.failover, f.s.failoverErr = &st, nil
	f.s.profiles, f.s.foMode = profs, SelectorControl{Mode: mode, Profile: prof, ManualEndpoint: man}
	f.s.mu.Unlock()
}

// serveCompat answers GET /api/status on the old daemon's address.
func (f *Failover) serveCompat(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		out := map[string]any{"endpoints": f.cfg.Endpoints, "profiles": f.cfg.Profiles, "state": f.compatState(time.Now())}
		f.mu.Unlock()
		send(w, out)
	})
	srv := &http.Server{Addr: f.cfg.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("failover status page on %s: %v", f.cfg.Listen, err)
	}
}

// restoreHealth seeds the current profile's probes from the last state.json
// (rewritten every 10 s), so a restart doesn't show every exit as down until
// the first fresh checks land a few seconds later.
func (f *Failover) restoreHealth(raw map[string]any) {
	health, _ := raw["health"].(map[string]any)
	for key, v := range health {
		h, _ := v.(map[string]any)
		p := f.pr[key+"-"+f.prof]
		if h == nil || p == nil {
			continue
		}
		if known, _ := h["known"].(bool); !known {
			continue
		}
		num := func(k string) int { n, _ := h[k].(float64); return int(n) }
		p.known = true
		p.healthy, _ = h["healthy"].(bool)
		p.fails, p.oks = num("consecutive_failures"), num("consecutive_successes")
		p.latency = time.Duration(num("latency_ms")) * time.Millisecond
		if t, err := time.Parse(time.RFC3339Nano, fmt.Sprint(h["last_success"])); err == nil && t.Year() > 1 {
			p.lastSuccess = t
		}
		if t, err := time.Parse(time.RFC3339Nano, fmt.Sprint(h["last_failure"])); err == nil && t.Year() > 1 {
			p.lastFail = t
		}
		p.lastErr, _ = h["last_error"].(string)
	}
}
