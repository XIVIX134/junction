package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

type API struct{ s *State }

func (a *API) routes(mux *http.ServeMux, web fs.FS) {
	mux.HandleFunc("GET /api/overview", a.overview)
	mux.HandleFunc("GET /api/devices", a.devices)
	mux.HandleFunc("GET /api/devices/{id}", a.device)
	mux.HandleFunc("PATCH /api/devices/{id}", a.patchDevice)
	mux.HandleFunc("GET /api/exits", a.exits)
	mux.HandleFunc("GET /api/usage", a.usage)
	mux.HandleFunc("GET /api/traffic", a.traffic)
	mux.HandleFunc("GET /api/exits/history", a.exitsHistory)
	mux.HandleFunc("GET /api/categories", a.getCategories)
	mux.HandleFunc("GET /api/devices/daily", a.devicesDaily)
	mux.HandleFunc("PUT /api/selector", a.putSelector)
	a.rulesRoutes(mux)
	mux.HandleFunc("PUT /api/devices/{id}/vpn", a.putDeviceVPN)
	mux.HandleFunc("DELETE /api/devices/{id}", a.deleteDevice)
	mux.HandleFunc("PUT /api/devices/{id}/link", a.putDeviceLink)
	mux.HandleFunc("PUT /api/devices/{id}/pause", a.putDevicePause)
	mux.HandleFunc("DELETE /api/devices/{id}/pause", a.deleteDevicePause)
	mux.HandleFunc("PUT /api/groups/{name}/vpn", a.putGroupVPN)
	mux.HandleFunc("GET /api/bundle", a.getBundle)
	mux.HandleFunc("POST /api/bundle/renewals", a.addRenewal)
	mux.HandleFunc("POST /api/bundle/checks", a.addCheck)
	mux.HandleFunc("DELETE /api/bundle/events/{id}", a.deleteBundleEvent)
	mux.HandleFunc("GET /api/stream", a.stream)
	// v1.6
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("POST /api/settings/password", a.setPassword)
	mux.HandleFunc("POST /api/settings/exits/test", a.testExitHandler)
	mux.HandleFunc("POST /api/settings/exits", a.addExitHandler)
	mux.HandleFunc("DELETE /api/settings/exits/{key}", a.removeExitHandler)
	mux.HandleFunc("GET /api/backup", a.getBackup)
	mux.HandleFunc("GET /api/session", a.getSession)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/health", a.getHealth)
	mux.HandleFunc("POST /api/update", a.postUpdate)
	mux.HandleFunc("GET /api/setup", a.getSetup)
	mux.HandleFunc("POST /api/setup", a.postSetup)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "There is no such API endpoint.")
	})
	mux.Handle("/", spa(web))
}

func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := a.s.overview(time.Now())
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	send(w, ov)
}

func (a *API) devices(w http.ResponseWriter, r *http.Request) {
	d, _, err := a.s.devices(time.Now())
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	send(w, d)
}

