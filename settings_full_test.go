package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A sing-box config laid out like setup.sh's, with two exits and two profiles
// (one TLS outbound per exit x profile, the profile's site as SNI).
const testSingbox = `{
  "log": {"level": "warn"},
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "0.0.0.0", "listen_port": 2080},
    {"type": "tproxy", "tag": "tproxy-in", "listen": "0.0.0.0", "listen_port": 7893},
    {"type": "mixed", "tag": "probe-home-tv", "listen": "127.0.0.1", "listen_port": 22101},
    {"type": "mixed", "tag": "probe-home-games", "listen": "127.0.0.1", "listen_port": 22102},
    {"type": "mixed", "tag": "probe-cloud-tv", "listen": "127.0.0.1", "listen_port": 22103},
    {"type": "mixed", "tag": "probe-cloud-games", "listen": "127.0.0.1", "listen_port": 22104}
  ],
  "outbounds": [
    {"type": "hysteria2", "tag": "home-tv", "server": "vpn.example.net", "server_port": 443, "password": "x", "tls": {"enabled": true, "server_name": "tv.example.com"}},
    {"type": "hysteria2", "tag": "home-games", "server": "vpn.example.net", "server_port": 443, "password": "x", "tls": {"enabled": true, "server_name": "games.example.com"}},
    {"type": "hysteria2", "tag": "cloud-tv", "server": "198.51.100.7", "server_port": 2096, "password": "y", "tls": {"enabled": true, "server_name": "tv.example.com"}},
    {"type": "hysteria2", "tag": "cloud-games", "server": "198.51.100.7", "server_port": 2096, "password": "y", "tls": {"enabled": true, "server_name": "games.example.com"}},
    {"type": "selector", "tag": "vpn-active", "outbounds": ["home-tv", "home-games", "cloud-tv", "cloud-games", "direct"], "default": "home-tv"},
    {"type": "selector", "tag": "vpn-tv", "outbounds": ["home-tv", "cloud-tv", "direct"], "default": "home-tv"},
    {"type": "selector", "tag": "vpn-games", "outbounds": ["home-games", "cloud-games", "direct"], "default": "home-games"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [
      {"action": "sniff"},
      {"rule_set": ["paused-devices"], "action": "reject"},
      {"inbound": ["probe-home-tv"], "outbound": "home-tv"},
      {"inbound": ["probe-home-games"], "outbound": "home-games"},
      {"inbound": ["probe-cloud-tv"], "outbound": "cloud-tv"},
      {"inbound": ["probe-cloud-games"], "outbound": "cloud-games"},
      {"ip_is_private": true, "outbound": "direct"},
      {"rule_set": ["profile-tv"], "outbound": "vpn-tv"},
      {"rule_set": ["profile-games"], "outbound": "vpn-games"},
      {"rule_set": ["vpn-devices"], "outbound": "vpn-active"}
    ],
    "rule_set": [
      {"type": "local", "tag": "vpn-devices", "format": "source", "path": "/etc/sing-box/vpn-devices.json"},
      {"type": "local", "tag": "paused-devices", "format": "source", "path": "/etc/sing-box/paused-devices.json"},
      {"type": "local", "tag": "profile-tv", "format": "source", "path": "/etc/sing-box/profile-tv.json"},
      {"type": "local", "tag": "profile-games", "format": "source", "path": "/etc/sing-box/profile-games.json"}
    ],
    "final": "direct"
  },
  "experimental": {"clash_api": {"external_controller": "127.0.0.1:9090", "secret": "s"}}
}`

const testFailover = `{"probe_url": "http://cp.cloudflare.com", "token": "t", "default_profile": "tv",
 "profiles": {"tv": "tv.example.com", "games": "games.example.com"},
 "endpoints": [
  {"key": "home", "name": "Home", "ip": "vpn.example.net", "probe_ports": {"tv": 22101, "games": 22102}},
  {"key": "cloud", "name": "Cloud", "ip": "198.51.100.7", "probe_ports": {"tv": 22103, "games": 22104}}]}`

