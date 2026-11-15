package main

// Per-device routing policy, enforced by sing-box through two local rule-sets
// that it reloads by itself within a second of a change (no restart):
//   /etc/sing-box/vpn-devices.json    sources that follow the VPN setting
//   /etc/sing-box/paused-devices.json sources whose traffic is rejected
// Everything else goes direct. Junction owns both files.

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"sort"
	"time"
)

const (
	vpnListPath    = "/etc/sing-box/vpn-devices.json"
	pausedListPath = "/etc/sing-box/paused-devices.json"
	pauseForever   = int64(4102444800) // 2100-01-01: "until resumed"
	// A rule-set needs at least one entry; this documentation-only address matches nothing.
	matchNothing = "192.0.2.255/32"
)

func pausedUntil(until int64, now time.Time) *string {
	if until <= now.Unix() || until >= pauseForever {
		return nil
	}
	s := time.Unix(until, 0).UTC().Format(time.RFC3339)
	return &s
}

func writeRuleSet(path string, cidrs []string) error {
	if len(cidrs) == 0 {
		cidrs = []string{matchNothing}
	}
	sort.Strings(cidrs)
	b, _ := json.MarshalIndent(map[string]any{
		"version": 2,
		"rules":   []map[string]any{{"source_ip_cidr": cidrs}},
	}, "", "  ")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	// Written in place: sing-box watches this path.
	return os.WriteFile(path, b, 0o644)
}

// applyPolicy writes sing-box's lists and the speed caps from every device's
// effective state (see rules.go). Caller must not hold mu.
//
// When sing-box has per-profile selectors (vpn-psn, vpn-moe, vpn-gaming), VPN
// devices go into profile-<name>.json and vpn-devices.json stays empty.
// Without them, every VPN device stays in
// vpn-devices.json (the global selector), so nothing breaks mid-upgrade.
func (s *State) applyPolicy() {
	// The gateway's own health checks (127.0.0.1 via :2080) must go direct: they test
	// that sing-box forwards traffic. Through the VPN, a dead exit looked like a broken
	// gateway and handed .4 to the backup (seen in a real VPN outage).
	var vpn []string
	var paused []string
	byProfile := map[string][]string{}
	caps := map[string]float64{}
	s.mu.RLock()
	perProfile := perProfileSelectors()
	for id, d := range s.devs {
		if d.IP == "" || s.selfIP[d.IP] {
			continue
		}
		e, ok := s.eff[id]
		if !ok {
			continue
		}
		cidr := d.IP + "/32"
		if e.Paused {
			paused = append(paused, cidr)
			continue
		}
		if e.VPN {
			if perProfile {
				byProfile[e.Profile] = append(byProfile[e.Profile], cidr)
			} else {
				vpn = append(vpn, cidr)
			}
		}
		if e.SpeedLimitMbps != nil {
			caps[d.IP] = *e.SpeedLimitMbps
		}
	}
	s.mu.RUnlock()
	write := func(path string, list []string) {
		if err := writeRuleSet(path, list); err != nil {
			log.Printf("policy: %v", err)
		}
	}
	// Write the new lists before shrinking the old one, so a device is never in neither.
	if perProfile { // before sing-box has been read, the profile names are only a guess
		for _, p := range currentProfiles() {
			write(profileListPath(p), byProfile[p])
		}
	}
	write(vpnListPath, vpn)
	write(pausedListPath, paused)
	s.shaper.Apply(caps)
}

// importPolicy runs once: devices already listed in vpn-devices.json (written by
// hand before Junction managed it) are marked VPN in the database.
func (s *State) importPolicy() {
	if _, done := s.store.getSetting("policy_imported"); done {
		return
	}
	b, err := os.ReadFile(vpnListPath)
	if err == nil {
		var rs struct {
			Rules []struct {
				SourceIPCIDR []string `json:"source_ip_cidr"`
			} `json:"rules"`
		}
		if json.Unmarshal(b, &rs) == nil {
			listed := map[string]bool{}
			for _, r := range rs.Rules {
				for _, c := range r.SourceIPCIDR {
					listed[c] = true
				}
			}
			s.mu.Lock()
			found := map[string]bool{}
			for _, d := range s.devs {
				if listed[d.IP+"/32"] {
					found[d.IP+"/32"] = true
					if !d.VPN {
						d.VPN, d.dirty = true, true
					}
				}
			}
			now := time.Now()
			for c := range listed {
				ip := c[:len(c)-3]
				if found[c] || c == "127.0.0.1/32" || s.selfIP[ip] {
					continue
				}
				d := s.device("ip:"+ip, ip, "", now)
				d.VPN, d.LastSeen, d.dirty = true, time.Unix(0, 0), true
			}
			s.mu.Unlock()
		}
	}
	s.flush()
	s.store.setSetting("policy_imported", "1")
}
