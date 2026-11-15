package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// testState is a State with a fresh database and every file it reads in a temp dir.
func testState(t *testing.T) *State {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(filepath.Join(dir, "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	for _, v := range []*string{&keepalivedConf, &dnsmasqConf, &reservationsConf, &updatesLog, &procRoute, &exitsPendingPath} {
		old := *v
		*v = filepath.Join(dir, "missing-"+filepath.Base(old))
		t.Cleanup(func() { *v = old })
	}
	cfg := defaultConfig()
	cfg.LAN = "192.168.1.0/24" // normally detected from the interface
	cfg.DB = filepath.Join(dir, "j.db")
	cfg.FailoverConfig = filepath.Join(dir, "failover.json")
	cfg.DnsmasqLeases = filepath.Join(dir, "leases")
	cfg.GatewayIP = "192.168.1.4"
	s := &State{cfg: cfg, base: cfg, store: st, loc: time.UTC, devs: map[string]*devState{}, selectors: map[string]string{},
		eff: map[string]Effective{}, exempt: map[[2]string]int64{}, ipToID: map[string]string{}, restart: func(string) {}}
	s.brand = s.loadBrand()
	s.auth = newAuth(st)
	return s
}

func testHandler(s *State) http.Handler {
	mux := http.NewServeMux()
	(&API{s: s}).routes(mux, fstest.MapFS{"index.html": {Data: []byte("<html>")}})
	return s.auth.middleware(mux)
}

type call struct {
	method, path, body string
	cookie             *http.Cookie
	ip                 string
}

func do(h http.Handler, c call) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.ip != "" {
		req.RemoteAddr = c.ip + ":5555"
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
				t.Fatalf("session cookie must be HttpOnly, SameSite=Strict, Path=/: %+v", c)
			}
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Result().Header)
	return nil
}

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$600000$") || strings.Count(h, "$") != 3 {
		t.Fatalf("hash format: %s", h)
	}
	if !checkPassword("correct horse", h) || checkPassword("correct horsE", h) || checkPassword("", h) {
		t.Fatal("password check is wrong")
	}
	h2, _ := hashPassword("correct horse")
	if h2 == h {
		t.Fatal("two hashes of one password must differ (random salt)")
	}
	for _, bad := range []string{"", "x", "pbkdf2-sha256$0$AA$AA", "sha1$1000$AA$AA", "pbkdf2-sha256$1000$!!$AA"} {
		if checkPassword("correct horse", bad) {
			t.Fatalf("accepted malformed hash %q", bad)
		}
	}
}

func fastHash(t *testing.T) {
	old := pbkdf2Iter
	pbkdf2Iter = 1000
	t.Cleanup(func() { pbkdf2Iter = old })
}

