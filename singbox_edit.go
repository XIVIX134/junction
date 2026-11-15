package main

// Changing sing-box's config from Settings: VPN exits and profiles.
//
// The layout is the one setup.sh writes and the failover expects:
//   - an exit "<key>" has one outbound (or WireGuard endpoint) per profile,
//     tagged "<key>-<profile>". A profile with its own site name (TLS SNI) gets
//     a copy of the exit's outbound with that name; otherwise "<key>-<profile>"
//     is a selector holding the one real outbound (so a WireGuard key is never
//     used twice at once).
//   - selectors "vpn-<profile>" (devices in profile-<profile>.json) and
//     "vpn-active" (devices in vpn-devices.json) list every exit plus direct.
//   - the failover checks each exit x profile through a local inbound
//     "probe-<key>-<profile>" (127.0.0.1:221xx) routed to "<key>-<profile>".
//
// Every change is checked with `sing-box check`, put in place with the old
// config kept as .junction-prev, and sing-box reloaded. If sing-box doesn't come
// back healthy (its API answers with the new selectors and traffic flows), the
// old config goes back by itself.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sbConf struct{ m map[string]any }

func loadSingbox(path string) (*sbConf, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSingbox(b)
}

func parseSingbox(b []byte) (*sbConf, error) {
	m := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return &sbConf{m: m}, nil
}

func (c *sbConf) bytes() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(c.m)
	return buf.Bytes()
}

