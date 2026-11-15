package main

// Settings (v1.6): what the app's Settings screen shows and changes. What is
// saved here lives in the settings table and wins over config.json. Timezone,
// gateway address and LAN are only read at start, so saving them restarts
// Junction (the service supervisor starts it again; traffic is unaffected:
// sing-box and the firewall keep routing on their own).

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
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
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	Devices   int    `json:"devices"`
	HasLogin  bool   `json:"hasLogin"`
}

type BackupInfo struct {
	Name  string `json:"name"`
	IP    string `json:"ip"`
	State string `json:"state"` // standby | covering | unknown
}

type DHCPInfo struct {
	On           bool    `json:"on"`
	Pool         string  `json:"pool"`
	LeaseHours   float64 `json:"leaseHours"`
	Reservations int     `json:"reservations"`
}

type NetworkSettings struct {
	GatewayIP  string      `json:"gatewayIP"`
	LAN        string      `json:"lan"`
	Iface      string      `json:"iface"`
	DetectedAt *string     `json:"detectedAt"`
	Router     *RouterInfo `json:"router"`
	Backup     *BackupInfo `json:"backup"`
	DHCP       *DHCPInfo   `json:"dhcp"`
}

type ExitBrief struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

type VPNSettings struct {
	Exits          []ExitBrief `json:"exits"`
	Profiles       []string    `json:"profiles"`
	DefaultProfile string      `json:"defaultProfile"`
}

type AccessSettings struct {
	Required    bool `json:"required"`
	PasswordSet bool `json:"passwordSet"`
	SessionDays int  `json:"sessionDays"` // 0 = until signed out
}

type Settings struct {
	Brand            Brand           `json:"brand"`
	Timezone         string          `json:"timezone"`
	TimezoneDetected string          `json:"timezoneDetected"`
	Network          NetworkSettings `json:"network"`
	VPN              VPNSettings     `json:"vpn"`
	Access           AccessSettings  `json:"access"`
	Version          string          `json:"version"`
}

type NetworkPatch struct {
	GatewayIP *string `json:"gatewayIP"`
	LAN       *string `json:"lan"`
}

type BrandPatch struct {
	Name   *string         `json:"name"`
	Color  *string         `json:"color"`
	Letter *string         `json:"letter"`
	Image  json.RawMessage `json:"image"` // null or "" removes it
}

// SettingsPatch: every field is optional; missing = unchanged.
type SettingsPatch struct {
	Brand    *BrandPatch   `json:"brand"`
	Timezone *string       `json:"timezone"`
	Network  *NetworkPatch `json:"network"`
	VPN      *struct {
		DefaultProfile *string `json:"defaultProfile"`
	} `json:"vpn"`
	Access *struct {
		Required    *bool `json:"required"`
		SessionDays *int  `json:"sessionDays"`
	} `json:"access"`
}

const (
	defaultBrandColor = "#4797ff"
	maxBrandImage     = 300 << 10
)

// Files read for the network section (vars for tests).
var (
	dnsmasqConf      = "/etc/junction/dnsmasq.conf"
	reservationsConf = "/etc/junction/reservations.conf"
	etcTimezone      = "/etc/timezone"
)

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
func readExitList(path string) []foEndpoint {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c foConfig
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return c.Endpoints
}

func failoverDefaultProfile(path string) string {
	b, err := os.ReadFile(path)
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
	for _, p := range []string{s.setting("default_profile", ""), s.base.DefaultProfile, failoverDefaultProfile(s.cfg.FailoverConfig)} {
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

func (s *State) backupInfo() *BackupInfo {
	gs := readGatewayStatus()
	if gs == nil {
		return nil
	}
	b := &BackupInfo{Name: s.base.BackupName, IP: s.base.BackupIP, State: "unknown"}
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

// dhcpInfo reads Junction's dnsmasq setup; nil when there is none to read.
func (s *State) dhcpInfo() *DHCPInfo {
	conf, confErr := os.ReadFile(dnsmasqConf)
	_, leaseErr := os.Stat(s.cfg.DnsmasqLeases)
	if confErr != nil && leaseErr != nil {
		return nil
	}
	d := &DHCPInfo{On: leaseErr == nil && s.cfg.DHCPListen != nil && !*s.cfg.DHCPListen, LeaseHours: 1}
	ranged := false
	sc := bufio.NewScanner(bytes.NewReader(conf))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "dhcp-host=") {
			d.Reservations++
		}
		if v, ok := strings.CutPrefix(l, "dhcp-range="); ok && !ranged {
			ranged = true
			d.Pool, d.LeaseHours = parseDHCPRange(v)
		}
	}
	if b, err := os.ReadFile(reservationsConf); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "dhcp-host=") {
				d.Reservations++
			}
		}
	}
	return d
}

