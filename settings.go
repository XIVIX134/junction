package main

// Settings: what the app's Settings screen shows and changes, which is the whole
// configuration. Brand, timezone, gateway address, LAN, default profile,
// sign-in, data retention, update checks and the bundle's monthly renewal live
// in the settings table (they win over config.json). Everything else is written
// to config.json (configfile.go), the DHCP server's files (dhcp.go) and
// sing-box's config (singbox_edit.go). What is read only at start restarts
// Junction (the service supervisor starts it again; traffic is unaffected:
// sing-box and the firewall keep routing on their own).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var version = "dev" // set at build time: -ldflags "-X main.version=v1.6.0"

type Brand struct {
	Name   string  `json:"name"`
	Color  string  `json:"color"`
	Letter string  `json:"letter"`
	Image  *string `json:"image"` // data: URL, or null
}

func brandNoImage(b Brand) Brand {
	b.Image = nil
	return b
}

type RouterInfo struct {
	URL         string `json:"url"`
	User        string `json:"user"`
	PasswordSet bool   `json:"passwordSet"`
	Connected   bool   `json:"connected"`
	Devices     int    `json:"devices"`
}

type BackupInfo struct {
	Name  string `json:"name"`
	IP    string `json:"ip"`
	State string `json:"state"` // standby | covering | unknown
}

type NetworkSettings struct {
	GatewayIP          string      `json:"gatewayIP"`
	LAN                string      `json:"lan"`
	Iface              string      `json:"iface"`
	Ifaces             []string    `json:"ifaces"`
	DetectedAt         *string     `json:"detectedAt"`
	LearnNamesFromDHCP bool        `json:"learnNamesFromDHCP"`
	Router             *RouterInfo `json:"router"`
	Backup             *BackupInfo `json:"backup"`
}

type Reservation struct {
	MAC  string `json:"mac"`
	IP   string `json:"ip"`
	Name string `json:"name"`
}

type DHCPSettings struct {
	On           bool          `json:"on"`
	PoolStart    string        `json:"poolStart"`
	PoolEnd      string        `json:"poolEnd"`
	LeaseHours   float64       `json:"leaseHours"`
	DNS          []string      `json:"dns"`
	Leases       int           `json:"leases"`
	Reservations []Reservation `json:"reservations"`
}

type ExitInfo struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`   // wireguard | hysteria2 | vless | trojan | shadowsocks | other
	Server    string `json:"server"` // host:port
	Healthy   bool   `json:"healthy"`
	LatencyMs *int   `json:"latencyMs"`
}

type ProfileInfo struct {
	Name      string `json:"name"`
	CheckSite string `json:"checkSite"` // the TLS name (SNI) the profile's exit connections use
}

type FailoverSettings struct {
	Enabled    bool   `json:"enabled"`
	CheckURL   string `json:"checkURL"`
	EverySec   int    `json:"everySec"`
	TimeoutSec int    `json:"timeoutSec"`
	DownAfter  int    `json:"downAfter"`
	UpAfter    int    `json:"upAfter"`
	WaitMin    int    `json:"waitMin"`
}

type VPNSettings struct {
	Exits          []ExitInfo       `json:"exits"`
	Profiles       []ProfileInfo    `json:"profiles"`
	DefaultProfile string           `json:"defaultProfile"`
	Failover       FailoverSettings `json:"failover"`
}

type AutoRenew struct {
	SizeGB float64 `json:"sizeGB"`
	Day    int     `json:"day"`
}

type BundleSettings struct {
	AutoRenew *AutoRenew `json:"autoRenew"`
}

type AccessSettings struct {
	Required    bool `json:"required"`
	PasswordSet bool `json:"passwordSet"`
	SessionDays int  `json:"sessionDays"` // 0 = until signed out
}

type UpdateSettings struct {
	Check     bool    `json:"check"`
	URL       string  `json:"url"`
	LastCheck *string `json:"lastCheck"`
	Available *string `json:"available"`
}

type DataSettings struct {
	UsageDays int   `json:"usageDays"`
	SitesDays int   `json:"sitesDays"`
	DBBytes   int64 `json:"dbBytes"`
}

type StartingName struct {
	MAC   string `json:"mac"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

