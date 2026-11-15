package main

// Automatic device names, from what devices say about themselves:
//   - DHCP: phones and PCs broadcast their hostname when they join (we only listen).
//   - mDNS: Apple devices, TVs, printers answer "who is 192.168.1.x?" with their .local name.
//   - UPnP/SSDP: TVs, consoles and media boxes advertise a friendly name.
// A name the user typed always wins over these.

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Source priority: a higher number replaces a lower one.
var sourceRank = map[string]int{"": 0, "vendor": 1, "mdns": 2, "dhcp": 3, "router": 3, "upnp": 4}

type nameHint struct {
	MAC, IP string // one of them identifies the device
	Name    string
	Source  string
	Model   string
}

func (s *State) applyHint(h nameHint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := h.MAC
	if id == "" {
		id = s.ipToID[h.IP]
	}
	d, ok := s.devs[id]
	if !ok {
		return
	}
	if h.Model != "" && d.Model != h.Model {
		d.Model, d.dirty = h.Model, true
	}
	if h.Name == "" {
		return
	}
	if sourceRank[h.Source] >= sourceRank[d.AutoSource] && (d.AutoName != h.Name || d.AutoSource != h.Source) {
		d.AutoName, d.AutoSource, d.dirty = h.Name, h.Source, true
	}
}

var (
	reRandomAndroid = regexp.MustCompile(`(?i)^android[-_][0-9a-f]{6,}$`)
	reLGTV          = regexp.MustCompile(`(?i)^lgwebostv`)
	reWinHost       = regexp.MustCompile(`^(DESKTOP|LAPTOP|WIN)-[A-Z0-9]+$`)
	reSpaces        = regexp.MustCompile(`\s+`)
	rePossessive    = regexp.MustCompile(`([A-Za-z])-s-`) // Android drops the apostrophe: "Alex-s-S26" = "Alex's S26"
)

// prettyName turns a hostname into something a person would call the device.
// It returns "" for names that carry no information (random Android ids).
func prettyName(h string) (name, model string) {
	h = strings.TrimSuffix(strings.TrimSpace(h), ".")
	for _, suf := range []string{".local", ".lan", ".home"} {
		h = strings.TrimSuffix(h, suf)
	}
	switch {
	case h == "" || h == "localhost":
		return "", ""
	case reRandomAndroid.MatchString(h):
		return "", "Android"
	case reLGTV.MatchString(h):
		return "LG TV", "LG webOS TV"
	case reWinHost.MatchString(h):
		return h, "Windows"
	}
	low := strings.ToLower(h)
	switch {
	case strings.Contains(low, "iphone"):
		model = "iPhone"
	case strings.Contains(low, "ipad"):
		model = "iPad"
	case strings.Contains(low, "macbook") || strings.Contains(low, "imac"):
		model = "Mac"
	case strings.Contains(low, "galaxy") || strings.Contains(low, "redmi") || strings.Contains(low, "pixel"):
		model = "Android"
	case strings.Contains(low, "playstation") || strings.HasPrefix(low, "ps5") || strings.HasPrefix(low, "ps4"):
		model = "PlayStation"
	case strings.Contains(low, "xbox"):
		model = "Xbox"
	}
	n := rePossessive.ReplaceAllString(h, "$1's ")
	n = strings.NewReplacer("-", " ", "_", " ").Replace(n)
	n = reSpaces.ReplaceAllString(n, " ")
	if len(n) > 40 {
		n = n[:40]
	}
	return n, model
}

// ---------- DHCP (passive) ----------

// listenDHCP reads DHCP requests that clients broadcast to port 67. The ISP
// router answers them; we only read the hostname and vendor class.
func (s *State) listenDHCP(ctx context.Context) {
	pc, err := net.ListenPacket("udp4", ":67")
	if err != nil {
		s.logf("DHCP name listener off: %v", err)
		return
	}
	go func() { <-ctx.Done(); pc.Close() }()
	buf := make([]byte, 1500)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if h, ok := parseDHCP(buf[:n]); ok {
			s.applyHint(h)
		}
	}
}

func parseDHCP(b []byte) (nameHint, bool) {
	if len(b) < 240 || b[0] != 1 || b[2] != 6 || binary.BigEndian.Uint32(b[236:240]) != 0x63825363 {
		return nameHint{}, false
	}
	mac := net.HardwareAddr(b[28:34]).String()
	var host, vendor string
	for i := 240; i < len(b); {
		code := b[i]
		if code == 255 {
			break
		}
		if code == 0 {
			i++
			continue
		}
		if i+1 >= len(b) {
			break
		}
		l := int(b[i+1])
		if i+2+l > len(b) {
			break
		}
		v := b[i+2 : i+2+l]
		switch code {
		case 12:
			host = string(v)
		case 60:
			vendor = string(v)
		case 81: // client FQDN: flags, rcode1, rcode2, name
			if host == "" && len(v) > 3 {
				host = strings.SplitN(string(v[3:]), ".", 2)[0]
			}
		}
		i += 2 + l
	}
	name, model := prettyName(host)
	switch v := strings.ToLower(vendor); {
	case strings.HasPrefix(v, "android"):
		model = "Android"
	case strings.HasPrefix(v, "msft"):
		model = "Windows"
	case strings.HasPrefix(v, "dhcpcd") || strings.HasPrefix(v, "udhcp"):
		if model == "" {
			model = "Linux"
		}
	}
	return nameHint{MAC: strings.ToLower(mac), Name: name, Source: "dhcp", Model: model}, true
}

