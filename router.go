package main

// Reads device names from the ISP router (Huawei HG8145V5, TE Data firmware):
// log in, fetch the LAN device list, log out. It runs every few minutes and
// only reads. Routers like this allow one admin session at a time, so the
// session is kept to a second or two.

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reQuoted = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	reHexEsc = regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
)

type routerDevice struct {
	IP, MAC, Port, Status, HostName, Alias string
}

func (s *State) pollRouter(ctx context.Context) {
	devs, err := fetchRouterDevices(ctx, s.cfg.RouterURL, s.cfg.RouterUser, s.cfg.RouterPassword)
	s.mu.Lock()
	s.routerErr, s.routerAt = err, time.Now()
	if err == nil {
		s.routerDevs = len(devs)
	}
	s.mu.Unlock()
	if err != nil {
		s.logf("router: %v", err)
		return
	}
	for _, d := range devs {
		raw := d.Alias
		if raw == "" || raw == "--" {
			raw = d.HostName
		}
		if raw == "--" {
			raw = ""
		}
		name, model := prettyName(raw)
		if name == "" && model == "" {
			continue
		}
		s.applyHint(nameHint{MAC: strings.ToLower(d.MAC), Name: name, Source: "router", Model: model})
	}
}

func fetchRouterDevices(ctx context.Context, base, user, pass string) ([]routerDevice, error) {
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		// The router only has a self-signed certificate and is only reached on the LAN.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "Cookie", Value: "body:Language:english:id=-1", Path: "/"}})

	post := func(path string, form url.Values) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := cl.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return string(b), nil
	}
	get := func(path string) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		resp, err := cl.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return string(b), nil
	}
	token := func(s string) string { return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff")) }

	tok, err := post("/asp/GetRandCount.asp", url.Values{})
	if err != nil {
		return nil, fmt.Errorf("can't reach the router: %w", err)
	}
	if _, err := post("/login.cgi", url.Values{
		"UserName": {user}, "PassWord": {base64.StdEncoding.EncodeToString([]byte(pass))},
		"Language": {"english"}, "x.X_HW_Token": {token(tok)},
	}); err != nil {
		return nil, fmt.Errorf("router login: %w", err)
	}
	page, err := get("/html/bbsp/common/GetLanUserDevInfo.asp")
	// Always log out so the router's single admin session is free again.
	if lt, e := get("/html/ssmp/common/GetRandToken.asp"); e == nil {
		post("/logout.cgi?RequestFile=html/logout.html", url.Values{"x.X_HW_Token": {token(lt)}})
	}
	if err != nil {
		return nil, fmt.Errorf("router device list: %w", err)
	}
	if !strings.Contains(page, "USERDevice") {
		return nil, fmt.Errorf("router login was refused (wrong password, or someone else is logged in)")
	}
	return parseRouterDevices(page), nil
}

func parseRouterDevices(page string) []routerDevice {
	seen := map[string]bool{}
	var out []routerDevice
	for _, chunk := range strings.Split(page, "new USERDevice(")[1:] {
		args, _, _ := strings.Cut(chunk, ")") // arguments are quoted, hex-escaped strings: no ")" inside
		var f []string
		for _, q := range reQuoted.FindAllStringSubmatch(args, -1) {
			f = append(f, reHexEsc.ReplaceAllStringFunc(q[1], func(h string) string {
				n, _ := strconv.ParseUint(h[2:], 16, 8)
				return string(rune(n))
			}))
		}
		// Domain, IpAddr, MacAddr, Port, IpType, DevType, DevStatus, PortType, Time, HostName, v4, v6, DeviceType, UserDevAlias, ...
		if len(f) < 14 || seen[f[2]] {
			continue
		}
		seen[f[2]] = true
		out = append(out, routerDevice{IP: f[1], MAC: f[2], Port: f[3], Status: f[6], HostName: f[9], Alias: f[13]})
	}
	return out
}