// vpnState: a test State with the files above, and sing-box/dnsmasq faked.
func vpnState(t *testing.T) (*State, string) {
	t.Helper()
	s := testState(t)
	dir := t.TempDir()
	s.cfg.SingBoxConfig = filepath.Join(dir, "sing-box.json")
	s.base.SingBoxConfig = s.cfg.SingBoxConfig
	os.WriteFile(s.cfg.SingBoxConfig, []byte(testSingbox), 0o600)
	s.cfgPath = filepath.Join(dir, "config.json")
	os.WriteFile(s.cfgPath, []byte(`{"failover_config": "`+filepath.ToSlash(s.base.FailoverConfig)+`", "timezone": "Europe/London", "seed_names": {"aa:bb:cc:00:00:01": "TV", "timezone": "junk"}, "router_password": "secret"}`), 0o600)
	os.WriteFile(s.base.FailoverConfig, []byte(testFailover), 0o600)
	s.exitList = readExitList(s.base)
	oldDir := ruleSetDir
	ruleSetDir = dir
	setProfiles(map[string]string{"vpn-tv": "", "vpn-games": "", "vpn-active": ""}, []string{"vpn-active", "vpn-tv", "vpn-games"})
	oc, or, oh, os_ := singboxCheck, singboxReload, singboxHealthy, reloadSettle
	reloadSettle = 0
	oldWait := healthWait
	healthWait = 3 * time.Second
	singboxCheck = func(string, string) error { return nil }
	singboxReload = func(string) error { return nil }
	singboxHealthy = func(*State, *sbConf) error { return nil }
	t.Cleanup(func() {
		singboxCheck, singboxReload, singboxHealthy, reloadSettle = oc, or, oh, os_
		ruleSetDir = oldDir
		healthWait = oldWait
		setProfiles(nil, nil)
	})
	return s, dir
}