func TestSignInFlow(t *testing.T) {
	fastHash(t)
	s := testState(t)
	h := testHandler(s)

	// Open by default.
	if rec := do(h, call{method: "GET", path: "/api/rules"}); rec.Code != 200 {
		t.Fatalf("API must be open without a password: %d", rec.Code)
	}
	if rec := do(h, call{method: "PUT", path: "/api/settings", body: `{"access":{"required":true}}`}); rec.Code != 400 {
		t.Fatalf("sign-in can't be required without a password: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, call{method: "POST", path: "/api/settings/password", body: `{"password":"short"}`}); rec.Code != 400 {
		t.Fatalf("short password accepted: %d", rec.Code)
	}
	rec := do(h, call{method: "POST", path: "/api/settings/password", body: `{"password":"long enough"}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"passwordSet":true`) {
		t.Fatalf("set password: %d %s", rec.Code, rec.Body)
	}
	if v, _ := s.store.getSetting("auth_hash"); !strings.HasPrefix(v, "pbkdf2-sha256$") || strings.Contains(v, "long enough") {
		t.Fatalf("stored hash: %q", v)
	}
	// Whoever turns sign-in on keeps a session.
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"access":{"required":true,"sessionDays":7}}`})
	if rec.Code != 200 {
		t.Fatalf("require sign-in: %d %s", rec.Code, rec.Body)
	}
	mine := sessionFrom(t, rec)
	if mine.MaxAge != 7*86400 {
		t.Fatalf("session should last 7 days, MaxAge=%d", mine.MaxAge)
	}

	// Now the API is closed; the app's files and session/login are not.
	for _, p := range []string{"/api/rules", "/api/overview", "/api/stream", "/api/settings", "/api/health"} {
		if rec := do(h, call{method: "GET", path: p}); rec.Code != 401 {
			t.Fatalf("%s without a session: %d", p, rec.Code)
		}
	}
	if rec := do(h, call{method: "GET", path: "/"}); rec.Code != 200 {
		t.Fatalf("static files must stay public: %d", rec.Code)
	}
	if rec := do(h, call{method: "GET", path: "/api/session"}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"required":true,"signedIn":false`) {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, call{method: "GET", path: "/api/rules", cookie: mine}); rec.Code != 200 {
		t.Fatalf("with a session: %d", rec.Code)
	}
	if rec := do(h, call{method: "GET", path: "/api/rules", cookie: &http.Cookie{Name: sessionCookie, Value: mine.Value + "x"}}); rec.Code != 401 {
		t.Fatalf("a wrong cookie must not work: %d", rec.Code)
	}

	// Login.
	if rec := do(h, call{method: "POST", path: "/api/login", body: `{"password":"wrong one"}`}); rec.Code != 401 {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec = do(h, call{method: "POST", path: "/api/login", body: `{"password":"long enough"}`})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"signedIn":true`) {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	other := sessionFrom(t, rec)
	if rec := do(h, call{method: "GET", path: "/api/session", cookie: other}); !strings.Contains(rec.Body.String(), `"signedIn":true`) {
		t.Fatalf("session after login: %s", rec.Body)
	}
	// Sessions are stored hashed and survive a restart.
	if n := len(newAuth(s.store).sessions); n != 2 {
		t.Fatalf("sessions in the database: %d", n)
	}
	var stored int
	s.store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE hash = ?`, other.Value).Scan(&stored)
	if stored != 0 {
		t.Fatal("the cookie value itself must never be stored")
	}

	// Logout ends that session only.
	if rec := do(h, call{method: "POST", path: "/api/logout", cookie: other}); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := do(h, call{method: "GET", path: "/api/rules", cookie: other}); rec.Code != 401 {
		t.Fatalf("after logout: %d", rec.Code)
	}
	if rec := do(h, call{method: "GET", path: "/api/rules", cookie: mine}); rec.Code != 200 {
		t.Fatalf("other sessions must survive a logout: %d", rec.Code)
	}

	// A new password signs out every other browser.
	if rec := do(h, call{method: "POST", path: "/api/settings/password", body: `{"password":"another one"}`, cookie: mine}); rec.Code != 200 {
		t.Fatalf("change password: %d", rec.Code)
	}
	if rec := do(h, call{method: "GET", path: "/api/rules", cookie: mine}); rec.Code != 200 {
		t.Fatalf("the session that changed the password stays: %d", rec.Code)
	}

	// -reset-password opens it again.
	if err := resetPassword(s.store); err != nil {
		t.Fatal(err)
	}
	s.auth = newAuth(s.store)
	h = testHandler(s)
	if rec := do(h, call{method: "GET", path: "/api/rules"}); rec.Code != 200 || s.auth.passwordSet() {
		t.Fatalf("after reset: %d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	fastHash(t)
	s := testState(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	s.auth.now = func() time.Time { return now }
	h := testHandler(s)
	do(h, call{method: "POST", path: "/api/settings/password", body: `{"password":"long enough"}`})
	for i := 0; i < 5; i++ {
		if rec := do(h, call{method: "POST", path: "/api/login", body: `{"password":"nope nope"}`, ip: "192.168.1.50"}); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
		now = now.Add(5 * time.Second)
	}
	// Sixth within the minute: refused even with the right password.
	if rec := do(h, call{method: "POST", path: "/api/login", body: `{"password":"long enough"}`, ip: "192.168.1.50"}); rec.Code != 429 {
		t.Fatalf("6th attempt: %d", rec.Code)
	}
	// Another address isn't affected.
	if rec := do(h, call{method: "POST", path: "/api/login", body: `{"password":"long enough"}`, ip: "192.168.1.51"}); rec.Code != 200 {
		t.Fatalf("other address: %d", rec.Code)
	}
	// A minute after the first failures, it works again.
	now = now.Add(40 * time.Second)
	if rec := do(h, call{method: "POST", path: "/api/login", body: `{"password":"long enough"}`, ip: "192.168.1.50"}); rec.Code != 200 {
		t.Fatalf("after the wait: %d %s", rec.Code, rec.Body)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := testState(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	s.auth.now = func() time.Time { return now }
	s.store.setSetting("session_days", "1")
	s.auth.reload()
	rec := httptest.NewRecorder()
	if err := s.auth.startSession(rec, httptest.NewRequest("POST", "/", nil)); err != nil {
		t.Fatal(err)
	}
	c := sessionFrom(t, rec)
	req := httptest.NewRequest("GET", "/api/rules", nil)
	req.AddCookie(c)
	if s.auth.sessionOf(req) == "" {
		t.Fatal("fresh session not valid")
	}
	now = now.Add(25 * time.Hour)
	if s.auth.sessionOf(req) != "" {
		t.Fatal("expired session still valid")
	}
	// 0 days = until signed out (10 years).
	s.store.setSetting("session_days", "0")
	s.auth.reload()
	rec = httptest.NewRecorder()
	s.auth.startSession(rec, httptest.NewRequest("POST", "/", nil))
	if c := sessionFrom(t, rec); c.MaxAge < 3650*86400 {
		t.Fatalf("forever session MaxAge=%d", c.MaxAge)
	}
}

func TestSettingsPut(t *testing.T) {
	s := testState(t)
	restarted := make(chan string, 1)
	s.restart = func(why string) { restarted <- why }
	h := testHandler(s)

	rec := do(h, call{method: "GET", path: "/api/settings"})
	var st Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v", rec.Code, err)
	}
	if st.Brand.Name != "Junction" || st.Brand.Letter != "J" || st.Brand.Image != nil || st.Network.GatewayIP != "192.168.1.4" ||
		st.Access.SessionDays != 30 || st.Network.Router != nil || st.Network.DHCP != nil {
		t.Fatalf("defaults: %+v", st)
	}
	// null vs missing, as the app expects
	for _, k := range []string{`"image":null`, `"router":null`, `"backup":null`, `"dhcp":null`, `"detectedAt":null`} {
		if !strings.Contains(rec.Body.String(), k) {
			t.Fatalf("missing %s in %s", k, rec.Body)
		}
	}

	for body, want := range map[string]int{
		`{"timezone":"Mars/Olympus"}`:                                      400,
		`{"brand":{"color":"blue"}}`:                                       400,
		`{"brand":{"name":""}}`:                                            400,
		`{"network":{"lan":"10.0.0.0/8"}}`:                                 400,
		`{"network":{"gatewayIP":"10.0.0.1"}}`:                             400, // outside 192.168.1.0/24
		`{"brand":{"image":"data:image/gif;base64,R0lGODlhAQABAAAAACw="}}`: 400,
		`not json`: 400,
	} {
		if rec := do(h, call{method: "PUT", path: "/api/settings", body: body}); rec.Code != want {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	select {
	case why := <-restarted:
		t.Fatalf("a refused change restarted Junction: %s", why)
	default:
	}

	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nrest"))
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"brand":{"name":"Casa","color":"#ABCDEF","image":"` + png + `"}}`})
	json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Brand.Name != "Casa" || st.Brand.Color != "#abcdef" || st.Brand.Letter != "J" || st.Brand.Image == nil {
		t.Fatalf("brand: %d %+v", rec.Code, st)
	}
	if ov := s.brand; ov.Name != "Casa" {
		t.Fatalf("overview brand not updated: %+v", ov)
	}
	if b := s.loadBrand(); b.Name != "Casa" || b.Image == nil {
		t.Fatalf("brand not saved: %+v", b)
	}
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"brand":{"image":null}}`})
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Brand.Image != nil || st.Brand.Name != "Casa" {
		t.Fatalf("image null should remove only the image: %+v", st.Brand)
	}

	// The same timezone as now doesn't restart; a new one does, after the response.
	do(h, call{method: "PUT", path: "/api/settings", body: `{"timezone":"Africa/Cairo"}`})
	rec = do(h, call{method: "PUT", path: "/api/settings", body: `{"timezone":"Europe/Berlin"}`})
	json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Timezone != "Europe/Berlin" {
		t.Fatalf("timezone: %d %+v", rec.Code, st.Timezone)
	}
	select {
	case why := <-restarted:
		if !strings.Contains(why, "timezone") {
			t.Fatalf("restart reason: %s", why)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no restart after a timezone change")
	}
	c := s.base
	applySavedSettings(&c, s.store)
	if c.TimeZone != "Europe/Berlin" {
		t.Fatalf("saved timezone not applied at start: %s", c.TimeZone)
	}
}

func TestCheckImage(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString
	if _, msg := checkImage("data:image/svg+xml;base64," + b64([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := checkImage("data:image/png;base64," + b64([]byte("<svg/>"))); msg == "" {
		t.Fatal("content that isn't PNG accepted as PNG")
	}
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxBrandImage)...)
	if _, msg := checkImage("data:image/png;base64," + b64(big)); !strings.Contains(msg, "300 KB") {
		t.Fatalf("too big: %q", msg)
	}
	if _, msg := checkImage("https://example.com/logo.png"); msg == "" {
		t.Fatal("a URL isn't a data: URL")
	}
}

func TestNetworkFiles(t *testing.T) {
	s := testState(t)
	dir := t.TempDir()
	keepalivedConf = filepath.Join(dir, "keepalived.conf")
	os.WriteFile(keepalivedConf, []byte(`vrrp_instance JUNCTION {
    state BACKUP
    unicast_src_ip 192.168.1.7
    unicast_peer {
        192.168.1.3
    }
    virtual_ipaddress {
        192.168.1.4/24 dev eth0
    }
}`), 0o644)
	if v := keepalivedValue("virtual_ipaddress"); v != "192.168.1.4" {
		t.Fatalf("vip: %q", v)
	}
	if gw := detectGatewayIP("no-such-if"); gw != "192.168.1.4" {
		t.Fatalf("gateway from keepalived: %q", gw)
	}
	if b := s.backupInfo(); b == nil || b.IP != "192.168.1.3" || b.Name != "Backup" {
		t.Fatalf("backup: %+v", b)
	}

	dnsmasqConf = filepath.Join(dir, "dnsmasq.conf")
	reservationsConf = filepath.Join(dir, "reservations.conf")
	os.WriteFile(dnsmasqConf, []byte("interface=eth0\ndhcp-range=set:pool,192.168.1.100,192.168.1.179,255.255.255.0,12h\n"), 0o644)
	os.WriteFile(reservationsConf, []byte("# fixed\ndhcp-host=aa:bb:cc:dd:ee:ff,192.168.1.50,tv\ndhcp-host=aa:bb:cc:dd:ee:01,192.168.1.51\n"), 0o644)
	os.WriteFile(s.cfg.DnsmasqLeases, []byte(""), 0o644)
	off := false
	s.cfg.DHCPListen = &off
	d := s.dhcpInfo()
	if d == nil || !d.On || d.Pool != ".100 – .179" || d.LeaseHours != 12 || d.Reservations != 2 {
		t.Fatalf("dhcp: %+v", d)
	}
	for in, want := range map[string]float64{"30m": 0.5, "1d": 24, "infinite": 0, "3600": 1} {
		if h, ok := leaseHours(in); !ok || h != want {
			t.Fatalf("lease %s: %v", in, h)
		}
	}
	if p, _ := parseDHCPRange("10.0.0.10,10.0.1.20,24h"); p != "10.0.0.10 – 10.0.1.20" {
		t.Fatalf("pool across subnets: %q", p)
	}

	procRoute = filepath.Join(dir, "route")
	os.WriteFile(procRoute, []byte("Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n"+
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\n"+
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\n"), 0o644)
	if ifc, gw := defaultRoute(); ifc != "eth0" || gw != "192.168.1.1" {
		t.Fatalf("default route: %s %s", ifc, gw)
	}
}

func TestProfilesFromSelectors(t *testing.T) {
	t.Cleanup(func() { setProfiles(nil, nil) })
	setProfiles(nil, nil)
	if !validProfile("psn") || perProfileSelectors() {
		t.Fatal("fallback profiles")
	}
	setProfiles(map[string]string{"vpn-active": "x", "vpn-work": "", "vpn-home": "", "vpn-zzz": "", "dns": ""}, []string{"direct", "vpn-home", "vpn-work"})
	if got := strings.Join(currentProfiles(), ","); got != "home,work,zzz" || !perProfileSelectors() || validProfile("psn") || validProfile("active") {
		t.Fatalf("profiles: %s", got)
	}
	if profileChoices() != "home, work or zzz" {
		t.Fatal(profileChoices())
	}
	s := testState(t)
	if s.globalProfile() != "home" {
		t.Fatalf("default profile falls back to the first: %s", s.globalProfile())
	}
	s.store.setSetting("default_profile", "work")
	if s.globalProfile() != "work" {
		t.Fatalf("saved default profile: %s", s.globalProfile())
	}
}

func TestExitNames(t *testing.T) {
	s := testState(t)
	os.WriteFile(s.cfg.FailoverConfig, []byte(`{"endpoints":[{"key":"azure","name":"Azure"},{"key":"zai"},{"key":"oz"}]}`), 0o644)
	s.exitList = readExitList(s.cfg)
	if s.exitName("azure") != "Azure" || s.exitName("zai") != "Zai" || s.exitName("other") != "Other" {
		t.Fatalf("names: %s %s", s.exitName("zai"), s.exitName("other"))
	}
}

func TestSetupMarking(t *testing.T) {
	s := testState(t)
	s.markExistingSetup()
	if !s.setupNeeded() {
		t.Fatal("an empty install needs setup")
	}
	// Devices showing up before setup is finished don't hide it.
	s.store.upsertDevice(devRow{ID: "aa:bb", IP: "192.168.1.9", FirstSeen: time.Now(), LastSeen: time.Now()})
	s.markExistingSetup()
	if !s.setupNeeded() {
		t.Fatal("setup hidden by a new device")
	}
	// An install in use is never sent to setup.
	s2 := testState(t)
	s2.store.upsertDevice(devRow{ID: "aa:bb", IP: "192.168.1.9", FirstSeen: time.Now(), LastSeen: time.Now()})
	s2.markExistingSetup()
	if s2.setupNeeded() {
		t.Fatal("existing install sent to setup")
	}
	if rec := do(testHandler(s2), call{method: "POST", path: "/api/setup", body: `{}`}); rec.Code != 409 {
		t.Fatalf("setup on a set-up install: %d", rec.Code)
	}
	h := testHandler(s)
	rec := do(h, call{method: "POST", path: "/api/setup", body: `{"timezone":"Africa/Cairo","exits":[{"name":"Home VPN","outbound":{"type":"vless","server":"1.2.3.4"}}],"devicesOnVPN":[],"dhcp":true,"brand":{"name":"Casa"}}`})
	if rec.Code != 200 || s.setupNeeded() {
		t.Fatalf("finish setup: %d %s", rec.Code, rec.Body)
	}
	b, err := os.ReadFile(exitsPendingPath)
	if err != nil || !strings.Contains(string(b), `"vless"`) {
		t.Fatalf("exits-pending.json: %v %s", err, b)
	}
	if s.brand.Name != "Casa" {
		t.Fatal("setup answers not saved")
	}
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.6.0", "v1.5.0", true}, {"v1.5.0", "v1.5.0", false}, {"v1.10.0", "v1.9.9", true}, {"v1.6.0", "dev", false},
		{"v1.5.1", "v1.5.0-3-gabc", true}, {"1.4", "v1.5.0", false}} {
		if newerVersion(c.a, c.b) != c.want {
			t.Fatalf("newer(%s, %s)", c.a, c.b)
		}
	}
	s := testState(t)
	updatesLog = filepath.Join(t.TempDir(), "updates.log")
	os.WriteFile(updatesLog, []byte("2026-11-10T10:00:00Z v1.4.0 healthy\n2026-11-12T10:00:00Z v1.4.1 rolled-back rolled back after 41 s\nbad line\n"), 0o644)
	v := s.versionInfo()
	if len(v.History) != 2 || v.History[0].Version != "v1.4.1" || v.History[0].Note == nil || *v.History[0].Note != "rolled back after 41 s" || v.History[1].Note != nil {
		t.Fatalf("history: %+v", v.History)
	}
	if v.Available != nil {
		t.Fatal("no update_url: nothing available")
	}
	rec := do(testHandler(s), call{method: "POST", path: "/api/update", body: `{"version":"v1.6.0"}`})
	if rec.Code != 501 {
		t.Fatalf("update without update_url: %d", rec.Code)
	}
}

func TestUnpackRelease(t *testing.T) {
	mk := func(files map[string]string) string {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
			tw.Write([]byte(body))
		}
		tw.Close()
		gz.Close()
		p := filepath.Join(t.TempDir(), "r.tar.gz")
		os.WriteFile(p, buf.Bytes(), 0o600)
		return p
	}
	dest := t.TempDir()
	top := "junction-v1.6.0-linux-amd64"
	if err := unpack(mk(map[string]string{top + "/junctiond": "bin", top + "/gateway/setup.sh": "sh"}), dest, top); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "junctiond")); string(b) != "bin" {
		t.Fatal("top folder not dropped")
	}
	if err := unpack(mk(map[string]string{top + "/../../evil": "x"}), t.TempDir(), top); err == nil {
		t.Fatal("a path outside the folder was accepted")
	}
	sums := "abc123  junction-v1.6.0-linux-amd64.tar.gz\nfff  *install.sh\n"
	if sumFor(sums, top+".tar.gz") != "abc123" || sumFor(sums, "install.sh") != "fff" || sumFor(sums, "x") != "" {
		t.Fatal("SHA256SUMS lookup")
	}
}

func TestHealthCached(t *testing.T) {
	s := testState(t)
	s.cfg.ClashAPI = "http://127.0.0.1:1" // nothing answers
	oldM, oldR, oldS := iptablesSaveMangle, ipRuleShow, iptablesSave
	t.Cleanup(func() { iptablesSaveMangle, ipRuleShow, iptablesSave = oldM, oldR, oldS })
	iptablesSaveMangle = func() ([]byte, error) {
		return []byte("*mangle\n:PREROUTING ACCEPT [0:0]\n:SING_BOX_TPROXY - [0:0]\n-A PREROUTING -i eth0 -j SING_BOX_TPROXY\n-A SING_BOX_TPROXY -p tcp -j TPROXY --on-port 7893 --on-ip 127.0.0.1 --tproxy-mark 0x1/0xffffffff\nCOMMIT\n"), nil
	}
	ipRuleShow = func() ([]byte, error) {
		return []byte("0:\tfrom all lookup local\n32765:\tfrom all fwmark 0x1 lookup 100\n"), nil
	}
	iptablesSave = func() ([]byte, error) { return nil, os.ErrNotExist }
	h := s.healthNow(t.Context())
	want := map[string]string{"routing": "bad", "rules": "ok", "counting": "warn", "exits": "off", "backup": "off", "dhcp": "off", "clock": "ok", "storage": "ok"}
	if len(h.Checks) != len(want) {
		t.Fatalf("checks: %+v", h.Checks)
	}
	for _, c := range h.Checks {
		if want[c.Key] != c.Status || c.Name == "" || c.Detail == "" {
			t.Fatalf("%s: %s %q", c.Key, c.Status, c.Detail)
		}
	}
	at := h.CheckedAt
	if h2 := s.healthNow(t.Context()); h2.CheckedAt != at {
		t.Fatal("checks should be cached for 30 s")
	}
}