func objs(v any) []map[string]any {
	l, _ := v.([]any)
	var out []map[string]any
	for _, x := range l {
		if o, ok := x.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

func toAny(l []map[string]any) []any {
	out := make([]any, len(l))
	for i, o := range l {
		out[i] = o
	}
	return out
}

func strs(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func anyStrs(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

func tagOf(o map[string]any) string  { s, _ := o["tag"].(string); return s }
func typeOf(o map[string]any) string { s, _ := o["type"].(string); return s }

func isGroup(o map[string]any) bool { t := typeOf(o); return t == "selector" || t == "urltest" }

func (c *sbConf) list(sec string) []map[string]any { return objs(c.m[sec]) }
func (c *sbConf) setList(sec string, l []map[string]any) {
	if len(l) == 0 && sec == "endpoints" {
		delete(c.m, sec)
		return
	}
	c.m[sec] = toAny(l)
}

func (c *sbConf) route() map[string]any {
	r, ok := c.m["route"].(map[string]any)
	if !ok {
		r = map[string]any{}
		c.m["route"] = r
	}
	return r
}

func (c *sbConf) rules() []map[string]any     { return objs(c.route()["rules"]) }
func (c *sbConf) setRules(l []map[string]any) { c.route()["rules"] = toAny(l) }

// find returns the outbound or endpoint with this tag and its section.
func (c *sbConf) find(tag string) (string, map[string]any) {
	for _, sec := range []string{"outbounds", "endpoints"} {
		for _, o := range c.list(sec) {
			if tagOf(o) == tag {
				return sec, o
			}
		}
	}
	return "", nil
}

func (c *sbConf) hasTag(tag string) bool {
	for _, sec := range []string{"outbounds", "endpoints", "inbounds"} {
		for _, o := range c.list(sec) {
			if tagOf(o) == tag {
				return true
			}
		}
	}
	return false
}

// removeTag drops an outbound, endpoint or inbound, and every reference to it.
func (c *sbConf) removeTag(tag string) {
	for _, sec := range []string{"outbounds", "endpoints", "inbounds"} {
		l := c.list(sec)
		var keep []map[string]any
		for _, o := range l {
			if tagOf(o) != tag {
				keep = append(keep, o)
			}
		}
		if len(keep) != len(l) {
			c.setList(sec, keep)
		}
	}
	for _, g := range c.list("outbounds") {
		if !isGroup(g) {
			continue
		}
		var members []string
		for _, t := range strs(g["outbounds"]) {
			if t != tag {
				members = append(members, t)
			}
		}
		if len(members) == 0 {
			members = []string{"direct"}
		}
		g["outbounds"] = anyStrs(members)
		if d, _ := g["default"].(string); d == tag {
			g["default"] = members[0]
		}
	}
	var rules []map[string]any
	for _, r := range c.rules() {
		if r["outbound"] == tag {
			continue
		}
		if in := strs(r["inbound"]); len(in) == 1 && in[0] == tag {
			continue
		}
		rules = append(rules, r)
	}
	c.setRules(rules)
	if c.route()["final"] == tag {
		c.route()["final"] = "direct"
	}
}

// insertAfter puts o into a section right after the last item matching after (else first).
func (c *sbConf) insertAfter(sec string, o map[string]any, after func(map[string]any) bool) {
	l := c.list(sec)
	at := -1
	for i, x := range l {
		if after(x) {
			at = i
		}
	}
	l = append(l[:at+1], append([]map[string]any{o}, l[at+1:]...)...)
	c.setList(sec, l)
}

// groupAdd adds a member to a selector, before "direct".
func (c *sbConf) groupAdd(group, member string) {
	_, g := c.find(group)
	if g == nil {
		return
	}
	ms := strs(g["outbounds"])
	for _, m := range ms {
		if m == member {
			return
		}
	}
	at := len(ms)
	for i, m := range ms {
		if m == "direct" {
			at = i
			break
		}
	}
	ms = append(ms[:at], append([]string{member}, ms[at:]...)...)
	g["outbounds"] = anyStrs(ms)
	if d, _ := g["default"].(string); d == "" || d == "direct" {
		g["default"] = ms[0]
	}
}

func (c *sbConf) usedPorts() map[int]bool {
	used := map[int]bool{}
	for _, in := range c.list("inbounds") {
		if n, err := strconv.Atoi(fmt.Sprint(in["listen_port"])); err == nil {
			used[n] = true
		}
	}
	return used
}

// probePort: the next free local port for a probe inbound, from 22101.
func (c *sbConf) probePort(taken map[int]bool) int {
	used := c.usedPorts()
	for p := 22101; p < 23000; p++ {
		if used[p] || taken[p] {
			continue
		}
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			l.Close()
			taken[p] = true
			return p
		}
	}
	return 0
}

func (c *sbConf) addProbe(exitTag string, port int) {
	tag := "probe-" + exitTag
	c.removeTag(tag)
	c.insertAfter("inbounds", map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": port}, func(map[string]any) bool { return true })
	// Right after the other probe rules; else before the first rule that routes.
	rules := c.rules()
	at := -1
	for i, r := range rules {
		if in := strs(r["inbound"]); len(in) > 0 && strings.HasPrefix(in[0], "probe-") {
			at = i
		}
	}
	if at < 0 {
		for i, r := range rules {
			if _, routes := r["outbound"]; routes {
				break
			}
			at = i
		}
	}
	rule := map[string]any{"inbound": []any{tag}, "outbound": exitTag}
	rules = append(rules[:at+1], append([]map[string]any{rule}, rules[at+1:]...)...)
	c.setRules(rules)
}

// exitTags: the outbounds/endpoints of an exit, "<key>-<profile>".
func (c *sbConf) exitTags(key string) []string {
	var out []string
	for _, sec := range []string{"outbounds", "endpoints"} {
		for _, o := range c.list(sec) {
			if t := tagOf(o); strings.HasPrefix(t, key+"-") && endpointKey(t) == key {
				out = append(out, t)
			}
		}
	}
	return out
}

// baseOf follows a one-member selector to the real outbound.
func (c *sbConf) baseOf(tag string) (string, string, map[string]any) {
	for i := 0; i < 3; i++ {
		sec, o := c.find(tag)
		if o == nil {
			return "", "", nil
		}
		if !isGroup(o) {
			return tag, sec, o
		}
		ms := strs(o["outbounds"])
		if len(ms) == 0 {
			return "", "", nil
		}
		tag = ms[0]
	}
	return "", "", nil
}

func (c *sbConf) exitBase(key string, profs []string) (string, string, map[string]any) {
	for _, p := range profs {
		if t, sec, o := c.baseOf(key + "-" + p); o != nil {
			return t, sec, o
		}
	}
	for _, t := range c.exitTags(key) {
		if t, sec, o := c.baseOf(t); o != nil {
			return t, sec, o
		}
	}
	return "", "", nil
}

func kindOf(t string) string {
	switch t {
	case "wireguard", "hysteria2", "vless", "trojan", "shadowsocks":
		return t
	}
	return "other"
}

func (c *sbConf) exitDescription(key string, profs []string) (kind, server string) {
	_, _, o := c.exitBase(key, profs)
	if o == nil {
		return "other", ""
	}
	return kindOf(typeOf(o)), serverOf(o)
}

func serverOf(o map[string]any) string {
	if typeOf(o) == "wireguard" {
		if ps := objs(o["peers"]); len(ps) > 0 {
			return net.JoinHostPort(fmt.Sprint(ps[0]["address"]), fmt.Sprint(ps[0]["port"]))
		}
		return ""
	}
	if h, ok := o["server"].(string); ok && h != "" {
		return net.JoinHostPort(h, fmt.Sprint(o["server_port"]))
	}
	return ""
}

func serverHost(o map[string]any) string {
	h, _, err := net.SplitHostPort(serverOf(o))
	if err != nil {
		return ""
	}
	return h
}

func clone(o map[string]any) map[string]any {
	b, _ := json.Marshal(o)
	out := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.Decode(&out)
	return out
}

func hasTLS(o map[string]any) bool {
	t, ok := o["tls"].(map[string]any)
	return ok && t["enabled"] == true
}

// addExitProfile adds "<key>-<prof>" for an exit whose real outbound is base:
// a copy with the profile's site name, or a selector holding base.
func (c *sbConf) addExitProfile(key, prof, site, baseTag, baseSec string, base map[string]any) {
	tag := key + "-" + prof
	if site != "" && hasTLS(base) {
		o := clone(base)
		o["tag"] = tag
		o["tls"].(map[string]any)["server_name"] = site
		c.insertAfter(baseSec, o, func(x map[string]any) bool { return endpointKey(tagOf(x)) == key })
		return
	}
	c.insertAfter("outbounds", map[string]any{"type": "selector", "tag": tag, "outbounds": []any{baseTag}},
		func(x map[string]any) bool { return endpointKey(tagOf(x)) == key })
}

// ensureProfile adds the selector, rule-set and rule of a profile if they're missing.
func (c *sbConf) ensureProfile(prof string) {
	sel := "vpn-" + prof
	if _, o := c.find(sel); o == nil {
		c.insertAfter("outbounds", map[string]any{"type": "selector", "tag": sel, "outbounds": []any{"direct"}, "default": "direct", "interrupt_exist_connections": true},
			func(x map[string]any) bool { return strings.HasPrefix(tagOf(x), "vpn-") })
	}
	rs := "profile-" + prof
	r := c.route()
	sets := objs(r["rule_set"])
	found := false
	for _, s := range sets {
		found = found || tagOf(s) == rs
	}
	if !found {
		sets = append(sets, map[string]any{"type": "local", "tag": rs, "format": "source", "path": profileListPath(prof)})
		r["rule_set"] = toAny(sets)
	}
	rules := c.rules()
	for _, x := range rules {
		if x["outbound"] == sel {
			return
		}
	}
	// Before the global "vpn-devices -> vpn-active" rule, else at the end.
	at := len(rules)
	for i, x := range rules {
		if x["outbound"] == "vpn-active" {
			at = i
			break
		}
	}
	rule := map[string]any{"rule_set": []any{rs}, "outbound": sel}
	rules = append(rules[:at], append([]map[string]any{rule}, rules[at:]...)...)
	c.setRules(rules)
}

// ruleSetDir holds the device lists sing-box reads (a var for tests).
var ruleSetDir = "/etc/sing-box"

func profileListPath(prof string) string {
	return filepath.Join(ruleSetDir, "profile-"+prof+".json")
}

func (c *sbConf) removeProfile(prof string, exits []string, profs []string) {
	for _, key := range exits {
		tag := key + "-" + prof
		_, o := c.find(tag)
		if o == nil {
			continue
		}
		// Other profiles' selectors may hold this outbound: move it to the first of them.
		if !isGroup(o) {
			var holders []string
			for _, p := range profs {
				if p == prof {
					continue
				}
				if _, g := c.find(key + "-" + p); g != nil && isGroup(g) {
					if ms := strs(g["outbounds"]); len(ms) == 1 && ms[0] == tag {
						holders = append(holders, key+"-"+p)
					}
				}
			}
			if len(holders) > 0 {
				sec, _ := c.find(tag)
				moved := clone(o)
				moved["tag"] = holders[0]
				c.removeTag(holders[0])
				c.insertAfter(sec, moved, func(x map[string]any) bool { return endpointKey(tagOf(x)) == key })
				for _, h := range holders[1:] {
					_, g := c.find(h)
					g["outbounds"] = []any{holders[0]}
				}
				// it was in the groups the holder was removed from
				c.groupAdd("vpn-"+strings.TrimPrefix(holders[0], key+"-"), holders[0])
				c.groupAdd("vpn-active", holders[0])
				if in := "probe-" + holders[0]; c.hasTag(in) {
					c.repointProbe(in, holders[0])
				}
			}
		}
		c.removeTag(tag)
		c.removeTag("probe-" + tag)
	}
	c.removeTag("vpn-" + prof)
	rs := "profile-" + prof
	r := c.route()
	var sets []map[string]any
	for _, s := range objs(r["rule_set"]) {
		if tagOf(s) != rs {
			sets = append(sets, s)
		}
	}
	r["rule_set"] = toAny(sets)
	var rules []map[string]any
	for _, x := range c.rules() {
		if l := strs(x["rule_set"]); len(l) == 1 && l[0] == rs {
			continue
		}
		rules = append(rules, x)
	}
	c.setRules(rules)
}

// repointProbe makes sure a probe inbound's rule routes to tag (removeTag dropped it with the holder).
func (c *sbConf) repointProbe(inbound, tag string) {
	for _, r := range c.rules() {
		if in := strs(r["inbound"]); len(in) == 1 && in[0] == inbound {
			r["outbound"] = tag
			return
		}
	}
	var port int
	for _, in := range c.list("inbounds") {
		if tagOf(in) == inbound {
			port, _ = strconv.Atoi(fmt.Sprint(in["listen_port"]))
		}
	}
	c.addProbe(tag, port)
}

// setProfileSite changes the site name (SNI) one profile's exit connections use.
func (c *sbConf) setProfileSite(prof, site string, exits, profs []string) {
	for _, key := range exits {
		tag := key + "-" + prof
		_, o := c.find(tag)
		if o == nil {
			continue
		}
		if !isGroup(o) {
			if hasTLS(o) && site != "" {
				o["tls"].(map[string]any)["server_name"] = site
			}
			continue
		}
		baseTag, baseSec, base := c.baseOf(tag)
		if base == nil || site == "" || !hasTLS(base) {
			continue
		}
		// Was a selector on the shared outbound: give it its own copy with the new name.
		n := clone(base)
		n["tag"] = tag
		n["tls"].(map[string]any)["server_name"] = site
		sec, _ := c.find(tag)
		l := c.list(sec)
		for i, x := range l {
			if tagOf(x) == tag {
				l = append(l[:i], l[i+1:]...)
				break
			}
		}
		c.setList(sec, l)
		_ = baseTag
		c.insertAfter(baseSec, n, func(x map[string]any) bool { return endpointKey(tagOf(x)) == key })
	}
}

// ---- pasted exits ----

var reExitKey = regexp.MustCompile(`[^a-z0-9]+`)

// exitKeyFor makes an exit's key from its name: lowercase letters and digits, unique.
func exitKeyFor(name string, taken func(string) bool) string {
	k := reExitKey.ReplaceAllString(strings.ToLower(name), "")
	if len(k) > 12 {
		k = k[:12]
	}
	if k == "" || k[0] >= '0' && k[0] <= '9' {
		k = "exit" + k
	}
	reserved := map[string]bool{"direct": true, "vpn": true, "probe": true, "block": true, "dns": true, "active": true}
	base := k
	for i := 2; reserved[k] || taken(k); i++ {
		k = base + strconv.Itoa(i)
	}
	return k
}

// parseExit turns a pasted exit into a sing-box outbound or endpoint (without a tag).
func parseExit(d ExitDraft) (obj map[string]any, endpoint bool, err error) {
	text := strings.TrimSpace(d.Text)
	if text == "" {
		return nil, false, bad("Paste the exit's settings first.")
	}
	if len(text) > 64<<10 {
		return nil, false, bad("That's too long for one exit.")
	}
	switch d.Kind {
	case "wireguard":
		return parseWireGuard(text)
	case "outbound":
	default:
		return nil, false, bad("The kind must be wireguard or outbound.")
	}
	m := map[string]any{}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, false, bad("That isn't JSON: %v. Paste one sing-box outbound, like {\"type\": \"hysteria2\", ...}.", err)
	}
	if _, whole := m["outbounds"]; whole { // a whole config: take its first real outbound
		var pick map[string]any
		for _, sec := range []string{"endpoints", "outbounds"} {
			for _, o := range objs(m[sec]) {
				switch typeOf(o) {
				case "direct", "block", "dns", "selector", "urltest", "":
					continue
				}
				if pick == nil {
					pick, endpoint = o, sec == "endpoints"
				}
			}
		}
		if pick == nil {
			return nil, false, bad("That config has no VPN outbound in it.")
		}
		m = pick
	}
	t := typeOf(m)
	switch t {
	case "":
		return nil, false, bad("The outbound needs a \"type\", like \"hysteria2\" or \"vless\".")
	case "direct", "block", "dns", "selector", "urltest":
		return nil, false, bad("A %s outbound isn't a VPN exit.", t)
	case "wireguard":
		if _, ok := m["peers"]; !ok {
			return nil, false, bad("That's the old WireGuard outbound format; paste the WireGuard .conf file instead.")
		}
		endpoint = true
	}
	delete(m, "tag")
	if serverOf(m) == "" {
		return nil, false, bad("The outbound needs a server and server_port.")
	}
	return m, endpoint, nil
}

// parseWireGuard reads a WireGuard .conf into a sing-box (1.11+) endpoint.
func parseWireGuard(text string) (map[string]any, bool, error) {
	sec := ""
	peers := 0
	var addr, allowed []string
	vals := map[string]string{}
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";") {
			continue
		}
		if strings.HasPrefix(l, "[") {
			sec = strings.ToLower(strings.Trim(l, "[] "))
			if sec == "peer" {
				peers++
			}
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if sec == "peer" && peers > 1 {
			continue
		}
		switch sec + "." + k {
		case "interface.address":
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); a != "" {
					addr = append(addr, a)
				}
			}
		case "peer.allowedips":
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); a != "" {
					allowed = append(allowed, a)
				}
			}
		default:
			vals[sec+"."+k] = v
		}
	}
	pk, pub, ep := vals["interface.privatekey"], vals["peer.publickey"], vals["peer.endpoint"]
	if pk == "" || pub == "" || ep == "" || len(addr) == 0 {
		return nil, false, bad("The WireGuard config needs PrivateKey and Address under [Interface], and PublicKey and Endpoint under [Peer].")
	}
	host, port, err := net.SplitHostPort(ep)
	pn, perr := strconv.Atoi(port)
	if err != nil || perr != nil || pn < 1 || pn > 65535 {
		return nil, false, bad("The peer's Endpoint must look like vpn.example.com:51820.")
	}
	if len(allowed) == 0 {
		allowed = []string{"0.0.0.0/0", "::/0"}
	}
	peer := map[string]any{"address": host, "port": pn, "public_key": pub, "allowed_ips": anyStrs(allowed)}
	if v := vals["peer.presharedkey"]; v != "" {
		peer["pre_shared_key"] = v
	}
	if v, err := strconv.Atoi(vals["peer.persistentkeepalive"]); err == nil && v > 0 {
		peer["persistent_keepalive_interval"] = v
	}
	o := map[string]any{"type": "wireguard", "address": anyStrs(addr), "private_key": pk, "peers": []any{peer}}
	if v, err := strconv.Atoi(vals["interface.mtu"]); err == nil && v > 0 {
		o["mtu"] = v
	}
	return o, true, nil
}

