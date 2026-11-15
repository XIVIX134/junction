package main

// First-run setup (v1.6). A new install shows the setup screen until it is
// finished; an install already in use (devices or bundle entries in its
// database) is marked as set up at start and never sees it.
//
// Junction doesn't edit sing-box's config: the VPN exits entered here are
// saved to exits-pending.json for the setup script to apply.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	procRoute        = "/proc/net/route"
	exitsPendingPath = "/etc/junction/exits-pending.json"
	internetCheckURL = "http://cp.cloudflare.com/generate_204"
)

type SetupDetected struct {
	GatewayIP  string  `json:"gatewayIP"`
	Iface      string  `json:"iface"`
	LAN        string  `json:"lan"`
	Router     string  `json:"router"`
	RouterOK   bool    `json:"routerOK"`
	InternetOK bool    `json:"internetOK"`
	Singbox    *string `json:"singbox"`
	RulesOK    bool    `json:"rulesOK"`
	Timezone   string  `json:"timezone"`
}

type SetupInfo struct {
	Needed   bool          `json:"needed"`
	Detected SetupDetected `json:"detected"`
}

type SetupBody struct {
	GatewayIP string      `json:"gatewayIP"`
	LAN       string      `json:"lan"`
	Timezone  string      `json:"timezone"`
	Brand     *BrandPatch `json:"brand"`
	Exits     []struct {
		Name     string          `json:"name"`
		Outbound json.RawMessage `json:"outbound"`
	} `json:"exits"`
	DevicesOnVPN []string `json:"devicesOnVPN"`
	DHCP         bool     `json:"dhcp"`
}

// markExistingSetup runs at start. setup_pending remembers that this install
// started out empty, so devices appearing before setup is finished don't hide it.
func (s *State) markExistingSetup() {
	if _, done := s.store.getSetting("setup_done"); done {
		return
	}
	if _, pending := s.store.getSetting("setup_pending"); pending {
		return
	}
	if s.store.hasHistory() {
		s.store.setSetting("setup_done", "existing")
		log.Printf("setup: this install is already in use; no setup screen")
		return
	}
	s.store.setSetting("setup_pending", "1")
}

func (s *State) setupNeeded() bool {
	_, done := s.store.getSetting("setup_done")
	return !done
}

// defaultRoute reads the default gateway (the router) from /proc/net/route.
func defaultRoute() (iface, gw string) {
	f, err := os.Open(procRoute)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fl := strings.Fields(sc.Text()) // Iface Destination Gateway Flags ...
		if len(fl) < 4 || fl[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(fl[2])
		if err != nil || len(b) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, binary.BigEndian.Uint32(b))
		return fl[0], ip.String()
	}
	return "", ""
}

// ifaceLAN returns an interface's first IPv4 address and its network.
func ifaceLAN(name string) (ip, lan string) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return "", ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			nw := &net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask}
			return n.IP.String(), nw.String()
		}
	}
	return "", ""
}

func tcpReachable(host string, d time.Duration) bool {
	if host == "" {
		return false
	}
	ok := make(chan bool, 2)
	for _, port := range []string{"80", "443"} {
		go func(port string) {
			c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), d)
			if err == nil {
				c.Close()
			}
			ok <- err == nil
		}(port)
	}
	return <-ok || <-ok
}

func internetOK(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, internetCheckURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

func (s *State) setupInfo(ctx context.Context) SetupInfo {
	iface := s.cfg.Interface
	rIface, router := defaultRoute()
	if _, err := net.InterfaceByName(iface); err != nil && rIface != "" {
		iface = rIface
	}
	ip, lan := ifaceLAN(iface)
	d := SetupDetected{GatewayIP: detectGatewayIP(iface), Iface: iface, LAN: lan, Router: router,
		Timezone: detectTimezone(s.base.TimeZone)}
	if d.GatewayIP == "" {
		d.GatewayIP = ip
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); d.RouterOK = tcpReachable(router, 2*time.Second) }()
	go func() { defer wg.Done(); d.InternetOK = internetOK(ctx) }()
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if v, _, err := s.singboxVersion(cctx); err == nil {
			d.Singbox = &v
		}
	}()
	go func() { defer wg.Done(); st, _ := rulesCheck(); d.RulesOK = st == "ok" }()
	wg.Wait()
	return SetupInfo{Needed: s.setupNeeded(), Detected: d}
}

func (a *API) getSetup(w http.ResponseWriter, r *http.Request) {
	send(w, a.s.setupInfo(r.Context()))
}

func (a *API) postSetup(w http.ResponseWriter, r *http.Request) {
	s := a.s
	if !s.setupNeeded() {
		fail(w, 409, "Setup is already done; change these in Settings.")
		return
	}
	var b SetupBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		fail(w, 400, "The request body must be the setup answers as JSON: "+err.Error())
		return
	}
	// Check what applyPatch doesn't, before anything is saved.
	for i, e := range b.Exits {
		var ob map[string]any
		if strings.TrimSpace(e.Name) == "" || utf8.RuneCountInString(e.Name) > 40 {
			fail(w, 400, fmt.Sprintf("VPN exit %d needs a name (up to 40 characters).", i+1))
			return
		}
		if json.Unmarshal(e.Outbound, &ob) != nil || ob["type"] == nil {
			fail(w, 400, fmt.Sprintf("The settings for VPN exit %q must be a sing-box outbound (JSON with a \"type\").", e.Name))
			return
		}
	}
	onVPN := map[string]bool{}
	s.mu.RLock()
	for _, id := range b.DevicesOnVPN {
		if _, ok := s.devs[id]; !ok {
			s.mu.RUnlock()
			fail(w, 400, "No device with id "+id+"; reload the page and pick again.")
			return
		}
		onVPN[id] = true
	}
	s.mu.RUnlock()

	p := SettingsPatch{Brand: b.Brand}
	if b.Timezone != "" {
		p.Timezone = &b.Timezone
	}
	if b.GatewayIP != "" || b.LAN != "" {
		p.Network = &NetworkPatch{}
		if b.GatewayIP != "" {
			p.Network.GatewayIP = &b.GatewayIP
		}
		if b.LAN != "" {
			p.Network.LAN = &b.LAN
		}
	}
	restart, ok := a.applyPatch(w, r, &p)
	if !ok {
		return
	}
	if len(onVPN) > 0 {
		if a.changeDevices(w, r, func(d *devState) bool { return onVPN[d.ID] }, func(d *devState) { d.VPN = true }) < 0 {
			return
		}
	}
	if len(b.Exits) > 0 || b.DHCP {
		pending, _ := json.MarshalIndent(map[string]any{
			"savedAt": time.Now().UTC().Format(time.RFC3339), "exits": b.Exits, "dhcp": b.DHCP,
		}, "", "  ")
		if err := writeFileAtomic(exitsPendingPath, append(pending, '\n'), 0o600); err != nil {
			fail(w, 500, "Could not save the VPN exits: "+err.Error())
			return
		}
		log.Printf("setup: saved %d VPN exit(s) and dhcp=%v to %s; the setup script applies them (Junction doesn't edit sing-box's config)",
			len(b.Exits), b.DHCP, exitsPendingPath)
	}
	if err := s.store.setSetting("setup_done", "1"); err != nil {
		fail(w, 500, "Could not save: "+err.Error())
		return
	}
	log.Printf("setup: finished (from %s)", clientIP(r))
	send(w, s.settings())
	s.restartSoon(restart)
}

// writeFileAtomic writes via a temporary file and a rename, so a reader never sees half a file.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