// parseDHCPRange reads "set:pool,192.168.1.100,192.168.1.179,255.255.255.0,12h"
// into ".100 – .179" and the lease in hours (0 = infinite).
func parseDHCPRange(v string) (string, float64) {
	var ips []string
	lease := 1.0 // dnsmasq's default
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		if ip := net.ParseIP(f); ip != nil {
			ips = append(ips, ip.String())
			continue
		}
		if h, ok := leaseHours(f); ok {
			lease = h
		}
	}
	switch len(ips) {
	case 0:
		return "", lease
	case 1:
		return ips[0], lease
	}
	a, b := ips[0], ips[1]
	if i := strings.LastIndexByte(a, '.'); i > 0 && strings.HasPrefix(b, a[:i+1]) {
		return a[i:] + " – " + b[i:], lease
	}
	return a + " – " + b, lease
}

func leaseHours(f string) (float64, bool) {
	if f == "infinite" {
		return 0, true
	}
	unit := 1.0 / 3600 // plain number: seconds
	switch {
	case strings.HasSuffix(f, "m"):
		unit, f = 1.0/60, f[:len(f)-1]
	case strings.HasSuffix(f, "h"):
		unit, f = 1, f[:len(f)-1]
	case strings.HasSuffix(f, "d"):
		unit, f = 24, f[:len(f)-1]
	case strings.HasSuffix(f, "w"):
		unit, f = 24*7, f[:len(f)-1]
	}
	n, err := strconv.ParseFloat(f, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return float64(int(n*unit*100+0.5)) / 100, true
}

func (s *State) settings() Settings {
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
		GatewayIP: s.setting("gateway_ip", s.cfg.GatewayIP),
		LAN:       s.setting("lan", s.cfg.LAN),
		Iface:     s.cfg.Interface,
		Backup:    s.backupInfo(),
		DHCP:      s.dhcpInfo(),
	}
	if s.gwDetectedAt != nil {
		t := s.gwDetectedAt.UTC().Format(time.RFC3339)
		n.DetectedAt = &t
	}
	if s.base.RouterURL != "" {
		n.Router = &RouterInfo{URL: s.base.RouterURL, Connected: routerErr == nil && !routerAt.IsZero(), Devices: routerDevs,
			HasLogin: s.base.RouterUser != "" && s.base.RouterPassword != ""}
	}
	out.Network = n

	ex, _ := s.exitsView(nil)
	out.VPN = VPNSettings{Exits: []ExitBrief{}, Profiles: currentProfiles(), DefaultProfile: s.globalProfile()}
	for _, e := range ex {
		out.VPN.Exits = append(out.VPN.Exits, ExitBrief{Key: e.Key, Name: e.Name, Healthy: e.Healthy})
	}
	if len(ex) == 0 { // not read from the failover yet: the configured exits
		for _, e := range s.exitList {
			out.VPN.Exits = append(out.VPN.Exits, ExitBrief{Key: e.Key, Name: s.exitName(e.Key)})
		}
	}

	out.Access = s.auth.settings()
	return out
}

// ---- changing settings ----

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