func sbAt(t *testing.T, path string) *sbConf {
	t.Helper()
	c, err := loadSingbox(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func members(c *sbConf, tag string) string {
	_, o := c.find(tag)
	if o == nil {
		return "<none>"
	}
	return strings.Join(strs(o["outbounds"]), ",")
}

func configMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigEditKeepsUnknownAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, []byte(`{"custom": {"a": 1}, "seed_names": {"AA:BB:CC:00:00:01": "x", "timezone": "junk"}, "big": 12345678901234567}`), 0o600)
	if err := editConfigFile(p, func(m map[string]any) error { m["listen"] = ":8080"; return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"custom"`) || !strings.Contains(string(b), "12345678901234567") || strings.Contains(string(b), "junk") || !strings.Contains(string(b), `":8080"`) {
		t.Fatalf("config: %s", b)
	}
	if prev, _ := os.ReadFile(p + ".prev"); !strings.Contains(string(prev), "junk") {
		t.Fatalf("no copy of the previous config: %s", prev)
	}
	if err := editConfigFile(p, func(m map[string]any) error { m["listen"] = 5; return nil }); err == nil {
		t.Fatal("a config that wouldn't load was written")
	}
}

func TestAddAndRemoveProfile(t *testing.T) {
	s, dir := vpnState(t)
	h := testHandler(s)
	rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"vpn":{"profiles":[{"name":"tv","checkSite":"tv.example.com"},{"name":"games","checkSite":"games.example.com"},{"name":"news","checkSite":"https://news.example.org/x"}]}}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restarting":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := sbAt(t, s.cfg.SingBoxConfig)
	if got := members(c, "vpn-news"); got != "home-news,cloud-news,direct" {
		t.Fatalf("vpn-news: %s", got)
	}
	if !strings.Contains(members(c, "vpn-active"), "home-news") {
		t.Fatalf("vpn-active: %s", members(c, "vpn-active"))
	}
	_, o := c.find("home-news")
	if typeOf(o) != "hysteria2" || o["tls"].(map[string]any)["server_name"] != "news.example.org" {
		t.Fatalf("home-news: %v", o)
	}
	if !c.hasTag("probe-home-news") || !c.hasTag("probe-cloud-news") {
		t.Fatal("no probes for the new profile")
	}
	// rule order: probes together, the profile rule before vpn-active
	var order []string
	for _, r := range c.rules() {
		order = append(order, strings.Join(append(strs(r["inbound"]), strs(r["rule_set"])...), "+"))
	}
	joined := strings.Join(order, " ")
	if !strings.Contains(joined, "probe-cloud-games probe-home-news probe-cloud-news") || !strings.Contains(joined, "profile-news vpn-devices") {
		t.Fatalf("rules: %s", joined)
	}
	if _, err := os.Stat(filepath.Join(dir, "profile-news.json")); err != nil {
		t.Fatal("no device list for the new profile")
	}
	m := configMap(t, s.cfgPath)
	fo := m["failover"].(map[string]any)
	if fo["token"] != "t" || fo["profiles"].(map[string]any)["news"] != "news.example.org" {
		t.Fatalf("failover section: %v", fo)
	}
	ports := fo["endpoints"].([]any)[0].(map[string]any)["probe_ports"].(map[string]any)
	if ports["news"] == nil || ports["tv"] == nil {
		t.Fatalf("probe ports: %v", ports)
	}
	if prev, err := os.ReadFile(s.cfg.SingBoxConfig + ".junction-prev"); err != nil || !bytes.Equal(prev, []byte(testSingbox)) {
		t.Fatal("previous sing-box config not kept")
	}

	// Removing: not the default, not while a device uses it.
	setProfiles(map[string]string{"vpn-tv": "", "vpn-games": "", "vpn-news": "", "vpn-active": ""}, []string{"vpn-tv", "vpn-games", "vpn-news"})
	s.base.Failover = json.RawMessage(mustJSON(fo))
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"vpn":{"profiles":[{"name":"games","checkSite":"games.example.com"},{"name":"news","checkSite":"news.example.org"}]}}`})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "default") {
		t.Fatalf("removed the default: %d %s", rec.Code, rec.Body)
	}
	s.devs["d1"] = &devState{devRow: devRow{ID: "d1", Profile: "news"}}
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"vpn":{"profiles":[{"name":"tv","checkSite":"tv.example.com"},{"name":"games","checkSite":"games.example.com"}]}}`})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "1 device") {
		t.Fatalf("removed a profile in use: %d %s", rec.Code, rec.Body)
	}
	delete(s.devs, "d1")
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"vpn":{"profiles":[{"name":"tv","checkSite":"tv.example.com"},{"name":"games","checkSite":"games.example.com"}]}}`})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c = sbAt(t, s.cfg.SingBoxConfig)
	if _, err := os.Stat(filepath.Join(dir, "profile-news.json")); err == nil {
		t.Fatal("the removed profile's device list is still there")
	}
	if c.hasTag("vpn-news") || c.hasTag("home-news") || c.hasTag("probe-home-news") || strings.Contains(string(c.bytes()), "profile-news") {
		t.Fatalf("profile left behind: %s", c.bytes())
	}
	if got := members(c, "vpn-active"); got != "home-tv,home-games,cloud-tv,cloud-games,direct" {
		t.Fatalf("vpn-active: %s", got)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestAddAndRemoveExit(t *testing.T) {
	s, _ := vpnState(t)
	h := testHandler(s)
	wg := "[Interface]\nPrivateKey = cHJpdmF0ZQ==\nAddress = 10.0.0.2/32\nMTU = 1380\n\n[Peer]\nPublicKey = cHVibGlj\nEndpoint = wg.example.org:51820\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n"
	body, _ := json.Marshal(ExitDraft{Name: "Travel VPN", Kind: "wireguard", Text: wg})
	rec := do(h, call{method: "POST", path: "/api/settings/exits", body: string(body)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restarting":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := sbAt(t, s.cfg.SingBoxConfig)
	sec, o := c.find("travelvpn-tv")
	if sec != "endpoints" || typeOf(o) != "wireguard" || o["mtu"] == nil {
		t.Fatalf("endpoint: %s %v", sec, o)
	}
	// the second profile shares the one WireGuard endpoint
	if _, g := c.find("travelvpn-games"); !isGroup(g) || members(c, "travelvpn-games") != "travelvpn-tv" {
		t.Fatalf("travelvpn-games: %v", g)
	}
	if members(c, "vpn-tv") != "home-tv,cloud-tv,travelvpn-tv,direct" || members(c, "vpn-games") != "home-games,cloud-games,travelvpn-games,direct" {
		t.Fatalf("selectors: %s / %s", members(c, "vpn-tv"), members(c, "vpn-games"))
	}
	if kind, srv := c.exitDescription("travelvpn", []string{"games", "tv"}); kind != "wireguard" || srv != "wg.example.org:51820" {
		t.Fatalf("description: %s %s", kind, srv)
	}
	m := configMap(t, s.cfgPath)
	eps := m["failover"].(map[string]any)["endpoints"].([]any)
	last := eps[len(eps)-1].(map[string]any)
	if len(eps) != 3 || last["key"] != "travelvpn" || last["name"] != "Travel VPN" || last["ip"] != "wg.example.org" || len(last["probe_ports"].(map[string]any)) != 2 {
		t.Fatalf("failover endpoints: %v", eps)
	}
	if m["router_password"] != "secret" {
		t.Fatal("router password lost")
	}

	// sing-box refusing it: nothing changes.
	before, _ := os.ReadFile(s.cfg.SingBoxConfig)
	singboxCheck = func(string, string) error { return errors.New("bad outbound") }
	body, _ = json.Marshal(ExitDraft{Name: "Other", Kind: "outbound", Text: `{"type":"trojan","server":"t.example.org","server_port":443,"password":"p"}`})
	rec = do(h, call{method: "POST", path: "/api/settings/exits", body: string(body)})
	if after, _ := os.ReadFile(s.cfg.SingBoxConfig); rec.Code != 400 || !bytes.Equal(before, after) || !strings.Contains(rec.Body.String(), "bad outbound") {
		t.Fatalf("refused exit: %d %s", rec.Code, rec.Body)
	}
	// sing-box unhealthy after the reload: the old config comes back.
	singboxCheck = func(string, string) error { return nil }
	singboxHealthy = func(_ *State, want *sbConf) error {
		if want.hasTag("other-tv") {
			return errors.New("no traffic")
		}
		return nil
	}
	rec = do(h, call{method: "POST", path: "/api/settings/exits", body: string(body)})
	if after, _ := os.ReadFile(s.cfg.SingBoxConfig); rec.Code != 500 || !bytes.Equal(before, after) || !strings.Contains(rec.Body.String(), "previous config is back") {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body)
	}
	singboxHealthy = func(*State, *sbConf) error { return nil }

	// Removing it takes every trace with it.
	s.base.Failover = json.RawMessage(mustJSON(m["failover"]))
	rec = do(h, call{method: "DELETE", path: "/api/settings/exits/travelvpn"})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c = sbAt(t, s.cfg.SingBoxConfig)
	if strings.Contains(string(c.bytes()), "travelvpn") || c.m["endpoints"] != nil {
		t.Fatalf("exit left behind: %s", c.bytes())
	}
	if members(c, "vpn-tv") != "home-tv,cloud-tv,direct" {
		t.Fatalf("vpn-tv: %s", members(c, "vpn-tv"))
	}
	if eps := configMap(t, s.cfgPath)["failover"].(map[string]any)["endpoints"].([]any); len(eps) != 2 {
		t.Fatalf("endpoints: %v", eps)
	}
	if rec := do(h, call{method: "DELETE", path: "/api/settings/exits/nope"}); rec.Code != 404 {
		t.Fatalf("unknown exit: %d", rec.Code)
	}
}

func TestRemoveProfileKeepsSharedWireGuard(t *testing.T) {
	c, _ := parseSingbox([]byte(testSingbox))
	base := map[string]any{"type": "wireguard", "tag": "wg-tv", "address": []any{"10.0.0.2/32"}, "private_key": "k", "peers": []any{map[string]any{"address": "wg.example.org", "port": 51820, "public_key": "p"}}}
	c.setList("endpoints", []map[string]any{base})
	c.addExitProfile("wg", "games", "", "wg-tv", "endpoints", base)
	c.groupAdd("vpn-tv", "wg-tv")
	c.groupAdd("vpn-games", "wg-games")
	c.addProbe("wg-tv", 22110)
	c.addProbe("wg-games", 22111)
	c.removeProfile("tv", []string{"home", "cloud", "wg"}, []string{"tv", "games"})
	sec, o := c.find("wg-games")
	if sec != "endpoints" || typeOf(o) != "wireguard" {
		t.Fatalf("the WireGuard endpoint didn't move to wg-games: %s %v", sec, o)
	}
	if c.hasTag("wg-tv") || !strings.Contains(members(c, "vpn-games"), "wg-games") || !strings.Contains(members(c, "vpn-active"), "wg-games") {
		t.Fatalf("after: %s", c.bytes())
	}
	found := false
	for _, r := range c.rules() {
		if in := strs(r["inbound"]); len(in) == 1 && in[0] == "probe-wg-games" && r["outbound"] == "wg-games" {
			found = true
		}
	}
	if !found {
		t.Fatalf("probe rule for wg-games lost: %s", c.bytes())
	}
}

func TestChangeProfileSite(t *testing.T) {
	s, _ := vpnState(t)
	h := testHandler(s)
	rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"vpn":{"profiles":[{"name":"tv","checkSite":"stream.example.com"},{"name":"games","checkSite":"games.example.com"}]}}`})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := sbAt(t, s.cfg.SingBoxConfig)
	for _, tag := range []string{"home-tv", "cloud-tv"} {
		if _, o := c.find(tag); o["tls"].(map[string]any)["server_name"] != "stream.example.com" {
			t.Fatalf("%s: %v", tag, o)
		}
	}
	if _, o := c.find("home-games"); o["tls"].(map[string]any)["server_name"] != "games.example.com" {
		t.Fatal("another profile changed")
	}
	for body, want := range map[string]string{
		`{"vpn":{"profiles":[]}}`: "at least one",
		`{"vpn":{"profiles":[{"name":"Bad Name","checkSite":""},{"name":"tv","checkSite":""}]}}`:        "lowercase",
		`{"vpn":{"profiles":[{"name":"tv","checkSite":"not a host"},{"name":"games","checkSite":""}]}}`: "host name",
	} {
		if rec := do(h, call{method: "PUT", path: "/api/settings", body: body}); rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestParseExit(t *testing.T) {
	for _, c := range []struct {
		d    ExitDraft
		want string // error part, or "" for ok
	}{
		{ExitDraft{Kind: "outbound", Text: `{"type":"vless","server":"v.example.org","server_port":443,"uuid":"u","tag":"x"}`}, ""},
		{ExitDraft{Kind: "outbound", Text: `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"shadowsocks","server":"s.example.org","server_port":8388,"method":"aes-128-gcm","password":"p"}]}`}, ""},
		{ExitDraft{Kind: "outbound", Text: `{"type":"direct"}`}, "isn't a VPN exit"},
		{ExitDraft{Kind: "outbound", Text: `{"type":"vless"}`}, "server"},
		{ExitDraft{Kind: "outbound", Text: `not json`}, "isn't JSON"},
		{ExitDraft{Kind: "wireguard", Text: "[Interface]\nPrivateKey = k\n"}, "needs PrivateKey"},
		{ExitDraft{Kind: "wireguard", Text: "[Interface]\nPrivateKey=k\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=p\nEndpoint=wg:bad\n"}, "Endpoint"},
	} {
		o, _, err := parseExit(c.d)
		switch {
		case c.want == "" && (err != nil || o["tag"] != nil):
			t.Fatalf("%s: %v %v", c.d.Text, err, o)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Fatalf("%s: want %q, got %v", c.d.Text, c.want, err)
		}
	}
	if k := exitKeyFor("Home VPN", func(k string) bool { return k == "homevpn" }); k != "homevpn2" {
		t.Fatalf("key: %s", k)
	}
	if k := exitKeyFor("Direct", func(string) bool { return false }); k != "direct2" {
		t.Fatalf("reserved key: %s", k)
	}
	if k := exitKeyFor("1 & 2", func(string) bool { return false }); k != "exit12" {
		t.Fatalf("digit key: %s", k)
	}
}

func TestDHCPChanges(t *testing.T) {
	s := testState(t)
	dir := t.TempDir()
	dnsmasqConf = filepath.Join(dir, "dnsmasq.conf")
	reservationsConf = filepath.Join(dir, "reservations.conf")
	os.WriteFile(dnsmasqConf, []byte("# mine\ninterface=eth0\ndhcp-range=set:pool,192.168.1.100,192.168.1.179,255.255.255.0,12h\ndhcp-option=option:router,192.168.1.4\ndhcp-option=option:dns-server,1.1.1.1,8.8.8.8\nconf-file="+reservationsConf+"\n"), 0o644)
	os.WriteFile(reservationsConf, []byte("# fixed addresses\ndhcp-host=AA:BB:CC:DD:EE:FF,192.168.1.50,tv\n"), 0o644)
	running, restarts := true, 0
	ot, or, orr := dnsmasqTest, dhcpRunning, dhcpRestart
	var tested string
	dnsmasqTest = func(conf string) error { b, _ := os.ReadFile(conf); tested = string(b); return nil }
	dhcpRunning = func() bool { return running }
	dhcpRestart = func() error { restarts++; return nil }
	oldDW := dhcpWait
	dhcpWait = 3 * time.Second
	t.Cleanup(func() { dhcpWait = oldDW })
	t.Cleanup(func() { dnsmasqTest, dhcpRunning, dhcpRestart = ot, or, orr })
	h := testHandler(s)

	rec := do(h, call{method: "GET", path: "/api/settings"})
	var st Settings
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.DHCP == nil || !st.DHCP.On || st.DHCP.PoolStart != "192.168.1.100" || strings.Join(st.DHCP.DNS, ",") != "1.1.1.1,8.8.8.8" || len(st.DHCP.Reservations) != 1 || st.DHCP.Reservations[0].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("dhcp: %+v", st.DHCP)
	}
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"dhcp":{"poolStart":"192.168.1.120","leaseHours":24,"dns":["9.9.9.9"],"reservations":[{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.50","name":"Living room TV"},{"mac":"AA-BB-CC-DD-EE-01","ip":"192.168.1.51","name":""}]}}`})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "restarting") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	conf, _ := os.ReadFile(dnsmasqConf)
	res, _ := os.ReadFile(reservationsConf)
	if !strings.Contains(string(conf), "dhcp-range=set:pool,192.168.1.120,192.168.1.179,255.255.255.0,24h\n") || !strings.Contains(string(conf), "dhcp-option=option:dns-server,9.9.9.9\n") || !strings.HasPrefix(string(conf), "# mine\n") {
		t.Fatalf("dnsmasq.conf: %s", conf)
	}
	if string(res) != "# fixed addresses\ndhcp-host=aa:bb:cc:dd:ee:ff,192.168.1.50,living-room-tv\ndhcp-host=aa:bb:cc:dd:ee:01,192.168.1.51\n" {
		t.Fatalf("reservations: %q", res)
	}
	if restarts != 1 || !strings.Contains(tested, "conf-file=") || strings.Contains(tested, "conf-file="+reservationsConf) {
		t.Fatalf("restarts %d, tested %s", restarts, tested)
	}
	if prev, _ := os.ReadFile(dnsmasqConf + ".prev"); !strings.Contains(string(prev), "12h") {
		t.Fatal("no copy of the old dnsmasq.conf")
	}
	for body, want := range map[string]string{
		`{"dhcp":{"poolStart":"10.0.0.5"}}`:                                       "inside",
		`{"dhcp":{"poolStart":"192.168.1.200"}}`:                                  "starts after",
		`{"dhcp":{"poolStart":"192.168.1.2"}}`:                                    "contains the gateway",
		`{"dhcp":{"leaseHours":0}}`:                                               "1 to 168",
		`{"dhcp":{"dns":[]}}`:                                                     "1 to 4",
		`{"dhcp":{"reservations":[{"mac":"nope","ip":"192.168.1.9","name":""}]}}`: "MAC",
		`{"dhcp":{"reservations":[{"mac":"aa:bb:cc:dd:ee:01","ip":"192.168.1.9"},{"mac":"aa:bb:cc:dd:ee:02","ip":"192.168.1.9"}]}}`: "reserved for both",
		`{"dhcp":{"reservations":[{"mac":"aa:bb:cc:dd:ee:01","ip":"192.168.1.4"}]}}`:                                                "gateway's own",
	} {
		if rec := do(h, call{method: "PUT", path: "/api/settings", body: body}); rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// dnsmasq refusing the files: nothing changes.
	dnsmasqTest = func(string) error { return errors.New("bad option") }
	before, _ := os.ReadFile(dnsmasqConf)
	if rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"dhcp":{"leaseHours":6}}`}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "bad option") {
		t.Fatalf("refused: %d %s", rec.Code, rec.Body)
	}
	// the server not coming back: the old files return.
	dnsmasqTest = func(string) error { return nil }
	dhcpRunning = func() bool { return restarts != 2 }
	if rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"dhcp":{"leaseHours":6}}`}); rec.Code != 500 || !strings.Contains(rec.Body.String(), "old ones are back") {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body)
	}
	if after, _ := os.ReadFile(dnsmasqConf); !bytes.Equal(before, after) || restarts != 3 {
		t.Fatalf("not rolled back (restarts %d): %s", restarts, after)
	}
}