type AdvancedSettings struct {
	ConfigPath         string         `json:"configPath"`
	Listen             string         `json:"listen"`
	DB                 string         `json:"db"`
	SingboxConfig      string         `json:"singboxConfig"`
	ClashAPI           string         `json:"clashAPI"`
	DnsmasqLeases      string         `json:"dnsmasqLeases"`
	LegacyFailoverFile *string        `json:"legacyFailoverFile"`
	StartingNames      []StartingName `json:"startingNames"`
}

type Settings struct {
	Brand            Brand            `json:"brand"`
	Timezone         string           `json:"timezone"`
	TimezoneDetected string           `json:"timezoneDetected"`
	Network          NetworkSettings  `json:"network"`
	DHCP             *DHCPSettings    `json:"dhcp"`
	VPN              VPNSettings      `json:"vpn"`
	Bundle           BundleSettings   `json:"bundle"`
	Access           AccessSettings   `json:"access"`
	Updates          UpdateSettings   `json:"updates"`
	Data             DataSettings     `json:"data"`
	Advanced         AdvancedSettings `json:"advanced"`
	Version          string           `json:"version"`
}

const (
	defaultBrandColor = "#4797ff"
	maxBrandImage     = 300 << 10
	defaultUsageDays  = 400
	defaultSitesDays  = 35
)

var etcTimezone = "/etc/timezone"

// applySavedSettings puts the values saved in the app over the config file's.
// Only the ones read at start live here; the rest are read when used.
func applySavedSettings(c *Config, store *Store) {
	if v, ok := store.getSetting("timezone"); ok && v != "" {
		if _, err := time.LoadLocation(v); err == nil {
			c.TimeZone = v
		} else {
			log.Printf("settings: ignoring saved timezone %q: %v", v, err)
		}
	}
	if v, ok := store.getSetting("gateway_ip"); ok && v != "" {
		c.GatewayIP = v
	}
	if v, ok := store.getSetting("lan"); ok && v != "" {
		c.LAN = v
	}
}

func (s *State) setting(k, def string) string {
	if v, ok := s.store.getSetting(k); ok && v != "" {
		return v
	}
	return def
}

func (s *State) settingInt(k string, def int) int {
	if n, err := strconv.Atoi(s.setting(k, "")); err == nil {
		return n
	}
	return def
}

func (s *State) loadBrand() Brand {
	b := Brand{Name: s.cfg.BrandName, Color: defaultBrandColor}
	if b.Name == "" {
		b.Name = "Junction"
	}
	if v, ok := s.store.getSetting("brand"); ok {
		json.Unmarshal([]byte(v), &b)
	}
	if b.Letter == "" {
		b.Letter = firstLetter(b.Name)
	}
	return b
}

func firstLetter(name string) string {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "J"
}

// exitName is an exit's display name: the failover config's, else its key capitalised.
func (s *State) exitName(key string) string {
	for _, e := range s.exitList {
		if e.Key == key && e.Name != "" {
			return e.Name
		}
	}
	if key == "" {
		return key
	}
	r, n := utf8.DecodeRuneInString(key)
	return string(unicode.ToUpper(r)) + key[n:]
}

// readExitList reads the VPN exits (and the default profile) from the failover config.
func readExitList(cfg Config) []foEndpoint {
	b, _, err := failoverJSON(cfg)
	if err != nil {
		return nil
	}
	var c foConfig
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return c.Endpoints
}

func failoverDefaultProfile(cfg Config) string {
	b, _, err := failoverJSON(cfg)
	if err != nil {
		return ""
	}
	var c struct {
		DefaultProfile string `json:"default_profile"`
	}
	json.Unmarshal(b, &c)
	return c.DefaultProfile
}

