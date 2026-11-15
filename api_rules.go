package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *API) rulesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/devices/{id}/profile", a.putDeviceProfile)
	mux.HandleFunc("PUT /api/devices/{id}/speed", a.putDeviceSpeed)
	mux.HandleFunc("GET /api/rules", a.getRules)
	mux.HandleFunc("POST /api/rules", a.postRule)
	mux.HandleFunc("PUT /api/rules/order", a.putRuleOrder)
	mux.HandleFunc("PUT /api/rules/{id}", a.putRule)
	mux.HandleFunc("DELETE /api/rules/{id}", a.deleteRule)
	mux.HandleFunc("POST /api/rules/{id}/exempt", a.postExempt)
	mux.HandleFunc("DELETE /api/rules/{id}/exempt/{device}", a.deleteExempt)
	mux.HandleFunc("GET /api/simulate", a.simulate)
	mux.HandleFunc("GET /api/events", a.events)
}

func (a *API) putDeviceProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Profile *string `json:"profile"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil {
		fail(w, 400, `The request body must be JSON like {"profile": "gaming"} or {"profile": null}.`)
		return
	}
	p := ""
	if body.Profile != nil {
		if !validProfile(*body.Profile) {
			fail(w, 400, "The profile must be "+profileChoices()+" (or null to follow the default).")
			return
		}
		p = *body.Profile
	}
	if n := a.changeDevices(w, r, func(d *devState) bool { return d.ID == id }, func(d *devState) { d.Profile = p }); n == 0 {
		fail(w, 404, "No device with that id.")
	} else if n > 0 {
		a.sendDevice(w, id)
	}
}

func (a *API) putDeviceSpeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Mbps *float64 `json:"mbps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil {
		fail(w, 400, `The request body must be JSON like {"mbps": 5} or {"mbps": null}.`)
		return
	}
	v := 0.0
	if body.Mbps != nil {
		if *body.Mbps < 0.5 || *body.Mbps > 1000 {
			fail(w, 400, "A speed limit must be between 0.5 and 1000 Mbit/s.")
			return
		}
		v = *body.Mbps
	}
	if n := a.changeDevices(w, r, func(d *devState) bool { return d.ID == id }, func(d *devState) { d.SpeedLimit = v }); n == 0 {
		fail(w, 404, "No device with that id.")
	} else if n > 0 {
		a.sendDevice(w, id)
	}
}

func (a *API) getRules(w http.ResponseWriter, r *http.Request) {
	a.s.mu.RLock()
	out := append([]Rule{}, a.s.rules...)
	a.s.mu.RUnlock()
	send(w, out)
}

func validateRule(r *Rule) string {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 60 {
		return "Give the rule a name (up to 60 characters)."
	}
	m := r.Match
	if !m.All && len(m.Devices) == 0 && len(m.Groups) == 0 && !m.NewDevices {
		return "Choose who the rule is for: all devices, groups, devices or new devices."
	}
	if sc := r.When.Schedule; sc != nil {
		if len(sc.Days) == 0 {
			return "A schedule needs at least one day."
		}
		for _, d := range sc.Days {
			if d < 0 || d > 6 {
				return "Days are 0 (Sunday) to 6 (Saturday)."
			}
		}
		s, ok1 := parseHM(sc.Start)
		e, ok2 := parseHM(sc.End)
		if !ok1 || !ok2 || s == e {
			return "A schedule needs a start and an end time like 22:00, and they can't be the same."
		}
	}
	if u := r.When.Usage; u != nil {
		if u.Period != "day" && u.Period != "week" && u.Period != "month" {
			return "The usage period must be day, week or month."
		}
		if u.OverGB <= 0 || u.OverGB > 100000 {
			return "The data amount must be more than 0 GB."
		}
	}
	t := r.Then
	if t.Pause == nil && t.SpeedMbps == nil && t.VPN == nil && t.Profile == nil {
		return "Choose what the rule does: pause, slow down, VPN on/off or a VPN profile."
	}
	if t.SpeedMbps != nil && (*t.SpeedMbps < 0.5 || *t.SpeedMbps > 1000) {
		return "A speed limit must be between 0.5 and 1000 Mbit/s."
	}
	if t.Profile != nil && !validProfile(*t.Profile) {
		return "The profile must be " + profileChoices() + "."
	}
	return ""
}

func (a *API) readRule(w http.ResponseWriter, r *http.Request) (Rule, bool) {
	var rule Rule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&rule); err != nil {
		fail(w, 400, "The rule must be JSON as described in API.md: "+err.Error())
		return rule, false
	}
	if msg := validateRule(&rule); msg != "" {
		fail(w, 400, msg)
		return rule, false
	}
	return rule, true
}

func (a *API) postRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := a.readRule(w, r)
	if !ok {
		return
	}
	rule.ID = 0
	saved, err := a.s.saveRule(rule)
	if err != nil {
		fail(w, 500, "Could not save the rule: "+err.Error())
		return
	}
	a.s.store.addEvent(time.Now(), "rule", "", saved.ID, fmt.Sprintf("Rule %q created", saved.Name))
	send(w, saved)
}

