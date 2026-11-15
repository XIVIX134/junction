package main

// PUT /api/settings: every field is optional; missing = unchanged. The whole
// patch is checked before anything is saved; then sing-box (profiles), the DHCP
// server, config.json and the settings table are changed, in that order (the
// first two check the new files and put the old ones back when they don't work).

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type NetworkPatch struct {
	GatewayIP          *string         `json:"gatewayIP"`
	LAN                *string         `json:"lan"`
	Iface              *string         `json:"iface"`
	LearnNamesFromDHCP *bool           `json:"learnNamesFromDHCP"`
	BackupName         *string         `json:"backupName"`
	BackupIP           *string         `json:"backupIP"`
	Router             json.RawMessage `json:"router"` // null = don't read the router
}

type BrandPatch struct {
	Name   *string         `json:"name"`
	Color  *string         `json:"color"`
	Letter *string         `json:"letter"`
	Image  json.RawMessage `json:"image"` // null or "" removes it
}

type DHCPPatch struct {
	PoolStart    *string        `json:"poolStart"`
	PoolEnd      *string        `json:"poolEnd"`
	LeaseHours   *float64       `json:"leaseHours"`
	DNS          *[]string      `json:"dns"`
	Reservations *[]Reservation `json:"reservations"`
}

type FailoverPatch struct {
	Enabled    *bool   `json:"enabled"`
	CheckURL   *string `json:"checkURL"`
	EverySec   *int    `json:"everySec"`
	TimeoutSec *int    `json:"timeoutSec"`
	DownAfter  *int    `json:"downAfter"`
	UpAfter    *int    `json:"upAfter"`
	WaitMin    *int    `json:"waitMin"`
}

