package main

// DHCP settings: Junction's dnsmasq files (written by setup.sh --dhcp).
// Changing them: the new files are checked with `dnsmasq --test` first; then
// the old ones are kept as .prev, the new ones put in place and the DHCP server
// restarted. If it doesn't come back, the old files go back and it is started
// again. Leases are kept in the lease file, so devices keep their addresses.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	dnsmasqConf      = "/etc/junction/dnsmasq.conf"
	reservationsConf = "/etc/junction/reservations.conf"
	dhcpService      = "junction-dhcp"
	// dnsmasqTest checks a config file (a var for tests).
	dnsmasqTest = func(conf string) error {
		bin, err := exec.LookPath("dnsmasq")
		if err != nil {
			if bin, err = exec.LookPath("/usr/sbin/dnsmasq"); err != nil {
				return fmt.Errorf("dnsmasq isn't installed")
			}
		}
		out, err := exec.Command(bin, "--test", "-C", conf).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "dnsmasq:")))
		}
		return nil
	}
	dhcpRunning = func() bool { return dnsmasqRunning() }
	dhcpRestart = func() error { return serviceDo(dhcpService, "restart") }
	dhcpWait    = 10 * time.Second // for the DHCP server to come back after a change
)

// dnsmasqRunning: a dnsmasq process is using Junction's config.
func dnsmasqRunning() bool {
	return len(findProcs(func(args []string) bool {
		if len(args) == 0 || filepath.Base(args[0]) != "dnsmasq" {
			return false
		}
		for _, a := range args {
			if strings.Contains(a, dnsmasqConf) {
				return true
			}
		}
		return false
	})) > 0
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{}, nil
	}
	return strings.Split(s, "\n"), nil
}

func joinLines(ls []string) []byte { return []byte(strings.Join(ls, "\n") + "\n") }

func directive(l string) (key, val string, ok bool) {
	l = strings.TrimSpace(l)
	if l == "" || strings.HasPrefix(l, "#") {
		return "", "", false
	}
	key, val, _ = strings.Cut(l, "=")
	return strings.TrimSpace(key), strings.TrimSpace(val), true
}

// parseReservation reads a dhcp-host value: "aa:bb:..,192.168.1.50,tv" (other
// forms, like set:tags or a lease time, are read for MAC, address and name).
func parseReservation(v string) (Reservation, bool) {
	var r Reservation
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		switch {
		case f == "":
		case r.MAC == "" && isMAC(f):
			hw, _ := net.ParseMAC(f)
			r.MAC = hw.String()
		case r.IP == "" && net.ParseIP(f) != nil && net.ParseIP(f).To4() != nil:
			r.IP = net.ParseIP(f).String()
		case strings.Contains(f, ":"), f == "infinite", isLeaseTime(f):
			// set:tag, tag:, id:, lease time
		case r.Name == "":
			r.Name = f
		}
	}
	return r, r.MAC != "" && r.IP != ""
}

func isMAC(f string) bool {
	hw, err := net.ParseMAC(f)
	return err == nil && len(hw) == 6
}

func isLeaseTime(f string) bool {
	if _, err := strconv.Atoi(f); err == nil {
		return true
	}
	_, ok := leaseHours(f)
	return ok && len(f) > 1 && strings.ContainsAny(f[len(f)-1:], "mhdw")
}

func (s *State) dhcpSettings() *DHCPSettings {
	conf, err := readLines(dnsmasqConf)
	if err != nil {
		return nil
	}
	d := &DHCPSettings{On: dhcpRunning(), LeaseHours: 1, DNS: []string{}, Reservations: []Reservation{}}
	ranged := false
	add := func(lines []string) {
		for _, l := range lines {
			k, v, ok := directive(l)
			if !ok {
				continue
			}
			switch {
			case k == "dhcp-host":
				if r, ok := parseReservation(v); ok {
					d.Reservations = append(d.Reservations, r)
				}
			case k == "dhcp-range" && !ranged:
				ranged = true
				ips, lease := rangeParts(v)
				d.LeaseHours = lease
				if len(ips) > 0 {
					d.PoolStart = ips[0]
				}
				if len(ips) > 1 {
					d.PoolEnd = ips[1]
				}
			case k == "dhcp-option" && isDNSOption(v):
				d.DNS = dnsList(v)
			}
		}
	}
	add(conf)
	if res, err := readLines(reservationsConf); err == nil && includesReservations(conf) {
		add(res)
	}
	d.Leases = countLeases(s.cfg.DnsmasqLeases)
	return d
}

func includesReservations(conf []string) bool {
	for _, l := range conf {
		if k, v, ok := directive(l); ok && k == "conf-file" && v == reservationsConf {
			return true
		}
	}
	return false
}

func isDNSOption(v string) bool {
	return strings.HasPrefix(v, "option:dns-server,") || strings.HasPrefix(v, "6,")
}

func dnsList(v string) []string {
	out := []string{}
	for _, f := range strings.Split(v, ",")[1:] {
		if ip := net.ParseIP(strings.TrimSpace(f)); ip != nil {
			out = append(out, ip.String())
		}
	}
	return out
}

