package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"
)

var httpc = &http.Client{Timeout: 4 * time.Second}

func getJSON(ctx context.Context, url, bearer string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ---- sing-box Clash API ----

type clashConn struct {
	ID       string   `json:"id"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Chains   []string `json:"chains"`
	Metadata struct {
		SourceIP      string `json:"sourceIP"`
		DestinationIP string `json:"destinationIP"`
		Host          string `json:"host"`
		Type          string `json:"type"`
	} `json:"metadata"`
}

type clashConns struct {
	DownloadTotal int64       `json:"downloadTotal"`
	UploadTotal   int64       `json:"uploadTotal"`
	Connections   []clashConn `json:"connections"`
}

type clashSelector struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

// ---- vpn-failoverd ----

type failoverStatus struct {
	Endpoints []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"endpoints"`
	State struct {
		Mode        string `json:"mode"`
		SelectedTag string `json:"selected_tag"`
		LastSwitch  string `json:"last_switch"`
		LastReason  string `json:"last_reason"`
		Health      map[string]struct {
			Known       bool   `json:"known"`
			Healthy     bool   `json:"healthy"`
			LatencyMs   int    `json:"latency_ms"`
			LastError   string `json:"last_error"`
			LastSuccess string `json:"last_success"`
			LastFailure string `json:"last_failure"`
		} `json:"health"`
	} `json:"state"`
}

// ---- ARP ----

type arpEntry struct {
	IP  string
	MAC string
}

// readARP returns complete entries (flag 0x2) on the given interface.
func readARP(iface string) ([]arpEntry, error) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []arpEntry
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 6 || fl[2] != "0x2" || fl[5] != iface || fl[3] == "00:00:00:00:00:00" {
			continue
		}
		out = append(out, arpEntry{IP: fl[0], MAC: strings.ToLower(fl[3])})
	}
	return out, sc.Err()
}

// sweep sends one tiny UDP datagram to every host in the LAN so the kernel
// resolves (and caches) its MAC. Devices that never talk to the gateway then
// still show up in /proc/net/arp. Port 9 is "discard".
func sweep(cidr string) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return
	}
	for a := p.Masked().Addr().Next(); p.Contains(a); a = a.Next() {
		if c, err := net.DialTimeout("udp4", net.JoinHostPort(a.String(), "9"), 50*time.Millisecond); err == nil {
			c.Write([]byte{0})
			c.Close()
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// directLatency measures a TCP handshake straight out (the gateway's own
// traffic is not sent through sing-box).
// It takes the best of three handshakes so one slow packet doesn't read as a slow line.
func directLatency() (time.Duration, bool) {
	best, ok := time.Duration(0), false
	for i := 0; i < 3; i++ {
		t := time.Now()
		c, err := net.DialTimeout("tcp4", "1.1.1.1:443", 3*time.Second)
		if err != nil {
			continue
		}
		c.Close()
		if d := time.Since(t); !ok || d < best {
			best, ok = d, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return best, ok
}

// ---- small helpers ----

var vendors = map[string]string{
	"bc:24:11": "Proxmox virtual NIC",
	"78:eb:46": "Huawei",
	"98:00:6a": "ZTE",
	"74:56:3c": "Gigabyte",
}

func vendorOf(mac string) string {
	if len(mac) >= 8 {
		return vendors[mac[:8]]
	}
	return ""
}

// randomMAC reports a locally administered address (bit 1 of the first octet),
// which is what phones use for "private Wi-Fi address".
func randomMAC(mac string) bool {
	var b byte
	if _, err := fmt.Sscanf(mac, "%02x", &b); err != nil {
		return false
	}
	return b&0x02 != 0
}

func isPrivate(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && (a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast())
}
