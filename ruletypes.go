package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// VPN profiles are sing-box's per-profile selectors, "vpn-<name>" (vpn-active
// is the global one). fallbackProfiles are used until sing-box has been read,
// and when its config has no per-profile selectors.
var fallbackProfiles = []string{"psn", "moe", "gaming"}

var (
	profilesMu    sync.RWMutex
	profilesCur   = fallbackProfiles
	profilesFound bool
)

func currentProfiles() []string {
	profilesMu.RLock()
	defer profilesMu.RUnlock()
	return profilesCur
}

// perProfileSelectors: sing-box has vpn-<profile> selectors (see policy.go).
func perProfileSelectors() bool {
	profilesMu.RLock()
	defer profilesMu.RUnlock()
	return profilesFound
}

// setProfiles takes the profile list from sing-box's selectors, in the order
// of its config file (order), alphabetical for any not in it.
func setProfiles(sels map[string]string, order []string) {
	rank := map[string]int{}
	for i, t := range order {
		rank[t] = i + 1
	}
	var ps []string
	for name := range sels {
		if p, ok := strings.CutPrefix(name, "vpn-"); ok && p != "active" && p != "" {
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		ri, rj := rank["vpn-"+ps[i]], rank["vpn-"+ps[j]]
		if ri != rj && ri > 0 && rj > 0 {
			return ri < rj
		}
		if (ri > 0) != (rj > 0) {
			return ri > 0
		}
		return ps[i] < ps[j]
	})
	profilesMu.Lock()
	defer profilesMu.Unlock()
	profilesFound = len(ps) > 0
	if profilesFound {
		profilesCur = ps
	} else {
		profilesCur = fallbackProfiles
	}
}

func validProfile(p string) bool {
	for _, x := range currentProfiles() {
		if x == p {
			return true
		}
	}
	return false
}

// profileChoices lists the profiles for an error message: "psn, moe or gaming".
func profileChoices() string {
	ps := currentProfiles()
	if len(ps) == 1 {
		return ps[0]
	}
	return strings.Join(ps[:len(ps)-1], ", ") + " or " + ps[len(ps)-1]
}

type RuleSchedule struct {
	Days  []int  `json:"days"`  // 0=Sun..6=Sat
	Start string `json:"start"` // "HH:MM", gateway time
	End   string `json:"end"`   // < Start = past midnight
}

type RuleUsage struct {
	Period string  `json:"period"` // day | week | month
	OverGB float64 `json:"overGB"`
}

type Rule struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Order   int    `json:"order"`
	Match   struct {
		All        bool     `json:"all,omitempty"`
		Devices    []string `json:"devices,omitempty"`
		Groups     []string `json:"groups,omitempty"`
		NewDevices bool     `json:"newDevices,omitempty"`
	} `json:"match"`
	When struct {
		Schedule *RuleSchedule `json:"schedule,omitempty"`
		Usage    *RuleUsage    `json:"usage,omitempty"`
	} `json:"when"`
	Then struct {
		Pause     *bool    `json:"pause,omitempty"`
		SpeedMbps *float64 `json:"speedMbps,omitempty"`
		VPN       *bool    `json:"vpn,omitempty"`
		Profile   *string  `json:"profile,omitempty"`
	} `json:"then"`
	Graph json.RawMessage `json:"graph,omitempty"`
}

type RuleRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Why  string `json:"why"`
}

type Exemption struct {
	RuleID int64  `json:"ruleId"`
	Until  string `json:"until"`
}

type Effective struct {
	VPN            bool        `json:"vpn"`
	Profile        string      `json:"profile"`
	Exit           string      `json:"exit"`
	Paused         bool        `json:"paused"`
	PausedBy       *string     `json:"pausedBy"`
	SpeedLimitMbps *float64    `json:"speedLimitMbps"`
	Rules          []RuleRef   `json:"rules"`
	Exemptions     []Exemption `json:"exemptions"`
}

type Event struct {
	ID       int64   `json:"id"`
	At       string  `json:"at"`
	Kind     string  `json:"kind"`
	DeviceID *string `json:"deviceId"`
	RuleID   *int64  `json:"ruleId"`
	Text     string  `json:"text"`
}