func (a *API) device(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	now := time.Now()
	devs, _, err := a.s.devices(now)
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	var dev *Device
	for i := range devs {
		if devs[i].ID == id {
			dev = &devs[i]
		}
	}
	if dev == nil {
		fail(w, 404, "No device with that id.")
		return
	}
	det := DeviceDetail{Device: *dev, TopHosts: []HostBytes{}}

	dayStart := a.s.dayStart(now)
	hours := make([]HourUsage, 24)
	for i := range hours {
		hours[i] = HourUsage{Hour: dayStart.Add(time.Duration(i) * time.Hour).UTC().Format(time.RFC3339), ByExit: map[string]int64{}}
	}
	from := dayStart.AddDate(0, 0, -29)
	days := make([]DayUsage, 30)
	for i := range days {
		days[i] = DayUsage{Day: from.AddDate(0, 0, i).Format("2006-01-02")}
	}
	ids := []string{id} // a device's charts include its linked connections
	for _, l := range dev.Links {
		ids = append(ids, l.ID)
	}
	for _, uid := range ids {
		err = a.s.sumUsage(from, now.Add(time.Hour), uid, func(h int64, _, exit string, down, up int64) {
			t := time.Unix(h, 0).In(a.s.loc)
			if di := int(a.s.dayStart(t).Sub(from).Hours()/24 + 0.5); di >= 0 && di < 30 {
				days[di].Down += down
				days[di].Up += up
			}
			if !t.Before(dayStart) {
				if hi := int(t.Sub(dayStart).Hours()); hi >= 0 && hi < 24 {
					hours[hi].Down += down
					hours[hi].Up += up
					hours[hi].ByExit[exit] += down + up
				}
			}
		})
		if err != nil {
			fail(w, 500, "Could not read usage from the database: "+err.Error())
			return
		}
	}
	det.Hours, det.Days = hours, days
	hostSum := map[string]int64{}
	for _, uid := range ids {
		if th, err := a.s.store.topHosts(now.In(a.s.loc).Format("2006-01-02"), uid, 10); err == nil {
			for _, h := range th {
				hostSum[h.Host] += h.Bytes
			}
		}
	}
	for h, b := range hostSum {
		det.TopHosts = append(det.TopHosts, HostBytes{Host: h, Bytes: b})
	}
	sort.Slice(det.TopHosts, func(i, j int) bool { return det.TopHosts[i].Bytes > det.TopHosts[j].Bytes })
	if len(det.TopHosts) > 10 {
		det.TopHosts = det.TopHosts[:10]
	}
	send(w, det)
}

func (a *API) patchDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name  *string          `json:"name"`
		Group *json.RawMessage `json:"group"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		fail(w, 400, "The request body must be JSON like {\"name\": \"Console\"}.")
		return
	}
	a.s.mu.Lock()
	d, ok := a.s.devs[id]
	if !ok {
		a.s.mu.Unlock()
		fail(w, 404, "No device with that id.")
		return
	}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if len(n) > 40 {
			a.s.mu.Unlock()
			fail(w, 400, "Names can be at most 40 characters.")
			return
		}
		d.Name = n
	}
	if body.Group != nil {
		var g *string
		if err := json.Unmarshal(*body.Group, &g); err != nil {
			a.s.mu.Unlock()
			fail(w, 400, "Group must be a string or null.")
			return
		}
		if g == nil {
			d.Group = ""
		} else {
			d.Group = strings.TrimSpace(*g)
		}
	}
	row := d.devRow
	a.s.mu.Unlock()
	if err := a.s.store.upsertDevice(row); err != nil {
		fail(w, 500, "Could not save the device: "+err.Error())
		return
	}
	devs, _, _ := a.s.devices(time.Now())
	for _, dv := range devs {
		if dv.ID == id {
			send(w, dv)
			return
		}
	}
	fail(w, 404, "No device with that id.")
}

func (a *API) exits(w http.ResponseWriter, r *http.Request) {
	_, exitToday, err := a.s.devices(time.Now())
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	ex, sel := a.s.exitsView(exitToday)
	send(w, map[string]any{"exits": ex, "selector": sel})
}

func (a *API) usage(w http.ResponseWriter, r *http.Request) {
	n := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 || n > 400 {
			fail(w, 400, "days must be a number from 1 to 400.")
			return
		}
	}
	now := time.Now()
	from := a.s.dayStart(now).AddDate(0, 0, -(n - 1))
	out := make([]UsageDay, n)
	for i := range out {
		out[i] = UsageDay{Day: from.AddDate(0, 0, i).Format("2006-01-02"), ByExit: map[string]int64{}}
	}
	content := map[int64]int64{}
	err := a.s.sumUsage(from, now.Add(time.Hour), "", func(h int64, _, exit string, down, up int64) {
		content[h] += down + up
		t := time.Unix(h, 0).In(a.s.loc)
		i := int(a.s.dayStart(t).Sub(from).Hours()/24 + 0.5)
		if i < 0 || i >= n {
			return
		}
		if exit == "direct" {
			out[i].Direct += down + up
		} else {
			out[i].Tunnel += down + up
		}
		out[i].ByExit[exit] += down + up
	})
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	billed, _ := a.s.billed(content, from, now.Add(time.Hour))
	for h, b := range billed {
		i := int(a.s.dayStart(time.Unix(h, 0).In(a.s.loc)).Sub(from).Hours()/24 + 0.5)
		if i >= 0 && i < n {
			out[i].Billed += b
		}
	}
	send(w, out)
}

// putSelector changes the global VPN mode: auto failover, a fixed exit, or everything direct.
func (a *API) putSelector(w http.ResponseWriter, r *http.Request) {
	if a.s.fo == nil {
		fail(w, 409, "Junction isn't controlling the VPN on this gateway.")
		return
	}
	var body struct {
		Mode     string `json:"mode"`
		Endpoint string `json:"endpoint"`
		Profile  string `json:"profile"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		fail(w, 400, "The request body must be JSON with a mode: auto, manual or direct.")
		return
	}
	if err := a.s.fo.set(body.Mode, body.Endpoint, body.Profile); err != nil {
		fail(w, 400, "Can't change the VPN: "+err.Error()+".")
		return
	}
	a.s.fo.decide(r.Context(), time.Now())
	a.s.fo.publish()
	a.exits(w, r)
}