// ---------- mDNS (reverse lookup) ----------

func reverseName(ip netip.Addr) string {
	a := ip.As4()
	return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", a[3], a[2], a[1], a[0])
}

// probeMDNS asks each IP "what is your name?" over mDNS, both directly
// (legacy unicast query) and on the multicast group, and reads the answers.
func (s *State) probeMDNS(ctx context.Context) {
	ips := s.onlineIPs()
	if len(ips) == 0 {
		return
	}
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer pc.Close()
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil || !addr.Is4() {
			continue
		}
		q, err := mdnsQuery(reverseName(addr))
		if err != nil {
			continue
		}
		pc.WriteTo(q, &net.UDPAddr{IP: net.ParseIP(ip), Port: 5353})
		pc.WriteTo(q, group)
		time.Sleep(20 * time.Millisecond)
	}
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 9000)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		src := from.(*net.UDPAddr).IP.String()
		var p dnsmessage.Parser
		if _, err := p.Start(buf[:n]); err != nil {
			continue
		}
		p.SkipAllQuestions()
		for {
			h, err := p.AnswerHeader()
			if err != nil {
				break
			}
			if h.Type != dnsmessage.TypePTR {
				p.SkipAnswer()
				continue
			}
			r, err := p.PTRResource()
			if err != nil {
				break
			}
			// The PTR owner tells us which IP the name belongs to.
			owner := ptrOwnerIP(h.Name.String())
			if owner == "" {
				owner = src
			}
			if name, model := prettyName(r.PTR.String()); name != "" || model != "" {
				s.applyHint(nameHint{IP: owner, Name: name, Source: "mdns", Model: model})
			}
		}
	}
}

func mdnsQuery(name string) ([]byte, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, err
	}
	m := dnsmessage.Message{Questions: []dnsmessage.Question{{
		Name: n, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET | 0x8000, // ask for a unicast reply
	}}}
	return m.Pack()
}

func ptrOwnerIP(owner string) string {
	parts := strings.Split(strings.TrimSuffix(owner, ".in-addr.arpa."), ".")
	if len(parts) != 4 {
		return ""
	}
	ip := parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0]
	if _, err := netip.ParseAddr(ip); err != nil {
		return ""
	}
	return ip
}

// ---------- UPnP / SSDP ----------

func (s *State) probeSSDP(ctx context.Context) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer pc.Close()
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: upnp:rootdevice\r\n\r\n"
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for i := 0; i < 2; i++ {
		pc.WriteTo([]byte(msg), dst)
		time.Sleep(300 * time.Millisecond)
	}
	locs := map[string]string{} // ip -> description URL
	pc.SetReadDeadline(time.Now().Add(4 * time.Second))
	buf := make([]byte, 4096)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		ip := from.(*net.UDPAddr).IP.String()
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				if _, seen := locs[ip]; !seen {
					locs[ip] = strings.TrimSpace(v)
				}
			}
		}
	}
	s.mu.RLock()
	self := s.selfIP
	s.mu.RUnlock()
	for ip, loc := range locs {
		if self[ip] {
			continue
		}
		name, model := upnpDescribe(ctx, loc)
		if name != "" || model != "" {
			s.applyHint(nameHint{IP: ip, Name: name, Source: "upnp", Model: model})
		}
	}
}

func upnpDescribe(ctx context.Context, loc string) (string, string) {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "http" {
		return "", ""
	}
	if a, err := netip.ParseAddr(u.Hostname()); err != nil || !a.IsPrivate() { // only fetch from the LAN
		return "", ""
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", ""
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var d struct {
		Device struct {
			FriendlyName string `xml:"friendlyName"`
			Manufacturer string `xml:"manufacturer"`
			ModelName    string `xml:"modelName"`
		} `xml:"device"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&d); err != nil {
		return "", ""
	}
	// "[LG] webOS TV UQ79006LD" -> "LG webOS TV UQ79006LD"
	name := strings.TrimSpace(strings.NewReplacer("[", "", "]", "").Replace(d.Device.FriendlyName))
	name = reSpaces.ReplaceAllString(name, " ")
	if len(name) > 40 {
		name = name[:40]
	}
	maker, modelName := strings.TrimSpace(d.Device.Manufacturer), strings.TrimSpace(d.Device.ModelName)
	model := modelName
	if maker != "" && !strings.HasPrefix(strings.ToLower(modelName), strings.ToLower(strings.Fields(maker)[0])) {
		model = strings.TrimSpace(maker + " " + modelName) // "Huawei" + "Huawei EchoLife" stays "Huawei EchoLife"
	}
	if len(model) > 40 {
		model = model[:40]
	}
	return name, model
}

func (s *State) onlineIPs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	now := time.Now()
	for _, d := range s.devs {
		if now.Sub(d.LastSeen) < 10*time.Minute && !s.selfIP[d.IP] {
			out = append(out, d.IP)
		}
	}
	return out
}