// singboxOrder lists the outbound tags in sing-box's config, in order.
func singboxOrder(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sb struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	json.Unmarshal(b, &sb)
	var out []string
	for _, o := range sb.Outbounds {
		out = append(out, o.Tag)
	}
	return out
}

// globalProfile is the VPN profile for devices without their own. While Junction
// runs the failover, the failover's profile is it (PUT /api/selector changes it too).
func (s *State) globalProfile() string {
	if s.fo != nil {
		s.fo.mu.Lock()
		defer s.fo.mu.Unlock()
		return s.fo.prof
	}
	for _, p := range []string{s.setting("default_profile", ""), s.base.DefaultProfile, failoverDefaultProfile(s.cfg)} {
		if p != "" && validProfile(p) {
			return p
		}
	}
	return currentProfiles()[0]
}

func detectTimezone(fallback string) string {
	if b, err := os.ReadFile(etcTimezone); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	if v := os.Getenv("TZ"); v != "" {
		return strings.TrimPrefix(v, ":")
	}
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if _, z, ok := strings.Cut(l, "zoneinfo/"); ok {
			return z
		}
	}
	return fallback
}

func (s *State) backupInfo(c Config) *BackupInfo {
	gs := readGatewayStatus()
	if gs == nil {
		return nil
	}
	b := &BackupInfo{Name: c.BackupName, IP: c.BackupIP, State: "unknown"}
	if b.Name == "" {
		b.Name = "Backup"
	}
	if b.IP == "" {
		b.IP = keepalivedValue("unicast_peer")
	}
	switch gs.Role {
	case "master":
		b.State = "standby"
	case "backup", "fault":
		b.State = "covering"
	}
	return b
}

// listIfaces: this box's network cards that have an IPv4 address (not loopback).
func listIfaces(current string) []string {
	out := []string{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || ifaceIPv4(i.Name) == "" {
			continue
		}
		out = append(out, i.Name)
	}
	found := false
	for _, n := range out {
		found = found || n == current
	}
	if !found && current != "" {
		out = append([]string{current}, out...)
	}
	return out
}

// savedConfig is config.json as saved now (Settings may have changed it since the start).
func (s *State) savedConfig() Config {
	if s.cfgPath == "" {
		return s.base
	}
	c, err := loadConfig(s.cfgPath)
	if err != nil {
		return s.base
	}
	return c
}

func (s *State) settings() Settings {
	b := s.savedConfig()
	out := Settings{
		Timezone:         s.setting("timezone", s.base.TimeZone),
		TimezoneDetected: detectTimezone(s.base.TimeZone),
		Version:          version,
	}
	s.mu.RLock()
	out.Brand = s.brand
	routerErr, routerAt, routerDevs := s.routerErr, s.routerAt, s.routerDevs
	s.mu.RUnlock()

	n := NetworkSettings{
		GatewayIP:          s.setting("gateway_ip", s.cfg.GatewayIP),
		LAN:                s.setting("lan", s.cfg.LAN),
		Iface:              b.Interface,
		Ifaces:             listIfaces(b.Interface),
		LearnNamesFromDHCP: b.DHCPListen == nil || *b.DHCPListen,
		Backup:             s.backupInfo(b),
	}
	if s.gwDetectedAt != nil {
		t := s.gwDetectedAt.UTC().Format(time.RFC3339)
		n.DetectedAt = &t
	}
	if b.RouterURL != "" {
		n.Router = &RouterInfo{URL: b.RouterURL, User: b.RouterUser, PasswordSet: b.RouterPassword != "",
			Connected: routerErr == nil && !routerAt.IsZero(), Devices: routerDevs}
	}
	out.Network = n
	out.DHCP = s.dhcpSettings()
	out.VPN = s.vpnSettings()
	out.Bundle.AutoRenew = s.autoRenew()
	out.Access = s.auth.settings()
	out.Updates = s.updateSettings(b)
	out.Data = DataSettings{UsageDays: s.settingInt("usage_days", defaultUsageDays), SitesDays: s.settingInt("sites_days", defaultSitesDays)}
	for _, f := range []string{s.cfg.DB, s.cfg.DB + "-wal"} {
		if st, err := os.Stat(f); err == nil {
			out.Data.DBBytes += st.Size()
		}
	}
	out.Advanced = AdvancedSettings{ConfigPath: s.cfgPath, Listen: b.Listen, DB: b.DB, SingboxConfig: b.SingBoxConfig,
		ClashAPI: b.ClashAPI, DnsmasqLeases: b.DnsmasqLeases, StartingNames: startingNames(b)}
	if len(b.Failover) == 0 || string(b.Failover) == "null" {
		if _, err := os.Stat(b.FailoverConfig); err == nil {
			p := b.FailoverConfig
			out.Advanced.LegacyFailoverFile = &p
		}
	}
	return out
}

