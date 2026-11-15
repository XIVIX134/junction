package main

// The rules engine. Every 15 s (and right after any change) it works out each
// device's effective state from its manual settings plus the rules that apply
// now, then hands that to sing-box (VPN / profile / pause lists) and to the
// shaper (speed caps). Changes are recorded as events.

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

var dayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

func (s *State) loadRules() error {
	rows, err := s.store.loadRules()
	if err != nil {
		return err
	}
	var rules []Rule
	for _, r := range rows {
		var rule Rule
		if err := json.Unmarshal(r.Body, &rule); err != nil {
			log.Printf("rule %d: %v", r.ID, err)
			continue
		}
		rule.ID, rule.Order = r.ID, r.Ord
		rules = append(rules, rule)
	}
	ex, err := s.store.loadExemptions()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rules, s.exempt = rules, ex
	s.mu.Unlock()
	return nil
}

func parseHM(v string) (int, bool) {
	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

func hasDay(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

// scheduleActive: is local time t inside the window? A window ending before it
// starts runs past midnight and belongs to the day it starts on.
func scheduleActive(sc *RuleSchedule, t time.Time) bool {
	start, ok1 := parseHM(sc.Start)
	end, ok2 := parseHM(sc.End)
	if !ok1 || !ok2 || start == end {
		return false
	}
	day, mins := int(t.Weekday()), t.Hour()*60+t.Minute()
	if start < end {
		return hasDay(sc.Days, day) && mins >= start && mins < end
	}
	prev := (day + 6) % 7
	return (hasDay(sc.Days, day) && mins >= start) || (hasDay(sc.Days, prev) && mins < end)
}

func scheduleText(sc *RuleSchedule) string {
	ds := append([]int(nil), sc.Days...)
	sort.Ints(ds)
	var names []string
	// collapse runs like Sun–Thu
	for i := 0; i < len(ds); {
		j := i
		for j+1 < len(ds) && ds[j+1] == ds[j]+1 {
			j++
		}
		if j-i >= 2 {
			names = append(names, dayNames[ds[i]]+"–"+dayNames[ds[j]])
		} else {
			for k := i; k <= j; k++ {
				names = append(names, dayNames[ds[k]])
			}
		}
		i = j + 1
	}
	if len(ds) == 7 {
		names = []string{"every day"}
	}
	return strings.Join(names, ", ") + " " + sc.Start + "–" + sc.End
}

// scheduleEnd returns when the window containing t ends (for "skip tonight").
func scheduleEnd(sc *RuleSchedule, t time.Time) time.Time {
	end, _ := parseHM(sc.End)
	e := time.Date(t.Year(), t.Month(), t.Day(), end/60, end%60, 0, 0, t.Location())
	if !e.After(t) {
		e = e.AddDate(0, 0, 1)
	}
	return e
}

func (s *State) periodStarts(now time.Time) (day, week, month time.Time) {
	day = s.dayStart(now)
	week = day.AddDate(0, 0, -int(day.Weekday()))
	l := now.In(s.loc)
	month = time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, s.loc)
	return
}

// usageByPeriod: per device {day, week, month} bytes (down+up).
func (s *State) usageByPeriod(now time.Time) map[string][3]int64 {
	day, week, month := s.periodStarts(now)
	from := month
	if week.Before(from) {
		from = week
	}
	out := map[string][3]int64{}
	s.sumUsage(from, now.Add(time.Hour), "", func(h int64, dev, _ string, down, up int64) {
		v := out[dev]
		b := down + up
		if h >= day.Unix() {
			v[0] += b
		}
		if h >= week.Unix() {
			v[1] += b
		}
		if h >= month.Unix() {
			v[2] += b
		}
		out[dev] = v
	})
	return out
}

func (s *State) ruleMatches(r *Rule, d *devState, now time.Time) bool {
	m := r.Match
	if m.All {
		return true
	}
	for _, id := range m.Devices {
		if id == d.ID {
			return true
		}
	}
	for _, g := range m.Groups {
		if g != "" && g == d.Group {
			return true
		}
	}
	if m.NewDevices && d.Name == "" && d.Group == "" && now.Sub(d.FirstSeen) < 24*time.Hour {
		return true
	}
	return false
}

// amount formats bytes the way people read them: "340 MB", "2.4 GB".
func amount(b int64) string {
	if b < 1e9 {
		return strconv.FormatFloat(float64(b)/1e6, 'f', 0, 64) + " MB"
	}
	return strconv.FormatFloat(float64(b)/1e9, 'f', 1, 64) + " GB"
}

// evaluate recomputes every device's effective state. Caller must not hold mu.
func (s *State) evaluate(now time.Time) {
	usage := s.usageByPeriod(now)
	globalProfile := s.globalProfile()

	s.mu.Lock()
	sels := s.selectors
	var events []func()
	next := make(map[string]Effective, len(s.devs))
	for id, d := range s.devs {
		if s.linkTarget(d) != "" {
			continue // a second connection of another device: follows it (below)
		}
		u := usage[id]
		for _, o := range s.devs { // a device's usage includes all its connections
			if s.linkTarget(o) == id {
				x := usage[o.ID]
				u = [3]int64{u[0] + x[0], u[1] + x[1], u[2] + x[2]}
			}
		}
		e := s.effectiveFor(id, d, now, u, globalProfile, sels)
		next[id] = e
		if prev, had := s.eff[id]; had && s.evaluated {
			events = append(events, s.diffEvents(d, prev, e, now)...)
		}
	}
	for id, d := range s.devs {
		if p := s.linkTarget(d); p != "" {
			next[id] = next[p]
		}
	}
	s.eff, s.evaluated, s.periodUsage = next, true, usage
	s.mu.Unlock()
	for _, f := range events {
		f()
	}
}

func ruleNames(rs []RuleRef) string {
	var n []string
	for _, r := range rs {
		n = append(n, r.Name)
	}
	return strings.Join(n, ", ")
}

func ruleIDOf(rs []RuleRef) int64 {
	if len(rs) > 0 {
		return rs[len(rs)-1].ID
	}
	return 0
}

// diffEvents describes what rules just changed for a device. Caller holds mu.
func (s *State) diffEvents(d *devState, a, b Effective, now time.Time) []func() {
	name := d.Name
	if name == "" {
		name = d.AutoName
	}
	if name == "" {
		name = "Device ." + d.IP[strings.LastIndex(d.IP, ".")+1:]
	}
	var out []func()
	add := func(kind string, rid int64, text string) {
		out = append(out, func() { s.store.addEvent(now, kind, d.ID, rid, text) })
	}
	ruleBy := func(e Effective) bool { return e.PausedBy != nil && *e.PausedBy == "rule" }
	if !ruleBy(a) && ruleBy(b) {
		add("rule", ruleIDOf(b.Rules), fmt.Sprintf("%s paused by %s", name, ruleNames(b.Rules)))
	} else if ruleBy(a) && !b.Paused {
		add("rule", ruleIDOf(a.Rules), fmt.Sprintf("%s unpaused: %s no longer applies", name, ruleNames(a.Rules)))
	}
	sp := func(e Effective) float64 {
		if e.SpeedLimitMbps == nil {
			return 0
		}
		return *e.SpeedLimitMbps
	}
	// Only rule-made speed changes here; manual ones are logged by the API as "control".
	if sp(a) != sp(b) {
		manual := d.SpeedLimit
		fromRuleB := sp(b) > 0 && sp(b) != manual && len(b.Rules) > 0
		fromRuleA := sp(a) > 0 && sp(a) != manual && len(a.Rules) > 0
		mb := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		switch {
		case fromRuleB:
			add("limit", ruleIDOf(b.Rules), fmt.Sprintf("%s slowed to %s Mbit/s (%s)", name, mb(sp(b)), ruleNames(b.Rules)))
		case fromRuleA && sp(b) > 0:
			add("limit", ruleIDOf(a.Rules), fmt.Sprintf("%s is back to its own %s Mbit/s limit", name, mb(sp(b))))
		case fromRuleA:
			add("limit", ruleIDOf(a.Rules), fmt.Sprintf("%s is back to full speed", name))
		}
	}
	if a.VPN != b.VPN && (len(a.Rules) > 0 || len(b.Rules) > 0) {
		state := "off"
		if b.VPN {
			state = "on"
		}
		add("rule", ruleIDOf(b.Rules), fmt.Sprintf("%s: VPN %s by %s", name, state, ruleNames(b.Rules)))
	}
	return out
}

// effectiveOf returns a device's last computed effective state.
func (s *State) effectiveOf(id string) Effective {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.eff[id]; ok {
		return e
	}
	return Effective{Rules: []RuleRef{}, Exemptions: []Exemption{}}
}

// recompute is what every change calls: rules -> effective -> sing-box + shaper.
func (s *State) recompute() {
	// One at a time: a 15 s tick that evaluated before a change must not apply
	// its stale result after the change was applied (that undid caps for ~15 s).
	s.recMu.Lock()
	defer s.recMu.Unlock()
	s.evaluate(time.Now())
	s.applyPolicy()
}

// ---- persistence helpers ----

func (s *State) saveRule(r Rule) (Rule, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	if r.ID == 0 {
		s.mu.RLock()
		ord := 0
		for _, x := range s.rules {
			if x.Order >= ord {
				ord = x.Order + 1
			}
		}
		s.mu.RUnlock()
		r.Order = ord
		id, err := s.store.insertRule(ord, body)
		if err != nil {
			return r, err
		}
		r.ID = id
	} else if err := s.store.updateRule(r.ID, r.Order, body); err != nil {
		return r, err
	}
	if err := s.loadRules(); err != nil {
		return r, err
	}
	s.recompute()
	return r, nil
}

// effectiveFor computes one device's state at time now. Caller holds mu (any mode).
func (s *State) effectiveFor(id string, d *devState, now time.Time, u [3]int64, globalProfile string, sels map[string]string) Effective {
	local := now.In(s.loc)
	e := Effective{VPN: d.VPN, Rules: []RuleRef{}, Exemptions: []Exemption{}}
	profile := d.Profile
	var speed float64 = d.SpeedLimit
	if d.PausedUntil > now.Unix() {
		e.Paused = true
		by := "manual"
		e.PausedBy = &by
	}
	for i := range s.rules {
		r := &s.rules[i]
		if !r.Enabled || !s.ruleMatches(r, d, now) {
			continue
		}
		var why []string
		ok := true
		if sc := r.When.Schedule; sc != nil {
			if scheduleActive(sc, local) {
				why = append(why, scheduleText(sc))
			} else {
				ok = false
			}
		}
		if us := r.When.Usage; us != nil && ok {
			idx := map[string]int{"day": 0, "week": 1, "month": 2}[us.Period]
			label := map[string]string{"day": "today", "week": "this week", "month": "this month"}[us.Period]
			used := u[idx]
			if float64(used) >= us.OverGB*1e9 {
				why = append(why, fmt.Sprintf("used %s of %s %s", amount(used), amount(int64(us.OverGB*1e9)), label))
			} else {
				ok = false
			}
		}
		if !ok {
			continue
		}
		key := [2]string{strconv.FormatInt(r.ID, 10), id}
		if until, ex := s.exempt[key]; ex && until > now.Unix() {
			e.Exemptions = append(e.Exemptions, Exemption{RuleID: r.ID, Until: time.Unix(until, 0).UTC().Format(time.RFC3339)})
			continue
		}
		if len(why) == 0 {
			why = append(why, "always")
		}
		e.Rules = append(e.Rules, RuleRef{ID: r.ID, Name: r.Name, Why: strings.Join(why, ", ")})
		t := r.Then
		if t.Pause != nil && *t.Pause && !e.Paused {
			e.Paused = true
			by := "rule"
			e.PausedBy = &by
		}
		if t.SpeedMbps != nil && *t.SpeedMbps > 0 && (speed == 0 || *t.SpeedMbps < speed) {
			speed = *t.SpeedMbps
		}
		if t.VPN != nil {
			e.VPN = *t.VPN
		}
		if t.Profile != nil && validProfile(*t.Profile) {
			profile = *t.Profile
		}
	}
	if profile == "" {
		profile = globalProfile
	}
	e.Profile = profile
	if speed > 0 {
		v := speed
		e.SpeedLimitMbps = &v
	}
	switch {
	case e.Paused:
		e.Exit = "blocked"
	case e.VPN:
		e.Exit = sels["vpn-"+profile]
		if e.Exit == "" {
			e.Exit = sels["vpn-active"]
		}
	default:
		e.Exit = "direct"
	}
	return e
}