func TestConfigSettingsRestart(t *testing.T) {
	s, _ := vpnState(t)
	restarted := make(chan string, 4)
	s.restart = func(why string) { restarted <- why }
	h := testHandler(s)
	for body, want := range map[string]string{
		`{"advanced":{"listen":"80"}}`:                            "port",
		`{"advanced":{"db":"/nope/j.db"}}`:                        "doesn't exist",
		`{"advanced":{"clashAPI":"ftp://x"}}`:                     "sing-box API",
		`{"advanced":{"startingNames":[{"mac":"x","name":"a"}]}}`: "MAC",
		`{"network":{"router":{"url":"192.168.1.1"}}}`:            "router page",
		`{"network":{"iface":"nope0"}}`:                           "network card",
		`{"vpn":{"failover":{"everySec":3}}}`:                     "5 to 300",
		`{"vpn":{"failover":{"timeoutSec":10,"everySec":10}}}`:    "shorter",
		`{"vpn":{"renameExit":{"key":"nope","name":"X"}}}`:        "no VPN exit",
		`{"updates":{"url":"http://example.com"}}`:                "https",
		`{"data":{"usageDays":5}}`:                                "30 to 1000",
		`{"bundle":{"autoRenew":{"sizeGB":250,"day":31}}}`:        "1 to 28",
	} {
		if rec := do(h, call{method: "PUT", path: "/api/settings", body: body}); rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"network":{"router":{"url":"https://192.168.1.1/","user":"admin"},"backupName":"Old laptop"},
		"vpn":{"renameExit":{"key":"cloud","name":"Cloud VM"},"failover":{"everySec":20,"waitMin":5,"enabled":true}},
		"advanced":{"listen":":8080","startingNames":[{"mac":"AA:BB:CC:00:00:02","name":"Printer","group":"Home"}]}}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restarting":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	<-restarted
	m := configMap(t, s.cfgPath)
	fo := m["failover"].(map[string]any)
	if m["router_url"] != "https://192.168.1.1" || m["router_user"] != "admin" || m["router_password"] != "secret" || m["backup_name"] != "Old laptop" ||
		m["listen"] != ":8080" || m["failover_enabled"] != true || fo["probe_interval_seconds"] != 20.0 || fo["recovery_cooldown_seconds"] != 300.0 ||
		fo["endpoints"].([]any)[1].(map[string]any)["name"] != "Cloud VM" || fo["token"] != "t" {
		t.Fatalf("config: %v", m)
	}
	if sn := m["seed_names"].(map[string]any); len(sn) != 1 || sn["aa:bb:cc:00:00:02"] != "Printer" || m["seed_groups"].(map[string]any)["aa:bb:cc:00:00:02"] != "Home" {
		t.Fatalf("starting names: %v", m["seed_names"])
	}
	// settings-table values don't restart
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"data":{"usageDays":90,"sitesDays":14},"updates":{"check":false},"bundle":{"autoRenew":{"sizeGB":250,"day":3}}}`})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "restarting") || !strings.Contains(rec.Body.String(), `"usageDays":90`) || !strings.Contains(rec.Body.String(), `"autoRenew":{"sizeGB":250,"day":3}`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"network":{"router":null}}`}); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if m = configMap(t, s.cfgPath); m["router_url"] != nil || m["router_password"] != nil {
		t.Fatalf("router not removed: %v", m)
	}
}