func startingNames(c Config) []StartingName {
	out := []StartingName{}
	seen := map[string]bool{}
	for _, m := range []map[string]string{c.SeedNames, c.SeedGroups} {
		for mac := range m {
			if _, err := net.ParseMAC(mac); err != nil || seen[strings.ToLower(mac)] {
				continue
			}
			seen[strings.ToLower(mac)] = true
			out = append(out, StartingName{MAC: strings.ToLower(mac), Name: c.SeedNames[mac], Group: c.SeedGroups[mac]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].MAC < out[j].MAC
	})
	return out
}

// failoverSettingsView reads the failover's thresholds (with the defaults Junction uses).
func (s *State) failoverView() (foConfig, map[string]any) {
	c := foDefaults()
	raw := map[string]any{}
	if b, _, err := failoverJSON(s.savedConfig()); err == nil {
		json.Unmarshal(b, &c)
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		dec.Decode(&raw)
	}
	return c, raw
}

func (s *State) vpnSettings() VPNSettings {
	fc, _ := s.failoverView()
	v := VPNSettings{Exits: []ExitInfo{}, Profiles: []ProfileInfo{}, DefaultProfile: s.globalProfile(),
		Failover: FailoverSettings{Enabled: s.savedConfig().FailoverEnabled, CheckURL: fc.ProbeURL, EverySec: fc.ProbeInterval, TimeoutSec: fc.ProbeTimeout,
			DownAfter: fc.FailureThreshold, UpAfter: fc.SuccessThreshold, WaitMin: fc.RecoveryCooldown / 60}}
	profs := currentProfiles()
	for _, p := range profs {
		v.Profiles = append(v.Profiles, ProfileInfo{Name: p, CheckSite: fc.Profiles[p]})
	}
	health := map[string]ExitHealth{}
	ex, _ := s.exitsView(nil)
	for _, e := range ex {
		health[e.Key] = e
	}
	sb, _ := loadSingbox(s.cfg.SingBoxConfig)
	for _, e := range fc.Endpoints {
		info := ExitInfo{Key: e.Key, Name: s.exitName(e.Key), Kind: "other"}
		if e.Name != "" {
			info.Name = e.Name
		}
		if sb != nil {
			info.Kind, info.Server = sb.exitDescription(e.Key, profs)
		}
		if h, ok := health[e.Key]; ok {
			info.Healthy = h.Healthy
			if h.LatencyMs != nil {
				ms := int(*h.LatencyMs + 0.5)
				info.LatencyMs = &ms
			}
		}
		v.Exits = append(v.Exits, info)
	}
	return v
}

func (s *State) autoRenew() *AutoRenew {
	v, ok := s.store.getSetting("bundle_autorenew")
	if !ok || v == "" {
		return nil
	}
	var a AutoRenew
	if json.Unmarshal([]byte(v), &a) != nil || a.SizeGB <= 0 || a.Day < 1 {
		return nil
	}
	return &a
}

// updateChecks: the app looks for new versions (needs update_url, and isn't switched off).
func (s *State) updateChecks() bool {
	return s.cfg.UpdateURL != "" && s.setting("update_check", "1") == "1"
}

func (s *State) updateSettings(b Config) UpdateSettings {
	u := UpdateSettings{Check: b.UpdateURL != "" && s.setting("update_check", "1") == "1", URL: b.UpdateURL}
	s.update.mu.Lock()
	defer s.update.mu.Unlock()
	if !s.update.checkedAt.IsZero() {
		t := s.update.checkedAt.UTC().Format(time.RFC3339)
		u.LastCheck = &t
	}
	if u.Check && newerVersion(s.update.available, version) {
		a := s.update.available
		u.Available = &a
	}
	return u
}

// ---- checks used when changing settings ----

var reColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// checkImage validates a data: URL logo and returns it, or a message saying what's wrong.
func checkImage(v string) (string, string) {
	head, data, ok := strings.Cut(v, ",")
	mime, isB64 := strings.CutSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if !ok || !strings.HasPrefix(head, "data:") || !isB64 {
		return "", "The logo must be an image file (a base64 data: URL)."
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(data); err != nil {
			return "", "The logo file looks damaged; pick it again."
		}
	}
	if len(raw) > maxBrandImage {
		return "", "The logo is too big: it can be at most 300 KB."
	}
	looks := map[string]bool{
		"image/png":     bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")),
		"image/jpeg":    bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}),
		"image/webp":    len(raw) > 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP",
		"image/svg+xml": bytes.Contains(raw[:min(len(raw), 4096)], []byte("<svg")),
	}
	match, known := looks[mime]
	if !known {
		return "", "The logo must be a PNG, JPEG, WebP or SVG image."
	}
	if !match {
		return "", "The logo file isn't the kind of image its name says; save it again as PNG, JPEG, WebP or SVG."
	}
	return v, ""
}