// ---- running sing-box ----

var (
	singboxMu    sync.Mutex // one change at a time
	singboxCheck            = func(bin, conf string) error {
		out, err := exec.Command(bin, "check", "-c", conf).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if i := strings.LastIndex(msg, "FATAL"); i >= 0 {
				msg = strings.TrimSpace(msg[i+5:])
			}
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("%s", msg)
		}
		return nil
	}
	// singboxReload makes the running sing-box read its config again.
	singboxReload = func(conf string) error {
		pids := findProcs(func(args []string) bool {
			if len(args) < 2 || filepath.Base(args[0]) != "sing-box" || args[1] != "run" {
				return false
			}
			for _, a := range args {
				if a == conf {
					return true
				}
			}
			return false
		})
		if len(pids) == 1 {
			return sighup(pids[0])
		}
		return serviceDo("sing-box", "restart")
	}
	singboxHealthy func(s *State, want *sbConf) error = (*State).singboxServes
)

// singboxBin: the binary the running sing-box was started from, else the one on PATH.
func singboxBin(conf string) string {
	var bin string
	findProcs(func(args []string) bool {
		if bin == "" && len(args) > 1 && filepath.Base(args[0]) == "sing-box" && args[1] == "run" {
			for _, a := range args {
				if a == conf && filepath.IsAbs(args[0]) {
					bin = args[0]
				}
			}
		}
		return false
	})
	if bin != "" {
		return bin
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	return "/usr/local/bin/sing-box"
}

// singboxServes: the API answers with the new selectors, and traffic flows through it.
func (s *State) singboxServes(want *sbConf) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var px struct {
		Proxies map[string]struct {
			All []string `json:"all"`
		} `json:"proxies"`
	}
	if err := getJSON(ctx, s.cfg.ClashAPI+"/proxies", s.secret, &px); err != nil {
		return err
	}
	for _, o := range want.list("outbounds") {
		if typeOf(o) != "selector" {
			continue
		}
		got, ok := px.Proxies[tagOf(o)]
		if !ok || strings.Join(got.All, ",") != strings.Join(strs(o["outbounds"]), ",") {
			return fmt.Errorf("sing-box isn't using the new config yet (%s)", tagOf(o))
		}
	}
	for _, in := range want.list("inbounds") {
		if typeOf(in) != "mixed" || strings.HasPrefix(tagOf(in), "probe-") {
			continue
		}
		port := fmt.Sprint(in["listen_port"])
		proxy, _ := url.Parse("http://127.0.0.1:" + port)
		cl := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
		resp, err := cl.Get("http://cp.cloudflare.com/generate_204")
		if err != nil {
			return fmt.Errorf("traffic through sing-box: %v", err)
		}
		resp.Body.Close()
		break
	}
	return nil
}