func (a *API) sendBundle(w http.ResponseWriter) {
	info, err := a.s.bundleInfo(time.Now())
	if err != nil {
		fail(w, 500, "Could not read the bundle log: "+err.Error())
		return
	}
	send(w, info)
}

func (a *API) getBundle(w http.ResponseWriter, r *http.Request) { a.sendBundle(w) }

// parseWhen reads an optional RFC 3339 time; empty means now. Future times are refused.
func parseWhen(v *string) (time.Time, string) {
	now := time.Now()
	if v == nil || *v == "" {
		return now, ""
	}
	t, err := time.Parse(time.RFC3339, *v)
	if err != nil {
		return now, "The time must look like 2026-11-13T18:00:00Z."
	}
	if t.After(now.Add(5 * time.Minute)) {
		return now, "That time is in the future."
	}
	return t, ""
}

func (a *API) addRenewal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GB        float64 `json:"gb"`
		At        *string `json:"at"`
		ExpiresAt *string `json:"expiresAt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		fail(w, 400, "The request body must be JSON like {\"gb\": 140}.")
		return
	}
	if body.GB <= 0 || body.GB > 100000 {
		fail(w, 400, "The bundle size must be a positive number of GB.")
		return
	}
	at, msg := parseWhen(body.At)
	if msg != "" {
		fail(w, 400, msg)
		return
	}
	var exp *time.Time
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil || !t.After(at) {
			fail(w, 400, "The expiry must be a time after the renewal.")
			return
		}
		exp = &t
	}
	if err := a.s.store.addBundleEvent("renewal", at, int64(body.GB*1e9), exp, a.s.hourBefore(at)); err != nil {
		fail(w, 500, "Could not save the renewal: "+err.Error())
		return
	}
	a.sendBundle(w)
}

func (a *API) addCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GBLeft *float64 `json:"gbLeft"`
		At     *string  `json:"at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.GBLeft == nil {
		fail(w, 400, "The request body must be JSON like {\"gbLeft\": 52.4}.")
		return
	}
	if *body.GBLeft < 0 || *body.GBLeft > 100000 {
		fail(w, 400, "What's left must be 0 GB or more.")
		return
	}
	at, msg := parseWhen(body.At)
	if msg != "" {
		fail(w, 400, msg)
		return
	}
	if cur, _ := a.s.bundle(time.Now()); cur == nil {
		fail(w, 400, "Log a renewal first, then what's left.")
		return
	}
	if err := a.s.store.addBundleEvent("check", at, int64(*body.GBLeft*1e9), nil, a.s.hourBefore(at)); err != nil {
		fail(w, 500, "Could not save the check: "+err.Error())
		return
	}
	a.sendBundle(w)
}

func (a *API) deleteBundleEvent(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &id); err != nil {
		fail(w, 400, "The id must be a number.")
		return
	}
	ok, err := a.s.store.deleteBundleEvent(id)
	if err != nil {
		fail(w, 500, "Could not delete the entry: "+err.Error())
		return
	}
	if !ok {
		fail(w, 404, "No bundle entry with that id.")
		return
	}
	a.sendBundle(w)
}