// rangeParts: the addresses in a dhcp-range (start, end, netmask) and the lease in hours.
func rangeParts(v string) ([]string, float64) {
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
	return ips, lease
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

// poolText: ".100 – .179" when both are in the same /24, for the Health page.
func poolText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	if i := strings.LastIndexByte(a, '.'); i > 0 && strings.HasPrefix(b, a[:i+1]) {
		return a[i:] + " – " + b[i:]
	}
	return a + " – " + b
}

func countLeases(path string) int {
	n := 0
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	now := time.Now().Unix()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var exp int64
		if _, err := fmt.Sscanf(sc.Text(), "%d", &exp); err == nil && (exp == 0 || exp > now) {
			n++
		}
	}
	return n
}

// ---- changing ----

type dhcpPlan struct {
	conf, res []byte // new file contents; res nil = unchanged
}

func ip4(v string) uint32 {
	ip := net.ParseIP(v).To4()
	if ip == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip)
}

// hostLabel makes a reservation name dnsmasq accepts: letters, digits and dashes.
func hostLabel(n string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(n)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

func (s *State) planDHCP(p *DHCPPatch) (*dhcpPlan, error) {
	cur := s.dhcpSettings()
	if cur == nil {
		return nil, bad("This gateway doesn't run a DHCP server (setup.sh --dhcp sets one up).")
	}
	conf, _ := readLines(dnsmasqConf)
	_, lan, err := net.ParseCIDR(s.setting("lan", s.cfg.LAN))
	if err != nil {
		return nil, bad("Set the local network first (Network).")
	}
	gw := s.setting("gateway_ip", s.cfg.GatewayIP)
	inLAN := func(v, what string) (string, error) {
		ip := checkIPv4(v)
		if ip == "" || !lan.Contains(net.ParseIP(ip)) {
			return "", bad("%s must be an address inside %s.", what, lan)
		}
		if ip == gw {
			return "", bad("%s can't be the gateway's own address (%s).", what, gw)
		}
		return ip, nil
	}
	start, end, lease, dns := cur.PoolStart, cur.PoolEnd, cur.LeaseHours, cur.DNS
	if p.PoolStart != nil {
		if start, err = inLAN(*p.PoolStart, "The pool's first address"); err != nil {
			return nil, err
		}
	}
	if p.PoolEnd != nil {
		if end, err = inLAN(*p.PoolEnd, "The pool's last address"); err != nil {
			return nil, err
		}
	}
	if p.PoolStart != nil || p.PoolEnd != nil {
		if ip4(start) > ip4(end) {
			return nil, bad("The pool starts after it ends (%s to %s).", start, end)
		}
		if g := ip4(gw); g != 0 && g >= ip4(start) && g <= ip4(end) {
			return nil, bad("The pool %s to %s contains the gateway's own address %s.", start, end, gw)
		}
	}
	if p.LeaseHours != nil {
		lease = math.Round(*p.LeaseHours)
		if lease < 1 || lease > 168 {
			return nil, bad("The lease time must be 1 to 168 hours.")
		}
	}
	if p.DNS != nil {
		dns = nil
		for _, v := range *p.DNS {
			ip := checkIPv4(v)
			if ip == "" {
				return nil, bad("%q isn't an IPv4 address (DNS servers look like 1.1.1.1).", v)
			}
			dns = append(dns, ip)
		}
		if len(dns) < 1 || len(dns) > 4 {
			return nil, bad("Give 1 to 4 DNS servers.")
		}
	}

	// dnsmasq.conf, with the changed lines replaced in place.
	var out []string
	sawRange, sawDNS := false, false
	hostsHere := !includesReservations(conf)
	for _, l := range conf {
		k, v, ok := directive(l)
		switch {
		case ok && k == "dhcp-range" && !sawRange:
			sawRange = true
			if p.PoolStart != nil || p.PoolEnd != nil || p.LeaseHours != nil {
				l = "dhcp-range=" + newRange(v, start, end, lease)
			}
		case ok && k == "dhcp-option" && isDNSOption(v):
			if sawDNS {
				continue
			}
			sawDNS = true
			if p.DNS != nil {
				l = "dhcp-option=option:dns-server," + strings.Join(dns, ",")
			}
		case ok && k == "dhcp-host" && p.Reservations != nil:
			continue // written below
		}
		out = append(out, l)
	}
	if !sawRange && (p.PoolStart != nil || p.PoolEnd != nil || p.LeaseHours != nil) {
		if start == "" || end == "" {
			return nil, bad("Give both the pool's first and last address.")
		}
		out = append(out, "dhcp-range="+newRange("", start, end, lease))
	}
	if !sawDNS && p.DNS != nil {
		out = append(out, "dhcp-option=option:dns-server,"+strings.Join(dns, ","))
	}
	plan := &dhcpPlan{}
	if p.Reservations != nil {
		var hosts []string
		macs, ips := map[string]bool{}, map[string]string{}
		for _, r := range *p.Reservations {
			if !isMAC(strings.TrimSpace(r.MAC)) {
				return nil, bad("%q isn't a MAC address (like aa:bb:cc:dd:ee:ff).", r.MAC)
			}
			hw, _ := net.ParseMAC(strings.TrimSpace(r.MAC))
			mac := hw.String()
			ip, err := inLAN(r.IP, "A reserved address")
			if err != nil {
				return nil, err
			}
			if macs[mac] {
				return nil, bad("%s has two reservations.", mac)
			}
			if other, dup := ips[ip]; dup {
				return nil, bad("%s is reserved for both %s and %s.", ip, other, mac)
			}
			macs[mac], ips[ip] = true, mac
			line := "dhcp-host=" + mac + "," + ip
			if n := hostLabel(r.Name); n != "" {
				line += "," + n
			}
			hosts = append(hosts, line)
		}
		if hostsHere {
			out = append(out, hosts...)
		} else {
			old, err := readLines(reservationsConf)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			var res []string
			for _, l := range old {
				if k, _, ok := directive(l); ok && k == "dhcp-host" {
					continue
				}
				res = append(res, l)
			}
			plan.res = joinLines(append(res, hosts...))
		}
	}
	plan.conf = joinLines(out)
	return plan, nil
}

// newRange rewrites a dhcp-range value with a new pool and lease, keeping tags and the netmask.
func newRange(old, start, end string, lease float64) string {
	lt := strconv.Itoa(int(lease)) + "h"
	if old == "" {
		return start + "," + end + "," + lt
	}
	var fs []string
	nIP, hadLease := 0, false
	for _, f := range strings.Split(old, ",") {
		t := strings.TrimSpace(f)
		switch {
		case net.ParseIP(t) != nil && nIP == 0:
			f, nIP = start, 1
		case net.ParseIP(t) != nil && nIP == 1:
			f, nIP = end, 2
		case net.ParseIP(t) == nil && !strings.Contains(t, ":") && (t == "infinite" || isLeaseTime(t)):
			f, hadLease = lt, true
		}
		fs = append(fs, f)
	}
	if !hadLease {
		fs = append(fs, lt)
	}
	return strings.Join(fs, ",")
}

func fileHash(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return string(h[:])
}

func (s *State) applyDHCP(pl *dhcpPlan) error {
	oldConf, err := os.ReadFile(dnsmasqConf)
	if err != nil {
		return err
	}
	oldRes, resErr := os.ReadFile(reservationsConf)
	if bytes.Equal(oldConf, pl.conf) && (pl.res == nil || (resErr == nil && bytes.Equal(oldRes, pl.res))) {
		return nil
	}
	// Check the new files together, in a temporary folder.
	dir, err := os.MkdirTemp("", "junction-dhcp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	res := pl.res
	if res == nil {
		res = oldRes
	}
	tmpRes := filepath.Join(dir, "reservations.conf")
	os.WriteFile(tmpRes, res, 0o644)
	testConf := strings.ReplaceAll(string(pl.conf), "conf-file="+reservationsConf, "conf-file="+tmpRes)
	tmpConf := filepath.Join(dir, "dnsmasq.conf")
	os.WriteFile(tmpConf, []byte(testConf), 0o644)
	if err := dnsmasqTest(tmpConf); err != nil {
		return bad("dnsmasq doesn't accept the new settings: %v", err)
	}

	running := dhcpRunning()
	put := func(conf, res []byte) error {
		if err := writeFileAtomic(dnsmasqConf, conf, 0o644); err != nil {
			return err
		}
		if res != nil {
			return writeFileAtomic(reservationsConf, res, 0o644)
		}
		return nil
	}
	writeFileAtomic(dnsmasqConf+".prev", oldConf, 0o644)
	if pl.res != nil && resErr == nil {
		writeFileAtomic(reservationsConf+".prev", oldRes, 0o644)
	}
	if err := put(pl.conf, pl.res); err != nil {
		return err
	}
	if !running {
		log.Printf("dhcp: new settings saved (the DHCP server isn't running)")
		return nil
	}
	if err := dhcpRestart(); err == nil && waitFor(dhcpWait, dhcpRunning) {
		log.Printf("dhcp: new settings in use")
		return nil
	} else {
		log.Printf("dhcp: the DHCP server didn't come back with the new settings (%v); putting the old ones back", err)
	}
	var back []byte
	if pl.res != nil && resErr == nil {
		back = oldRes
	}
	put(oldConf, back)
	dhcpRestart()
	return fmt.Errorf("the DHCP server didn't start with the new settings, so the old ones are back (see /var/log/dnsmasq.log)")
}

// waitFor polls ok until it holds twice in a row, a second apart, or the time is up.
func waitFor(d time.Duration, ok func() bool) bool {
	end := time.Now().Add(d)
	good := 0
	for time.Now().Before(end) {
		time.Sleep(time.Second)
		if ok() {
			if good++; good >= 2 {
				return true
			}
		} else {
			good = 0
		}
	}
	return false
}