// reloadSettle: sing-box takes a moment to close the old config and start the new one.
var reloadSettle = 1500 * time.Millisecond

// healthWait: how long sing-box gets to come back healthy after a change.
var healthWait = 20 * time.Second

func waitHealthy(d time.Duration, check func() error) error {
	end := time.Now().Add(d)
	var err error
	time.Sleep(reloadSettle)
	for time.Now().Before(end) {
		if err = check(); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return err
}

// applySingbox changes sing-box's config with edit; see the top of the file.
func (s *State) applySingbox(edit func(c *sbConf) error) error {
	singboxMu.Lock()
	defer singboxMu.Unlock()
	path := s.cfg.SingBoxConfig
	old, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading sing-box's config: %v", err)
	}
	c, err := parseSingbox(old)
	if err != nil {
		return fmt.Errorf("sing-box's config isn't valid JSON: %v", err)
	}
	if err := edit(c); err != nil {
		return err
	}
	nb := c.bytes()
	if bytes.Equal(nb, old) {
		return nil
	}
	cand := path + ".junction-new"
	if err := os.WriteFile(cand, nb, 0o600); err != nil {
		return err
	}
	defer os.Remove(cand)
	bin := singboxBin(path)
	if err := singboxCheck(bin, cand); err != nil {
		return bad("sing-box doesn't accept the change: %v", err)
	}
	if err := writeFileAtomic(path+".junction-prev", old, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(path, nb, 0o600); err != nil {
		return err
	}
	log.Printf("sing-box: config changed (previous kept as %s.junction-prev); reloading", path)
	err = singboxReload(path)
	if err == nil {
		err = waitHealthy(healthWait, func() error { return singboxHealthy(s, c) })
	}
	if err == nil {
		log.Printf("sing-box: healthy with the new config")
		return nil
	}
	log.Printf("sing-box: not healthy with the new config (%v); putting the previous one back", err)
	writeFileAtomic(path, old, 0o600)
	prev, _ := parseSingbox(old)
	if rerr := singboxReload(path); rerr != nil || waitHealthy(20*time.Second, func() error { return singboxHealthy(s, prev) }) != nil {
		serviceDo("sing-box", "restart")
	}
	return fmt.Errorf("sing-box didn't come back healthy with the change (%v), so the previous config is back", err)
}

// ---- profiles (PUT /api/settings vpn.profiles) ----

var (
	reProfile = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)
	reHost    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*$`)
)

type profilePlan struct {
	want    []ProfileInfo
	removed []string
	singbox bool
	edit    func(c *sbConf) error
	ports   map[string]map[string]int // exit -> profile -> probe port (filled by edit)
}

func (pp *profilePlan) has(p string) bool {
	for _, w := range pp.want {
		if w.Name == p {
			return true
		}
	}
	return false
}

// siteName accepts "example.com" or a pasted address, and returns the host name.
func siteName(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil {
			return "", false
		}
		v = u.Hostname()
	}
	v = strings.ToLower(strings.TrimSuffix(v, "."))
	return v, reHost.MatchString(v) && len(v) <= 253
}

func (s *State) profileInUse(p string) string {
	n := 0
	s.mu.RLock()
	for _, d := range s.devs {
		if d.Profile == p {
			n++
		}
	}
	rules := s.rules
	s.mu.RUnlock()
	if n > 0 {
		return fmt.Sprintf("%d device(s) use the %s profile; move them to another first.", n, p)
	}
	for _, r := range rules {
		b, _ := json.Marshal(r)
		if bytes.Contains(b, []byte(`"profile":"`+p+`"`)) {
			return fmt.Sprintf("The rule %q uses the %s profile; change it first.", r.Name, p)
		}
	}
	return ""
}

func (s *State) planProfiles(want []ProfileInfo, newDefault *string) (*profilePlan, error) {
	if len(want) == 0 {
		return nil, bad("Keep at least one profile.")
	}
	fc, _ := s.failoverView()
	cur := currentProfiles()
	curSet := map[string]bool{}
	for _, p := range cur {
		curSet[p] = true
	}
	pp := &profilePlan{ports: map[string]map[string]int{}}
	seen := map[string]bool{}
	for _, w := range want {
		name := strings.TrimSpace(w.Name)
		if !reProfile.MatchString(name) || name == "active" {
			return nil, bad("A profile's name must be lowercase letters, digits and dashes (not %q).", w.Name)
		}
		if seen[name] {
			return nil, bad("There are two profiles called %s.", name)
		}
		seen[name] = true
		site, ok := siteName(w.CheckSite)
		if !ok {
			return nil, bad("The site name for %s must be a host name like example.com.", name)
		}
		pp.want = append(pp.want, ProfileInfo{Name: name, CheckSite: site})
	}
	def := s.globalProfile()
	if newDefault != nil {
		def = *newDefault
	}
	for _, p := range cur {
		if seen[p] {
			continue
		}
		if p == def {
			return nil, bad("%s is the default profile: make another one the default first.", p)
		}
		if msg := s.profileInUse(p); msg != "" {
			return nil, bad("%s", msg)
		}
		pp.removed = append(pp.removed, p)
	}
	var exits []string
	for _, e := range fc.Endpoints {
		exits = append(exits, e.Key)
	}
	var added []ProfileInfo
	var changed []ProfileInfo
	for _, w := range pp.want {
		switch {
		case !curSet[w.Name]:
			added = append(added, w)
		case fc.Profiles[w.Name] != w.CheckSite:
			changed = append(changed, w)
		}
	}
	pp.singbox = len(added) > 0 || len(pp.removed) > 0 || (len(changed) > 0 && len(exits) > 0)
	pp.edit = func(c *sbConf) error {
		for _, p := range pp.removed {
			c.removeProfile(p, exits, cur)
		}
		taken := map[int]bool{}
		for _, w := range added {
			c.ensureProfile(w.Name)
			for _, key := range exits {
				bt, bsec, base := c.exitBase(key, cur)
				if base == nil {
					return fmt.Errorf("sing-box's config has no outbound for the exit %s", key)
				}
				c.addExitProfile(key, w.Name, w.CheckSite, bt, bsec, base)
				c.groupAdd("vpn-"+w.Name, key+"-"+w.Name)
				c.groupAdd("vpn-active", key+"-"+w.Name)
				port := c.probePort(taken)
				if port == 0 {
					return fmt.Errorf("no free local port for the %s probe", key)
				}
				c.addProbe(key+"-"+w.Name, port)
				if pp.ports[key] == nil {
					pp.ports[key] = map[string]int{}
				}
				pp.ports[key][w.Name] = port
			}
			if err := writeRuleSet(profileListPath(w.Name), nil); err != nil {
				return err
			}
		}
		for _, w := range changed {
			c.setProfileSite(w.Name, w.CheckSite, exits, cur)
		}
		return nil
	}
	return pp, nil
}

func (pp *profilePlan) failoverEdit(fo map[string]any) error {
	profs := map[string]any{}
	for _, w := range pp.want {
		profs[w.Name] = w.CheckSite
	}
	fo["profiles"] = profs
	for _, e := range foEndpoints(fo) {
		ports, _ := e["probe_ports"].(map[string]any)
		if ports == nil {
			ports = map[string]any{}
			e["probe_ports"] = ports
		}
		for _, p := range pp.removed {
			delete(ports, p)
		}
		key, _ := e["key"].(string)
		for p, port := range pp.ports[key] {
			ports[p] = port
		}
	}
	return nil
}

// ---- the failover section of config.json ----

func foEndpoints(fo map[string]any) []map[string]any { return objs(fo["endpoints"]) }

// failoverSection returns config.json's failover section to change: its own,
// else the older separate file's settings (moved into config.json), else new.
func (s *State) failoverSection(m map[string]any) (map[string]any, error) {
	if fo, ok := m["failover"].(map[string]any); ok {
		return fo, nil
	}
	if b, err := os.ReadFile(s.base.FailoverConfig); err == nil {
		fo := map[string]any{}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&fo); err != nil {
			return nil, fmt.Errorf("%s isn't valid JSON: %v", s.base.FailoverConfig, err)
		}
		return fo, nil
	}
	d := foDefaults()
	profs := map[string]any{}
	for _, p := range currentProfiles() {
		profs[p] = ""
	}
	return map[string]any{"probe_url": d.ProbeURL, "default_profile": s.globalProfile(), "profiles": profs, "endpoints": []any{}}, nil
}

// ---- exits: POST/DELETE /api/settings/exits, POST /api/settings/exits/test ----

type ExitTest struct {
	OK        bool    `json:"ok"`
	Message   string  `json:"message"`
	LatencyMs *int    `json:"latencyMs"`
	Server    *string `json:"server"`
}

type ExitDraft struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // wireguard | outbound
	Text string `json:"text"`
}

func readDraft(w http.ResponseWriter, r *http.Request) (ExitDraft, map[string]any, bool, bool) {
	var d ExitDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&d); err != nil {
		fail(w, 400, "The request body must be JSON like {\"name\": \"Home VPN\", \"kind\": \"wireguard\", \"text\": \"...\"}.")
		return d, nil, false, false
	}
	obj, endpoint, err := parseExit(d)
	if err != nil {
		fail(w, 400, err.Error())
		return d, nil, false, false
	}
	return d, obj, endpoint, true
}

// testExit runs the exit in a separate, temporary sing-box and fetches the
// failover's check address through it.
func (s *State) testExit(ctx context.Context, obj map[string]any, endpoint bool) ExitTest {
	srv := serverOf(obj)
	res := ExitTest{Server: &srv}
	dir, err := os.MkdirTemp("", "junction-exit-test")
	if err != nil {
		res.Message = err.Error()
		return res
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Message = err.Error()
		return res
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	o := clone(obj)
	o["tag"] = "exit"
	conf := map[string]any{
		"log":      map[string]any{"level": "error"},
		"dns":      map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
		"inbounds": []any{map[string]any{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": port}},
		"route":    map[string]any{"final": "exit", "default_domain_resolver": "local"},
	}
	if endpoint {
		conf["endpoints"] = []any{o}
		conf["outbounds"] = []any{map[string]any{"type": "direct", "tag": "direct"}}
	} else {
		conf["outbounds"] = []any{o, map[string]any{"type": "direct", "tag": "direct"}}
	}
	path := filepath.Join(dir, "test.json")
	b, _ := json.Marshal(conf)
	os.WriteFile(path, b, 0o600)
	bin := singboxBin(s.cfg.SingBoxConfig)
	if err := singboxCheck(bin, path); err != nil {
		res.Message = "sing-box doesn't accept it: " + err.Error()
		return res
	}
	rctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(rctx, bin, "run", "-c", path)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		res.Message = "couldn't start sing-box: " + err.Error()
		return res
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	up := false
	for i := 0; i < 50 && !up; i++ {
		time.Sleep(100 * time.Millisecond)
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			c.Close()
			up = true
		}
	}
	if !up {
		res.Message = "sing-box didn't start with it: " + lastLine(errOut.String())
		return res
	}
	check, _ := s.failoverView()
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	cl := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	var last error
	for try := 0; try < 2; try++ { // the first request also sets up the tunnel
		start := time.Now()
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, check.ProbeURL, nil)
		resp, err := cl.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				ms := int(time.Since(start).Milliseconds())
				res.OK, res.LatencyMs, res.Message = true, &ms, "works"
				return res
			}
			err = fmt.Errorf("the check page answered HTTP %d", resp.StatusCode)
		}
		last = err
	}
	res.Message = "It didn't answer: " + shortErr(last)
	if e := lastLine(errOut.String()); e != "" {
		res.Message += " (sing-box: " + e + ")"
	}
	return res
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(ls[len(ls)-1])
	if i := strings.Index(l, "] "); i >= 0 {
		l = l[i+2:]
	}
	if len(l) > 200 {
		l = l[:200]
	}
	return l
}

func shortErr(err error) string {
	if err == nil {
		return "no answer"
	}
	m := err.Error()
	if i := strings.LastIndex(m, ": "); i >= 0 && len(m)-i < 80 {
		m = m[i+2:]
	}
	return m
}

func (a *API) testExitHandler(w http.ResponseWriter, r *http.Request) {
	_, obj, endpoint, ok := readDraft(w, r)
	if !ok {
		return
	}
	send(w, a.s.testExit(r.Context(), obj, endpoint))
}

func (a *API) addExitHandler(w http.ResponseWriter, r *http.Request) {
	s := a.s
	d, obj, endpoint, ok := readDraft(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(d.Name)
	if name == "" || len([]rune(name)) > 40 {
		fail(w, 400, "Give the exit a name (up to 40 characters).")
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	fc, _ := s.failoverView()
	for _, e := range fc.Endpoints {
		if strings.EqualFold(e.Name, name) {
			fail(w, 400, "There is already an exit called "+name+".")
			return
		}
	}
	profs := currentProfiles()
	ports := map[string]int{}
	var key string
	err := s.applySingbox(func(c *sbConf) error {
		key = exitKeyFor(name, func(k string) bool {
			for _, e := range fc.Endpoints {
				if e.Key == k {
					return true
				}
			}
			return len(c.exitTags(k)) > 0 || c.hasTag(k)
		})
		sec := "outbounds"
		if endpoint {
			sec = "endpoints"
		}
		taken := map[int]bool{}
		var baseTag string
		var base map[string]any
		for i, p := range profs {
			c.ensureProfile(p)
			tag := key + "-" + p
			if i == 0 {
				o := clone(obj)
				o["tag"] = tag
				if site := fc.Profiles[p]; site != "" && hasTLS(o) {
					o["tls"].(map[string]any)["server_name"] = site
				}
				c.insertAfter(sec, o, func(x map[string]any) bool {
					t := typeOf(x)
					return !isGroup(x) && t != "direct" && t != "block" && t != "dns"
				})
				baseTag, base = tag, o
			} else {
				c.addExitProfile(key, p, fc.Profiles[p], baseTag, sec, base)
			}
			c.groupAdd("vpn-"+p, tag)
			c.groupAdd("vpn-active", tag)
			port := c.probePort(taken)
			if port == 0 {
				return fmt.Errorf("no free local port for the probe")
			}
			c.addProbe(tag, port)
			ports[p] = port
		}
		for _, p := range profs {
			if !fileExists(profileListPath(p)) {
				writeRuleSet(profileListPath(p), nil)
			}
		}
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	err = editConfigFile(s.cfgPath, func(m map[string]any) error {
		fo, err := s.failoverSection(m)
		if err != nil {
			return err
		}
		pp := map[string]any{}
		for p, port := range ports {
			pp[p] = port
		}
		eps := foEndpoints(fo)
		eps = append(eps, map[string]any{"key": key, "name": name, "ip": serverHost(obj), "probe_ports": pp})
		fo["endpoints"] = toAny(eps)
		if profsMap, ok := fo["profiles"].(map[string]any); !ok || len(profsMap) == 0 {
			m2 := map[string]any{}
			for _, p := range profs {
				m2[p] = fc.Profiles[p]
			}
			fo["profiles"] = m2
		}
		m["failover"] = fo
		return nil
	})
	if err != nil {
		fail(w, 500, "sing-box has the exit, but saving it in the config failed: "+err.Error())
		return
	}
	log.Printf("settings: added the VPN exit %s (%s, %s) from %s", key, name, typeOf(obj), clientIP(r))
	s.sendSaved(w, []string{"VPN exits"})
}

func (a *API) removeExitHandler(w http.ResponseWriter, r *http.Request) {
	s := a.s
	key := r.PathValue("key")
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	fc, _ := s.failoverView()
	found := false
	for _, e := range fc.Endpoints {
		found = found || e.Key == key
	}
	if !found {
		fail(w, 404, "There is no VPN exit "+key+"; reload the page.")
		return
	}
	err := s.applySingbox(func(c *sbConf) error {
		tags := c.exitTags(key)
		sort.Strings(tags)
		for _, t := range tags {
			c.removeTag("probe-" + t)
		}
		// selectors first, so a shared outbound isn't put back into them
		for _, t := range tags {
			if _, o := c.find(t); o != nil && isGroup(o) {
				c.removeTag(t)
			}
		}
		for _, t := range c.exitTags(key) {
			c.removeTag(t)
		}
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	left := 0
	err = editConfigFile(s.cfgPath, func(m map[string]any) error {
		fo, err := s.failoverSection(m)
		if err != nil {
			return err
		}
		var keep []map[string]any
		for _, e := range foEndpoints(fo) {
			if e["key"] != key {
				keep = append(keep, e)
			}
		}
		left = len(keep)
		fo["endpoints"] = toAny(keep)
		m["failover"] = fo
		if left == 0 {
			m["failover_enabled"] = false // nothing left to switch between
		}
		return nil
	})
	if err != nil {
		fail(w, 500, "sing-box no longer has the exit, but saving the config failed: "+err.Error())
		return
	}
	log.Printf("settings: removed the VPN exit %s (%d left) from %s", key, left, clientIP(r))
	s.sendSaved(w, []string{"VPN exits"})
}

func failErr(w http.ResponseWriter, err error) {
	if pe, ok := err.(patchError); ok {
		fail(w, 400, string(pe))
		return
	}
	fail(w, 500, err.Error())
}