func validTimezone(v string) bool {
	if v == "" || v == "Local" {
		return false
	}
	_, err := time.LoadLocation(v)
	return err == nil
}

// checkLAN returns the network in canonical form ("192.168.1.0/24"), or "".
func checkLAN(v string) string {
	ip, n, err := net.ParseCIDR(strings.TrimSpace(v))
	if err != nil || ip.To4() == nil {
		return ""
	}
	if ones, _ := n.Mask.Size(); ones < 16 || ones > 30 {
		return "" // the ARP sweep visits every address: keep it to a home-sized network
	}
	return n.String()
}

func checkIPv4(v string) string {
	ip := net.ParseIP(strings.TrimSpace(v))
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.String()
}

// restartSoon exits a second after the response went out, so the new settings
// are read at the next start. Only the first call counts.
func (s *State) restartSoon(what []string) {
	if len(what) == 0 {
		return
	}
	why := "the new " + strings.ReplaceAll(strings.Join(what, ", "), "_", " ")
	s.restartOnce.Do(func() {
		go func() {
			time.Sleep(time.Second)
			s.restart(why)
		}()
	})
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	send(w, a.s.settings())
}

// SettingsSaved is the answer to PUT /api/settings and the exit changes.
type SettingsSaved struct {
	Settings
	Restarting bool `json:"restarting,omitempty"`
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var p SettingsPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&p); err != nil {
		fail(w, 400, "The request body must be JSON with the settings to change (a logo can be at most 300 KB).")
		return
	}
	a.s.settingsMu.Lock()
	defer a.s.settingsMu.Unlock()
	restart, ok := a.applyPatch(w, r, &p)
	if !ok {
		return
	}
	a.s.sendSaved(w, restart)
}

func (s *State) sendSaved(w http.ResponseWriter, restart []string) {
	send(w, SettingsSaved{Settings: s.settings(), Restarting: len(restart) > 0})
	s.restartSoon(restart)
}