// applyPatch validates the whole patch, then saves it. It returns what needs a
// restart, or writes the error and returns ok=false.
func (a *API) applyPatch(w http.ResponseWriter, r *http.Request, p *SettingsPatch) (restart []string, ok bool) {
	s := a.s
	saves := map[string]string{}
	var brand *Brand
	if b := p.Brand; b != nil {
		s.mu.RLock()
		nb := s.brand
		s.mu.RUnlock()
		if b.Name != nil {
			n := strings.TrimSpace(*b.Name)
			if n == "" || utf8.RuneCountInString(n) > 40 {
				fail(w, 400, "The name must be 1 to 40 characters.")
				return nil, false
			}
			nb.Name = n
		}
		if b.Color != nil {
			if !reColor.MatchString(*b.Color) {
				fail(w, 400, "The colour must look like #4797ff.")
				return nil, false
			}
			nb.Color = strings.ToLower(*b.Color)
		}
		if b.Letter != nil {
			l := strings.TrimSpace(*b.Letter)
			if n := utf8.RuneCountInString(l); n < 1 || n > 2 {
				fail(w, 400, "The badge letter must be 1 or 2 characters.")
				return nil, false
			}
			nb.Letter = l
		}
		if len(b.Image) > 0 {
			var img *string
			if err := json.Unmarshal(b.Image, &img); err != nil {
				fail(w, 400, "The logo must be a data: URL string, or null to remove it.")
				return nil, false
			}
			if img == nil || *img == "" {
				nb.Image = nil
			} else {
				v, msg := checkImage(*img)
				if msg != "" {
					fail(w, 400, msg)
					return nil, false
				}
				nb.Image = &v
			}
		}
		brand = &nb
	}
	if p.Timezone != nil {
		tz := strings.TrimSpace(*p.Timezone)
		if !validTimezone(tz) {
			fail(w, 400, "Pick a timezone from the list, like Europe/London.")
			return nil, false
		}
		saves["timezone"] = tz
	}
	if n := p.Network; n != nil {
		if n.GatewayIP != nil {
			ip := checkIPv4(*n.GatewayIP)
			if ip == "" {
				fail(w, 400, "The gateway address must be an IPv4 address like 192.168.1.4.")
				return nil, false
			}
			saves["gateway_ip"] = ip
		}
		if n.LAN != nil {
			lan := checkLAN(*n.LAN)
			if lan == "" {
				fail(w, 400, "The network must look like 192.168.1.0/24 (between /16 and /30).")
				return nil, false
			}
			saves["lan"] = lan
		}
		gw, lan := s.setting("gateway_ip", s.cfg.GatewayIP), s.setting("lan", s.cfg.LAN)
		if v, ok := saves["gateway_ip"]; ok {
			gw = v
		}
		if v, ok := saves["lan"]; ok {
			lan = v
		}
		if _, n, err := net.ParseCIDR(lan); err == nil && gw != "" && !n.Contains(net.ParseIP(gw)) {
			fail(w, 400, fmt.Sprintf("The gateway address %s isn't inside the network %s.", gw, lan))
			return nil, false
		}
	}
	if v := p.VPN; v != nil && v.DefaultProfile != nil {
		if !validProfile(*v.DefaultProfile) {
			fail(w, 400, "The profile must be "+profileChoices()+".")
			return nil, false
		}
		if s.fo != nil {
			if _, known := s.fo.cfg.Profiles[*v.DefaultProfile]; !known {
				fail(w, 400, "The failover isn't set up to probe the "+*v.DefaultProfile+" profile, so it can't be the default.")
				return nil, false
			}
		}
		saves["default_profile"] = *v.DefaultProfile
	}
	turnOn := false
	if ac := p.Access; ac != nil {
		if ac.SessionDays != nil {
			if *ac.SessionDays < 0 || *ac.SessionDays > 3650 {
				fail(w, 400, "Sessions can last 0 (until signed out) to 3650 days.")
				return nil, false
			}
			saves["session_days"] = strconv.Itoa(*ac.SessionDays)
		}
		if ac.Required != nil {
			if *ac.Required && !s.auth.passwordSet() {
				fail(w, 400, "Set a password first, then turn on sign-in.")
				return nil, false
			}
			saves["auth_required"] = map[bool]string{true: "1", false: "0"}[*ac.Required]
			turnOn = *ac.Required && !s.auth.required()
		}
	}

	// Everything checks out: save.
	if brand != nil {
		b, _ := json.Marshal(brand)
		if err := s.store.setSetting("brand", string(b)); err != nil {
			fail(w, 500, "Could not save the settings: "+err.Error())
			return nil, false
		}
		s.mu.Lock()
		s.brand = *brand
		s.mu.Unlock()
	}
	for k, v := range saves {
		if err := s.store.setSetting(k, v); err != nil {
			fail(w, 500, "Could not save the settings: "+err.Error())
			return nil, false
		}
	}
	s.auth.reload()
	log.Printf("settings: changed %s (from %s)", strings.Join(patchKeys(brand != nil, saves), ", "), clientIP(r))
	if v, ok := saves["default_profile"]; ok {
		if s.fo != nil {
			s.fo.mu.Lock()
			s.fo.prof = v
			s.fo.mu.Unlock()
			s.fo.publish()
		}
		s.recompute()
	}
	if turnOn {
		// Whoever turns sign-in on stays signed in on this browser.
		if err := s.auth.startSession(w, r); err != nil {
			log.Printf("settings: %v", err)
		}
	}
	for k, running := range map[string]string{"timezone": s.cfg.TimeZone, "gateway_ip": s.cfg.GatewayIP, "lan": s.cfg.LAN} {
		if v, ok := saves[k]; ok && v != running {
			restart = append(restart, k)
		}
	}
	return restart, true
}

func patchKeys(brand bool, saves map[string]string) []string {
	var ks []string
	if brand {
		ks = append(ks, "brand")
	}
	for k := range saves {
		ks = append(ks, k)
	}
	if len(ks) == 0 {
		ks = []string{"nothing"}
	}
	return ks
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

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var p SettingsPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&p); err != nil {
		fail(w, 400, "The request body must be JSON with the settings to change (a logo can be at most 300 KB).")
		return
	}
	restart, ok := a.applyPatch(w, r, &p)
	if !ok {
		return
	}
	send(w, a.s.settings())
	a.s.restartSoon(restart)
}