type SettingsPatch struct {
	Brand    *BrandPatch   `json:"brand"`
	Timezone *string       `json:"timezone"`
	Network  *NetworkPatch `json:"network"`
	DHCP     *DHCPPatch    `json:"dhcp"`
	VPN      *struct {
		DefaultProfile *string        `json:"defaultProfile"`
		Profiles       *[]ProfileInfo `json:"profiles"`
		Failover       *FailoverPatch `json:"failover"`
		RenameExit     *struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"renameExit"`
	} `json:"vpn"`
	Bundle *struct {
		AutoRenew json.RawMessage `json:"autoRenew"` // null = off
	} `json:"bundle"`
	Access *struct {
		Required    *bool `json:"required"`
		SessionDays *int  `json:"sessionDays"`
	} `json:"access"`
	Updates *struct {
		Check *bool   `json:"check"`
		URL   *string `json:"url"`
	} `json:"updates"`
	Data *struct {
		UsageDays *int `json:"usageDays"`
		SitesDays *int `json:"sitesDays"`
	} `json:"data"`
	Advanced *struct {
		Listen        *string         `json:"listen"`
		DB            *string         `json:"db"`
		SingboxConfig *string         `json:"singboxConfig"`
		ClashAPI      *string         `json:"clashAPI"`
		DnsmasqLeases *string         `json:"dnsmasqLeases"`
		StartingNames *[]StartingName `json:"startingNames"`
	} `json:"advanced"`
}

// patchError is a message for the person changing the settings (HTTP 400).
type patchError string

func (e patchError) Error() string { return string(e) }

func bad(format string, a ...any) error { return patchError(fmt.Sprintf(format, a...)) }

// applyPatch checks the whole patch, then saves it. It returns what needs a
// restart, or writes the error and returns ok=false.
func (a *API) applyPatch(w http.ResponseWriter, r *http.Request, p *SettingsPatch) (restart []string, ok bool) {
	s := a.s
	plan, err := s.planPatch(p)
	if err != nil {
		if pe, isPE := err.(patchError); isPE {
			fail(w, 400, string(pe))
		} else {
			fail(w, 500, "Could not save the settings: "+err.Error())
		}
		return nil, false
	}
	turnOn := plan.saves["auth_required"] == "1" && !s.auth.required()
	restart, err = s.commit(plan)
	if err != nil {
		code := 500
		if _, isPE := err.(patchError); isPE {
			code = 400
		}
		fail(w, code, err.Error())
		return nil, false
	}
	log.Printf("settings: changed %s (from %s)", strings.Join(plan.what(), ", "), clientIP(r))
	if turnOn {
		// Whoever turns sign-in on stays signed in on this browser.
		if err := s.auth.startSession(w, r); err != nil {
			log.Printf("settings: %v", err)
		}
	}
	return restart, true
}

// patchPlan is a checked patch, ready to save.
type patchPlan struct {
	brand    *Brand
	saves    map[string]string // settings table
	cfgEdits []func(m map[string]any) error
	cfgWhat  []string
	foEdits  []func(fo map[string]any) error // the failover section of config.json
	dhcp     *dhcpPlan
	profiles *profilePlan
}

func (pl *patchPlan) config(what string, f func(m map[string]any) error) {
	pl.cfgEdits = append(pl.cfgEdits, f)
	pl.cfgWhat = append(pl.cfgWhat, what)
}

func (pl *patchPlan) failover(what string, f func(fo map[string]any) error) {
	pl.foEdits = append(pl.foEdits, f)
	pl.cfgWhat = append(pl.cfgWhat, what)
}

func (pl *patchPlan) what() []string {
	var ks []string
	if pl.brand != nil {
		ks = append(ks, "brand")
	}
	for k := range pl.saves {
		ks = append(ks, k)
	}
	ks = append(ks, pl.cfgWhat...)
	if pl.dhcp != nil {
		ks = append(ks, "dhcp")
	}
	if pl.profiles != nil && pl.profiles.singbox {
		ks = append(ks, "profiles")
	}
	if len(ks) == 0 {
		ks = []string{"nothing"}
	}
	return ks
}

func (s *State) planPatch(p *SettingsPatch) (*patchPlan, error) {
	pl := &patchPlan{saves: map[string]string{}}
	if b := p.Brand; b != nil {
		s.mu.RLock()
		nb := s.brand
		s.mu.RUnlock()
		if b.Name != nil {
			n := strings.TrimSpace(*b.Name)
			if n == "" || utf8.RuneCountInString(n) > 40 {
				return nil, bad("The name must be 1 to 40 characters.")
			}
			nb.Name = n
		}
		if b.Color != nil {
			if !reColor.MatchString(*b.Color) {
				return nil, bad("The colour must look like #4797ff.")
			}
			nb.Color = strings.ToLower(*b.Color)
		}
		if b.Letter != nil {
			l := strings.TrimSpace(*b.Letter)
			if n := utf8.RuneCountInString(l); n < 1 || n > 2 {
				return nil, bad("The badge letter must be 1 or 2 characters.")
			}
			nb.Letter = l
		}
		if len(b.Image) > 0 {
			var img *string
			if err := json.Unmarshal(b.Image, &img); err != nil {
				return nil, bad("The logo must be a data: URL string, or null to remove it.")
			}
			if img == nil || *img == "" {
				nb.Image = nil
			} else {
				v, msg := checkImage(*img)
				if msg != "" {
					return nil, bad("%s", msg)
				}
				nb.Image = &v
			}
		}
		pl.brand = &nb
	}
	if p.Timezone != nil {
		tz := strings.TrimSpace(*p.Timezone)
		if !validTimezone(tz) {
			return nil, bad("Pick a timezone from the list, like Europe/London.")
		}
		pl.saves["timezone"] = tz
	}
	if n := p.Network; n != nil {
		if err := s.planNetwork(pl, n); err != nil {
			return nil, err
		}
	}
	if d := p.DHCP; d != nil {
		dp, err := s.planDHCP(d)
		if err != nil {
			return nil, err
		}
		pl.dhcp = dp
	}
	if v := p.VPN; v != nil {
		if v.Profiles != nil {
			pp, err := s.planProfiles(*v.Profiles, v.DefaultProfile)
			if err != nil {
				return nil, err
			}
			pl.profiles = pp
			pl.failover("profiles", pp.failoverEdit)
		}
		if v.DefaultProfile != nil {
			dp := *v.DefaultProfile
			known := validProfile(dp) || (pl.profiles != nil && pl.profiles.has(dp))
			if !known {
				return nil, bad("The profile must be %s.", profileChoices())
			}
			if s.fo != nil && pl.profiles == nil {
				if _, probed := s.fo.cfg.Profiles[dp]; !probed {
					return nil, bad("The failover isn't set up to check the %s profile, so it can't be the default.", dp)
				}
			}
			pl.saves["default_profile"] = dp
			pl.failover("default profile", func(fo map[string]any) error { fo["default_profile"] = dp; return nil })
		}
		if f := v.Failover; f != nil {
			if err := s.planFailover(pl, f); err != nil {
				return nil, err
			}
		}
		if re := v.RenameExit; re != nil {
			name := strings.TrimSpace(re.Name)
			if name == "" || utf8.RuneCountInString(name) > 40 {
				return nil, bad("An exit's name must be 1 to 40 characters.")
			}
			key := re.Key
			found := false
			for _, e := range s.exitList {
				found = found || e.Key == key
			}
			if !found {
				return nil, bad("There is no VPN exit %q; reload the page.", key)
			}
			pl.failover("exit name", func(fo map[string]any) error {
				for _, e := range foEndpoints(fo) {
					if e["key"] == key {
						e["name"] = name
						return nil
					}
				}
				return bad("There is no VPN exit %q; reload the page.", key)
			})
		}
	}
	if b := p.Bundle; b != nil && len(b.AutoRenew) > 0 {
		var ar *AutoRenew
		if err := json.Unmarshal(b.AutoRenew, &ar); err != nil {
			return nil, bad("The monthly renewal must be {\"sizeGB\": 250, \"day\": 1}, or null to turn it off.")
		}
		if ar == nil {
			pl.saves["bundle_autorenew"] = ""
		} else {
			if ar.SizeGB <= 0 || ar.SizeGB > 100000 {
				return nil, bad("The bundle size must be a positive number of GB.")
			}
			if ar.Day < 1 || ar.Day > 28 {
				return nil, bad("The renewal day must be 1 to 28 (every month has it).")
			}
			j, _ := json.Marshal(ar)
			pl.saves["bundle_autorenew"] = string(j)
			if s.autoRenew() == nil {
				pl.saves["bundle_autorenew_since"] = strconv.FormatInt(time.Now().Unix(), 10)
			}
		}
	}
	if ac := p.Access; ac != nil {
		if ac.SessionDays != nil {
			if *ac.SessionDays < 0 || *ac.SessionDays > 3650 {
				return nil, bad("Sessions can last 0 (until signed out) to 3650 days.")
			}
			pl.saves["session_days"] = strconv.Itoa(*ac.SessionDays)
		}
		if ac.Required != nil {
			if *ac.Required && !s.auth.passwordSet() {
				return nil, bad("Set a password first, then turn on sign-in.")
			}
			pl.saves["auth_required"] = map[bool]string{true: "1", false: "0"}[*ac.Required]
		}
	}
	if u := p.Updates; u != nil {
		if u.Check != nil {
			pl.saves["update_check"] = map[bool]string{true: "1", false: "0"}[*u.Check]
		}
		if u.URL != nil {
			v := strings.TrimSpace(*u.URL)
			if v != "" {
				pu, err := url.Parse(v)
				if err != nil || pu.Scheme != "https" || pu.Host == "" {
					return nil, bad("The releases address must be an https:// address, like https://api.github.com/repos/OWNER/junction/releases.")
				}
			}
			if v == defaultUpdateURL {
				v = "" // the default: keep the key out
			}
			pl.config("update_url", func(m map[string]any) error { setOrDelete(m, "update_url", v); return nil })
		}
	}
	if d := p.Data; d != nil {
		if d.UsageDays != nil {
			if *d.UsageDays < 30 || *d.UsageDays > 1000 {
				return nil, bad("Usage history can be kept 30 to 1000 days.")
			}
			pl.saves["usage_days"] = strconv.Itoa(*d.UsageDays)
		}
		if d.SitesDays != nil {
			if *d.SitesDays < 7 || *d.SitesDays > 400 {
				return nil, bad("Site lists can be kept 7 to 400 days.")
			}
			pl.saves["sites_days"] = strconv.Itoa(*d.SitesDays)
		}
	}
	if ad := p.Advanced; ad != nil {
		if err := s.planAdvanced(pl, ad.Listen, ad.DB, ad.SingboxConfig, ad.ClashAPI, ad.DnsmasqLeases, ad.StartingNames); err != nil {
			return nil, err
		}
	}
	return pl, nil
}

func (s *State) planNetwork(pl *patchPlan, n *NetworkPatch) error {
	if n.GatewayIP != nil {
		ip := checkIPv4(*n.GatewayIP)
		if ip == "" {
			return bad("The gateway address must be an IPv4 address like 192.168.1.4.")
		}
		pl.saves["gateway_ip"] = ip
	}
	if n.LAN != nil {
		lan := checkLAN(*n.LAN)
		if lan == "" {
			return bad("The network must look like 192.168.1.0/24 (between /16 and /30).")
		}
		pl.saves["lan"] = lan
	}
	gw, lan := s.setting("gateway_ip", s.cfg.GatewayIP), s.setting("lan", s.cfg.LAN)
	if v, ok := pl.saves["gateway_ip"]; ok {
		gw = v
	}
	if v, ok := pl.saves["lan"]; ok {
		lan = v
	}
	if _, nw, err := net.ParseCIDR(lan); err == nil && gw != "" && !nw.Contains(net.ParseIP(gw)) {
		return bad("The gateway address %s isn't inside the network %s.", gw, lan)
	}
	if n.Iface != nil {
		v := strings.TrimSpace(*n.Iface)
		known := false
		for _, i := range listIfaces(s.cfg.Interface) {
			known = known || i == v
		}
		if !known {
			return bad("There is no network card %q on this box.", v)
		}
		pl.config("interface", func(m map[string]any) error { m["interface"] = v; return nil })
	}
	if n.LearnNamesFromDHCP != nil {
		on := *n.LearnNamesFromDHCP
		if on && dnsmasqRunning() {
			return bad("This gateway hands out addresses itself, so names come from its leases; listening is off.")
		}
		pl.config("dhcp_listen", func(m map[string]any) error { m["dhcp_listen"] = on; return nil })
	}
	if n.BackupName != nil {
		v := strings.TrimSpace(*n.BackupName)
		if utf8.RuneCountInString(v) > 40 {
			return bad("The backup's name can be at most 40 characters.")
		}
		pl.config("backup_name", func(m map[string]any) error { setOrDelete(m, "backup_name", v); return nil })
	}
	if n.BackupIP != nil {
		v := strings.TrimSpace(*n.BackupIP)
		if v != "" {
			if v = checkIPv4(v); v == "" {
				return bad("The backup's address must be an IPv4 address like 192.168.1.3 (empty: keepalived's).")
			}
		}
		pl.config("backup_ip", func(m map[string]any) error { setOrDelete(m, "backup_ip", v); return nil })
	}
	if len(n.Router) > 0 {
		var rp *struct {
			URL      string  `json:"url"`
			User     *string `json:"user"`
			Password *string `json:"password"`
		}
		if err := json.Unmarshal(n.Router, &rp); err != nil {
			return bad("The router must be {\"url\": ..., \"user\": ..., \"password\": ...}, or null.")
		}
		if rp == nil {
			pl.config("router", func(m map[string]any) error {
				delete(m, "router_url")
				delete(m, "router_user")
				delete(m, "router_password")
				return nil
			})
		} else {
			u, err := url.Parse(strings.TrimSpace(rp.URL))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return bad("The router page must be an address like https://192.168.1.1.")
			}
			if rp.User != nil && utf8.RuneCountInString(*rp.User) > 64 || rp.Password != nil && len(*rp.Password) > 128 {
				return bad("The router's user name or password is too long.")
			}
			pl.config("router", func(m map[string]any) error {
				m["router_url"] = strings.TrimRight(u.String(), "/")
				if rp.User != nil {
					setOrDelete(m, "router_user", strings.TrimSpace(*rp.User))
				}
				if rp.Password != nil {
					setOrDelete(m, "router_password", *rp.Password)
				}
				return nil
			})
		}
	}
	return nil
}

func (s *State) planFailover(pl *patchPlan, f *FailoverPatch) error {
	cur, _ := s.failoverView()
	num := func(v *int, lo, hi int, what string) error {
		if v != nil && (*v < lo || *v > hi) {
			return bad("%s must be %d to %d.", what, lo, hi)
		}
		return nil
	}
	for _, c := range []error{
		num(f.EverySec, 5, 300, "Checking every … seconds"),
		num(f.TimeoutSec, 1, 30, "The check timeout"),
		num(f.DownAfter, 1, 20, "Down after … checks"),
		num(f.UpAfter, 1, 60, "Back after … checks"),
		num(f.WaitMin, 0, 120, "The wait"),
	} {
		if c != nil {
			return c
		}
	}
	every, timeout := cur.ProbeInterval, cur.ProbeTimeout
	if f.EverySec != nil {
		every = *f.EverySec
	}
	if f.TimeoutSec != nil {
		timeout = *f.TimeoutSec
	}
	if timeout >= every {
		return bad("The check timeout (%d s) must be shorter than the time between checks (%d s).", timeout, every)
	}
	var check string
	if f.CheckURL != nil {
		check = strings.TrimSpace(*f.CheckURL)
		u, err := url.Parse(check)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return bad("The check address must be a web address like http://cp.cloudflare.com.")
		}
	}
	if f.Enabled != nil {
		if *f.Enabled && len(s.exitList) == 0 {
			return bad("Add a VPN exit first.")
		}
		on := *f.Enabled
		pl.config("failover_enabled", func(m map[string]any) error { m["failover_enabled"] = on; return nil })
	}
	if f.CheckURL != nil || f.EverySec != nil || f.TimeoutSec != nil || f.DownAfter != nil || f.UpAfter != nil || f.WaitMin != nil {
		pl.failover("failover checks", func(fo map[string]any) error {
			if f.CheckURL != nil {
				fo["probe_url"] = check
			}
			set := func(k string, v *int, mul int) {
				if v != nil {
					fo[k] = *v * mul
				}
			}
			set("probe_interval_seconds", f.EverySec, 1)
			set("probe_timeout_seconds", f.TimeoutSec, 1)
			set("failure_threshold", f.DownAfter, 1)
			set("success_threshold", f.UpAfter, 1)
			set("recovery_cooldown_seconds", f.WaitMin, 60)
			return nil
		})
	}
	return nil
}

func (s *State) planAdvanced(pl *patchPlan, listen, db, sbc, clash, leases *string, names *[]StartingName) error {
	if listen != nil {
		v := strings.TrimSpace(*listen)
		_, port, err := net.SplitHostPort(v)
		if n, perr := strconv.Atoi(port); err != nil || perr != nil || n < 1 || n > 65535 {
			return bad("The web page port must look like :80 or 192.168.1.4:8080.")
		}
		pl.config("listen", func(m map[string]any) error { m["listen"] = v; return nil })
	}
	file := func(v *string, key, what string, mustExist bool) error {
		if v == nil {
			return nil
		}
		p := strings.TrimSpace(*v)
		if !strings.HasPrefix(p, "/") {
			return bad("%s must be a full path, starting with /.", what)
		}
		if mustExist && !fileExists(p) {
			return bad("%s %s doesn't exist (copy it there first).", what, p)
		}
		pl.config(key, func(m map[string]any) error { m[key] = p; return nil })
		return nil
	}
	if err := file(db, "db", "The database", true); err != nil {
		return err
	}
	if err := file(sbc, "singbox_config", "The sing-box config", true); err != nil {
		return err
	}
	if sbc != nil {
		if _, err := loadSingbox(strings.TrimSpace(*sbc)); err != nil {
			return bad("That sing-box config can't be read: %v", err)
		}
	}
	if err := file(leases, "dnsmasq_leases", "The DHCP lease file", false); err != nil {
		return err
	}
	if clash != nil {
		v := strings.TrimRight(strings.TrimSpace(*clash), "/")
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "http" || u.Host == "" {
			return bad("The sing-box API address must look like http://127.0.0.1:9090.")
		}
		pl.config("clash_api", func(m map[string]any) error { m["clash_api"] = v; return nil })
	}
	if names != nil {
		seedN, seedG := map[string]any{}, map[string]any{}
		for _, n := range *names {
			hw, err := net.ParseMAC(strings.TrimSpace(n.MAC))
			if err != nil || len(hw) != 6 {
				return bad("%q isn't a MAC address (like aa:bb:cc:dd:ee:ff).", n.MAC)
			}
			mac := hw.String()
			name, group := strings.TrimSpace(n.Name), strings.TrimSpace(n.Group)
			if name == "" || utf8.RuneCountInString(name) > 60 || utf8.RuneCountInString(group) > 40 {
				return bad("Every starting name needs a name (up to 60 characters; groups up to 40).")
			}
			if _, dup := seedN[mac]; dup {
				return bad("%s is listed twice.", mac)
			}
			seedN[mac] = name
			if group != "" {
				seedG[mac] = group
			}
		}
		pl.config("starting names", func(m map[string]any) error {
			m["seed_names"], m["seed_groups"] = seedN, seedG
			return nil
		})
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// commit saves a checked plan. sing-box and the DHCP server go first: they are
// checked and put back by themselves when the new files don't work.
func (s *State) commit(pl *patchPlan) (restart []string, err error) {
	if pp := pl.profiles; pp != nil && pp.singbox {
		if err := s.applySingbox(pp.edit); err != nil {
			return nil, err
		}
		for _, p := range pp.removed {
			os.Remove(profileListPath(p)) // sing-box no longer reads it
		}
	}
	if pl.dhcp != nil {
		if err := s.applyDHCP(pl.dhcp); err != nil {
			return nil, err
		}
	}
	if len(pl.cfgEdits) > 0 || len(pl.foEdits) > 0 {
		before := fileHash(s.cfgPath)
		err := editConfigFile(s.cfgPath, func(m map[string]any) error {
			for _, f := range pl.cfgEdits {
				if err := f(m); err != nil {
					return err
				}
			}
			if len(pl.foEdits) == 0 {
				return nil
			}
			fo, err := s.failoverSection(m)
			if err != nil {
				return err
			}
			for _, f := range pl.foEdits {
				if err := f(fo); err != nil {
					return err
				}
			}
			m["failover"] = fo
			return nil
		})
		if err != nil {
			return nil, err
		}
		if fileHash(s.cfgPath) != before {
			restart = append(restart, "config")
		}
	}
	if pl.brand != nil {
		b, _ := json.Marshal(pl.brand)
		if err := s.store.setSetting("brand", string(b)); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.brand = *pl.brand
		s.mu.Unlock()
	}
	for k, v := range pl.saves {
		if err := s.store.setSetting(k, v); err != nil {
			return nil, err
		}
	}
	s.auth.reload()
	if v, ok := pl.saves["default_profile"]; ok && (pl.profiles == nil || !pl.profiles.singbox) {
		if s.fo != nil {
			s.fo.mu.Lock()
			s.fo.prof = v
			s.fo.mu.Unlock()
			s.fo.publish()
		}
		s.recompute()
	}
	if _, ok := pl.saves["bundle_autorenew"]; ok {
		go s.autoRenewNow()
	}
	if pl.saves["update_check"] == "1" {
		s.startUpdateCheck(true)
	}
	for k, running := range map[string]string{"timezone": s.cfg.TimeZone, "gateway_ip": s.cfg.GatewayIP, "lan": s.cfg.LAN} {
		if v, ok := pl.saves[k]; ok && v != running {
			restart = append(restart, k)
		}
	}
	if pl.profiles != nil && pl.profiles.singbox && len(restart) == 0 {
		restart = append(restart, "profiles") // the failover reads its probes at start
	}
	return restart, nil
}