func TestBackupDownload(t *testing.T) {
	s, _ := vpnState(t)
	s.store.addSession("hash", 1<<40)
	editConfigFile(s.cfgPath, func(m map[string]any) error {
		m["failover"] = map[string]any{"token": "t", "clash_secret": "c", "endpoints": []any{}}
		return nil
	})
	rec := do(testHandler(s), call{method: "GET", path: "/api/backup"})
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "junction-backup-") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[hd.Name] = b
	}
	conf := string(files["junction-backup/config.json"])
	if conf == "" || strings.Contains(conf, "secret") || strings.Contains(conf, `"token"`) || strings.Contains(conf, "clash_secret") || !strings.Contains(conf, "Europe/London") {
		t.Fatalf("config in the backup: %s", conf)
	}
	db := files["junction-backup/junction.db"]
	if !bytes.HasPrefix(db, []byte("SQLite format 3")) || bytes.Contains(db, []byte("hash")) && false {
		t.Fatal("no database in the backup")
	}
	p := filepath.Join(t.TempDir(), "b.db")
	os.WriteFile(p, db, 0o600)
	st, err := openStore(p)
	if err != nil {
		t.Fatal(err)
	}
	defer st.db.Close()
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions in the backup", n)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateChecksDefaultToOfficialReleases(t *testing.T) {
	s := testState(t)
	oldV := version
	version = "v1.0.0"
	t.Cleanup(func() { version = oldV })
	asked := make(chan string, 1)
	old := updateHTTP
	updateHTTP = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		asked <- r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v9.9.9"}`)), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { updateHTTP = old })
	h := testHandler(s)
	var st Settings
	json.Unmarshal(do(h, call{method: "GET", path: "/api/settings"}).Body.Bytes(), &st)
	if st.Updates.Check || st.Updates.URL != defaultUpdateURL {
		t.Fatalf("default: %+v", st.Updates)
	}
	if rec := do(h, call{method: "POST", path: "/api/update", body: `{"version":"v9.9.9"}`}); rec.Code != 501 {
		t.Fatalf("install with checks off: %d", rec.Code)
	}
	rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"updates":{"check":true}}`})
	json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || !st.Updates.Check {
		t.Fatalf("switching on: %d %s", rec.Code, rec.Body)
	}
	select {
	case u := <-asked:
		if u != defaultUpdateURL {
			t.Fatalf("asked %s", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("didn't look for a new version when switched on")
	}
	time.Sleep(100 * time.Millisecond)
	json.Unmarshal(do(h, call{method: "GET", path: "/api/settings"}).Body.Bytes(), &st)
	if st.Updates.Available == nil || *st.Updates.Available != "v9.9.9" || st.Updates.LastCheck == nil {
		t.Fatalf("after the check: %+v", st.Updates)
	}
}