func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Streaming is not supported.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		ov, err := a.s.overview(time.Now())
		if err == nil {
			b, _ := json.Marshal(ov)
			if _, err := fmt.Fprintf(w, "event: overview\ndata: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		} else {
			log.Printf("stream: %v", err)
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}

// spa serves the built React app and falls back to index.html for client routes.
func spa(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := web.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(web, "index.html")
		if err != nil {
			http.Error(w, "The web app was not built into this binary.", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

// ---------- per-device controls ----------

// changeDevices applies f to the matching devices, saves them and rewrites
// sing-box's lists (sing-box picks the change up within a second).
func (a *API) changeDevices(w http.ResponseWriter, r *http.Request, match func(*devState) bool, f func(*devState)) int {
	a.s.mu.Lock()
	var rows []devRow
	var texts []string
	for _, d := range a.s.devs {
		if match(d) {
			before := d.devRow
			f(d)
			rows = append(rows, d.devRow)
			if t := controlText(before, d.devRow); t != "" {
				texts = append(texts, t)
				devID := d.ID
				defer func(t string) { a.s.store.addEvent(time.Now(), "control", devID, 0, t) }(t)
			}
		}
	}
	a.s.mu.Unlock()
	for _, row := range rows {
		if err := a.s.store.upsertDevice(row); err != nil {
			fail(w, 500, "Could not save the change: "+err.Error())
			return -1
		}
		// Audit trail: who changed what is otherwise unknowable afterwards.
		log.Printf("control: %s %s (%s) -> vpn=%v paused_until=%d (from %s)", row.ID, row.IP, row.Name, row.VPN, row.PausedUntil, clientIP(r))
	}
	a.s.recompute()
	ips := map[string]bool{}
	for _, row := range rows {
		if row.IP != "" {
			ips[row.IP] = true
		}
	}
	a.s.closeDeviceConnections(ips)
	return len(rows)
}

func (a *API) sendDevice(w http.ResponseWriter, id string) {
	devs, _, _ := a.s.devices(time.Now())
	for _, d := range devs {
		if d.ID == id {
			send(w, d)
			return
		}
	}
	fail(w, 404, "No device with that id.")
}

func (a *API) putDeviceVPN(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil || body.On == nil {
		fail(w, 400, "The request body must be JSON like {\"on\": true}.")
		return
	}
	n := a.changeDevices(w, r, func(d *devState) bool { return d.ID == id }, func(d *devState) { d.VPN = *body.On })
	if n == 0 {
		fail(w, 404, "No device with that id.")
	}
	if n > 0 {
		a.sendDevice(w, id)
	}
}

func (a *API) putDevicePause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Minutes *int `json:"minutes"` // null or absent = until resumed
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil {
			fail(w, 400, "The request body must be JSON like {\"minutes\": 60}, or empty to pause until resumed.")
			return
		}
	}
	until := pauseForever
	if body.Minutes != nil {
		if *body.Minutes < 1 || *body.Minutes > 7*24*60 {
			fail(w, 400, "A pause must be between 1 minute and 7 days.")
			return
		}
		until = time.Now().Add(time.Duration(*body.Minutes) * time.Minute).Unix()
	}
	n := a.changeDevices(w, r, func(d *devState) bool { return d.ID == id }, func(d *devState) { d.PausedUntil = until })
	if n == 0 {
		fail(w, 404, "No device with that id.")
	}
	if n > 0 {
		a.sendDevice(w, id)
	}
}

func (a *API) deleteDevicePause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n := a.changeDevices(w, r, func(d *devState) bool { return d.ID == id }, func(d *devState) { d.PausedUntil = 0 })
	if n == 0 {
		fail(w, 404, "No device with that id.")
	}
	if n > 0 {
		a.sendDevice(w, id)
	}
}

func (a *API) putGroupVPN(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil || body.On == nil {
		fail(w, 400, "The request body must be JSON like {\"on\": true}.")
		return
	}
	n := a.changeDevices(w, r, func(d *devState) bool { return d.Group == name }, func(d *devState) { d.VPN = *body.On })
	if n == 0 {
		fail(w, 404, "No devices in that group.")
		return
	}
	if n > 0 {
		send(w, map[string]any{"group": name, "on": *body.On, "devices": n})
	}
}

// deleteDevice forgets an offline device (and its history). Online devices come straight back.
func (a *API) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Hold flushMu so a save in progress can't write the device back after we delete it.
	a.s.flushMu.Lock()
	defer a.s.flushMu.Unlock()
	a.s.mu.Lock()
	d, ok := a.s.devs[id]
	if ok && time.Since(d.LastSeen) < 5*time.Minute {
		a.s.mu.Unlock()
		fail(w, 409, "That device is online; it can only be forgotten once it has been offline for 5 minutes.")
		return
	}
	delete(a.s.devs, id)
	for k := range a.s.pending { // unsaved usage of this device
		if k.Dev == id {
			delete(a.s.pending, k)
		}
	}
	for k := range a.s.hosts {
		if k[1] == id {
			delete(a.s.hosts, k)
		}
	}
	for ip, did := range a.s.ipToID {
		if did == id {
			delete(a.s.ipToID, ip)
		}
	}
	a.s.mu.Unlock()
	if !ok {
		fail(w, 404, "No device with that id.")
		return
	}
	if err := a.s.store.deleteDevice(id); err != nil {
		fail(w, 500, "Could not forget the device: "+err.Error())
		return
	}
	a.s.recompute()
	send(w, map[string]string{"forgotten": id})
}