func (a *API) ruleByID(id int64) (Rule, bool) {
	a.s.mu.RLock()
	defer a.s.mu.RUnlock()
	for _, x := range a.s.rules {
		if x.ID == id {
			return x, true
		}
	}
	return Rule{}, false
}

func (a *API) putRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	old, found := a.ruleByID(id)
	if !found {
		fail(w, 404, "No rule with that id.")
		return
	}
	rule, ok := a.readRule(w, r)
	if !ok {
		return
	}
	rule.ID, rule.Order = id, old.Order
	saved, err := a.s.saveRule(rule)
	if err != nil {
		fail(w, 500, "Could not save the rule: "+err.Error())
		return
	}
	what := "changed"
	if old.Enabled != saved.Enabled {
		what = map[bool]string{true: "turned on", false: "turned off"}[saved.Enabled]
	}
	a.s.store.addEvent(time.Now(), "rule", "", id, fmt.Sprintf("Rule %q %s", saved.Name, what))
	send(w, saved)
}

func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	old, found := a.ruleByID(id)
	if !found {
		fail(w, 404, "No rule with that id.")
		return
	}
	if err := a.s.store.deleteRule(id); err != nil {
		fail(w, 500, "Could not delete the rule: "+err.Error())
		return
	}
	a.s.loadRules()
	a.s.recompute()
	a.s.store.addEvent(time.Now(), "rule", "", id, fmt.Sprintf("Rule %q deleted", old.Name))
	send(w, map[string]int64{"deleted": id})
}

func (a *API) putRuleOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || len(body.IDs) == 0 {
		fail(w, 400, `The request body must be JSON like {"ids": [3, 1, 2]}.`)
		return
	}
	for i, id := range body.IDs {
		rule, found := a.ruleByID(id)
		if !found {
			fail(w, 404, fmt.Sprintf("No rule with id %d.", id))
			return
		}
		rule.Order = i
		b, _ := json.Marshal(rule)
		if err := a.s.store.updateRule(id, i, b); err != nil {
			fail(w, 500, "Could not save the order: "+err.Error())
			return
		}
	}
	a.s.loadRules()
	a.s.recompute()
	a.getRules(w, r)
}

func (a *API) postExempt(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule, found := a.ruleByID(id)
	if !found {
		fail(w, 404, "No rule with that id.")
		return
	}
	var body struct {
		DeviceID string `json:"deviceId"`
		Minutes  int    `json:"minutes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.DeviceID == "" {
		fail(w, 400, `The request body must be JSON like {"deviceId": "...", "minutes": 60}.`)
		return
	}
	if body.Minutes < 1 || body.Minutes > 1440 {
		fail(w, 400, "An exemption must last between 1 minute and 24 hours.")
		return
	}
	a.s.mu.RLock()
	d, ok := a.s.devs[body.DeviceID]
	name := ""
	if ok {
		name = displayName(d.devRow)
	}
	a.s.mu.RUnlock()
	if !ok {
		fail(w, 404, "No device with that id.")
		return
	}
	until := time.Now().Add(time.Duration(body.Minutes) * time.Minute)
	if err := a.s.store.setExemption(id, body.DeviceID, until.Unix()); err != nil {
		fail(w, 500, "Could not save the exemption: "+err.Error())
		return
	}
	a.s.loadRules()
	a.s.recompute()
	a.s.closeDeviceConnections(map[string]bool{d.IP: true})
	a.s.store.addEvent(time.Now(), "rule", body.DeviceID, id,
		fmt.Sprintf("%s: %q skipped until %s", name, rule.Name, until.In(a.s.loc).Format("15:04")))
	send(w, a.s.effectiveOf(body.DeviceID))
}

func (a *API) deleteExempt(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	dev := r.PathValue("device")
	if err := a.s.store.setExemption(id, dev, 0); err != nil {
		fail(w, 500, "Could not end the exemption: "+err.Error())
		return
	}
	a.s.loadRules()
	a.s.recompute()
	send(w, a.s.effectiveOf(dev))
}

// simulate: what would a device's state be at a given time? Usage conditions
// use the usage so far (the future can't be known).
func (a *API) simulate(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("device")
	at := time.Now()
	if v := r.URL.Query().Get("at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fail(w, 400, "at must look like 2026-11-14T23:00:00+02:00.")
			return
		}
		at = t
	}
	globalProfile := a.s.globalProfile()
	a.s.mu.RLock()
	d, ok := a.s.devs[id]
	if !ok {
		a.s.mu.RUnlock()
		fail(w, 404, "No device with that id.")
		return
	}
	e := a.s.effectiveFor(id, d, at, a.s.periodUsage[id], globalProfile, a.s.selectors)
	a.s.mu.RUnlock()
	send(w, map[string]any{"effective": e, "matched": e.Rules})
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	ev, err := a.s.store.listEvents(limit, q.Get("device"), before)
	if err != nil {
		fail(w, 500, "Could not read events: "+err.Error())
		return
	}
	if kind := q.Get("kind"); kind != "" {
		filtered := []Event{}
		for _, e := range ev {
			if e.Kind == kind {
				filtered = append(filtered, e)
			}
		}
		ev = filtered
	}
	send(w, ev)
}
