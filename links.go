package main

// Linked devices: one physical device that shows up with several connections
// (a phone using two Wi-Fi links at once, each with its own MAC). The primary
// holds the settings; secondaries follow its effective state and their usage
// is added to it. LinkTo "-" means "never link automatically" (user unlinked it).

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

// linkTarget returns the primary a device is linked to, or "". Caller holds mu.
func (s *State) linkTarget(d *devState) string {
	if d.LinkTo == "" || d.LinkTo == "-" || d.LinkTo == d.ID {
		return ""
	}
	p, ok := s.devs[d.LinkTo]
	if !ok || (p.LinkTo != "" && p.LinkTo != "-") {
		return "" // dangling or chained links are ignored
	}
	return d.LinkTo
}

var genericHosts = map[string]bool{"android": true, "iphone": true, "ipad": true, "galaxy": true,
	"localhost": true, "unknown": true, "espressif": true, "macbook": true, "laptop": true, "desktop": true, "phone": true}

// hostKey normalises a DHCP hostname so one device's names match:
// "Alex-s-S26-Ultra" and "alex-s26-ultra" -> "alex s26 ultra". "" = too generic to link on.
func hostKey(h string) string {
	h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), ".local"))
	var toks []string
	for _, t := range strings.FieldsFunc(h, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if t == "s" { // possessive: "Alex-s-S26"
			continue
		}
		toks = append(toks, t)
	}
	k := strings.Join(toks, " ")
	flat := strings.ReplaceAll(k, " ", "")
	if len(flat) < 8 || genericHosts[k] || reRandomAndroid.MatchString(strings.ReplaceAll(k, " ", "-")) {
		return ""
	}
	return k
}

// autoLink links connections of one physical device. hosts: MAC -> DHCP hostname.
// Two signals: the same (non-generic) hostname, e.g. a phone on two Wi-Fi links;
// or factory MACs that differ only slightly in the last byte, e.g. a console's
// cable and Wi-Fi ports (68:6c:e6:1f:b7:d6 and ...:d9).
func (s *State) autoLink(hosts map[string]string) {
	groups := map[string][]string{}
	for mac, h := range hosts {
		if k := hostKey(h); k != "" {
			groups[k] = append(groups[k], mac)
		}
	}
	s.mu.Lock()
	var events []func()
	for _, macs := range groups {
		events = append(events, s.linkMembers(macs, "is another connection of")...)
	}
	for _, pair := range s.adjacentMACs() {
		events = append(events, s.linkMembers(pair, "(next hardware address) is another connection of")...)
	}
	s.mu.Unlock()
	for _, f := range events {
		f()
	}
}

// adjacentMACs finds pairs of factory (not random) MACs that share the first five
// bytes and whose last bytes are at most 4 apart. Caller holds mu.
func (s *State) adjacentMACs() [][]string {
	byPrefix := map[string][]string{}
	for id, d := range s.devs {
		if d.Mac == "" || d.Mac != id || len(id) != 17 || randomMAC(id) {
			continue
		}
		byPrefix[id[:14]] = append(byPrefix[id[:14]], id)
	}
	var out [][]string
	for _, ids := range byPrefix {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, errA := strconv.ParseUint(ids[i][15:], 16, 8)
				b, errB := strconv.ParseUint(ids[j][15:], 16, 8)
				if errA == nil && errB == nil && (a-b <= 4 || b-a <= 4) {
					out = append(out, []string{ids[i], ids[j]})
				}
			}
		}
	}
	return out
}

// linkMembers links the given device ids under one primary (the one you named or
// grouped, else the oldest). Never touches a device the user unlinked ("-").
// Caller holds mu; returns event writers to run after unlocking.
func (s *State) linkMembers(ids []string, how string) []func() {
	var members []*devState
	for _, m := range ids {
		if d, ok := s.devs[m]; ok && d.LinkTo != "-" {
			members = append(members, d)
		}
	}
	if len(members) < 2 {
		return nil
	}
	// a device already linked elsewhere keeps that link; its primary joins instead
	for i, d := range members {
		if p := s.linkTarget(d); p != "" {
			members[i] = s.devs[p]
		}
	}
	primary := members[0]
	for _, d := range members[1:] {
		pd := primary.Name != "" || primary.Group != ""
		dd := d.Name != "" || d.Group != ""
		if (dd && !pd) || (dd == pd && d.FirstSeen.Before(primary.FirstSeen)) {
			primary = d
		}
	}
	var events []func()
	for _, d := range members {
		if d == primary || d.LinkTo == primary.ID {
			continue
		}
		d.LinkTo, d.dirty = primary.ID, true
		for _, o := range s.devs { // anything linked to d moves to the primary
			if o.LinkTo == d.ID {
				o.LinkTo, o.dirty = primary.ID, true
			}
		}
		text := displayName(d.devRow) + " (" + d.IP + ") " + how + " " + displayName(primary.devRow) + ": linked"
		devID := primary.ID
		events = append(events, func() { s.store.addEvent(time.Now(), "device", devID, 0, text) })
	}
	if primary.LinkTo != "" && primary.LinkTo != "-" {
		primary.LinkTo, primary.dirty = "", true
	}
	return events
}