func clientIP(r *http.Request) string {
	if r == nil {
		return "?"
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}

func displayName(r devRow) string {
	switch {
	case r.Name != "":
		return r.Name
	case r.AutoName != "":
		return r.AutoName
	}
	return "Device ." + r.IP[strings.LastIndex(r.IP, ".")+1:]
}

// controlText describes a manual change in one sentence (for the events timeline).
func controlText(a, b devRow) string {
	n := displayName(b)
	now := time.Now().Unix()
	switch {
	case a.VPN != b.VPN:
		return n + map[bool]string{true: ": VPN turned on", false: ": VPN turned off"}[b.VPN]
	case a.PausedUntil <= now && b.PausedUntil > now:
		if b.PausedUntil >= pauseForever {
			return n + " paused until resumed"
		}
		return n + " paused until " + time.Unix(b.PausedUntil, 0).Local().Format("15:04")
	case a.PausedUntil > now && b.PausedUntil <= now:
		return n + " resumed"
	case a.Profile != b.Profile:
		if b.Profile == "" {
			return n + ": VPN profile back to the default"
		}
		return n + ": VPN profile set to " + b.Profile
	case a.SpeedLimit != b.SpeedLimit:
		if b.SpeedLimit == 0 {
			return n + ": speed limit removed"
		}
		return fmt.Sprintf("%s: speed limited to %g Mbit/s", n, b.SpeedLimit)
	}
	return ""
}

// putDeviceLink: {"to": "<primary id>"} links this connection to another device;
// {"to": null} unlinks it and stops automatic linking for it.
func (a *API) putDeviceLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		To *string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		fail(w, 400, `The request body must be JSON like {"to": "<device id>"} or {"to": null}.`)
		return
	}
	a.s.mu.Lock()
	d, ok := a.s.devs[id]
	if !ok {
		a.s.mu.Unlock()
		fail(w, 404, "No device with that id.")
		return
	}
	text := ""
	if body.To == nil {
		d.LinkTo = "-"
		text = displayName(d.devRow) + " unlinked: shown as its own device"
	} else {
		p, ok := a.s.devs[*body.To]
		if !ok || p.ID == id || (p.LinkTo != "" && p.LinkTo != "-") {
			a.s.mu.Unlock()
			fail(w, 400, "Pick a device that isn't itself a linked connection.")
			return
		}
		d.LinkTo = p.ID
		for _, o := range a.s.devs { // anything linked to this one moves to the new primary
			if o.LinkTo == id {
				o.LinkTo, o.dirty = p.ID, true
			}
		}
		text = displayName(d.devRow) + " linked to " + displayName(p.devRow)
	}
	row := d.devRow
	a.s.mu.Unlock()
	if err := a.s.store.upsertDevice(row); err != nil {
		fail(w, 500, "Could not save: "+err.Error())
		return
	}
	a.s.store.addEvent(time.Now(), "control", id, 0, text)
	a.s.flush()
	a.s.recompute()
	send(w, map[string]string{"ok": text})
}
