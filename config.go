package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
)

// Config is read from /etc/junction/config.json. Every field has a default so
// an empty file (or none at all) works on the gateway as it is today.
type Config struct {
	Listen        string            `json:"listen"`         // ":80"
	DB            string            `json:"db"`             // "/var/lib/junction/junction.db"
	SingBoxConfig string            `json:"singbox_config"` // read only for the Clash API secret
	ClashAPI      string            `json:"clash_api"`      // "http://127.0.0.1:9090"
	FailoverAPI   string            `json:"failover_api"`   // vpn-failoverd status endpoint
	LAN           string            `json:"lan"`            // "192.168.1.0/24", swept to fill the ARP table
	Interface     string            `json:"interface"`      // "eth0"
	TimeZone      string            `json:"timezone"`       // e.g. "Europe/London"; empty = the system's, else UTC
	SeedNames     map[string]string `json:"seed_names"`     // MAC -> name, used only until the user renames
	SeedGroups    map[string]string `json:"seed_groups"`    // MAC -> group

	FailoverEnabled bool `json:"failover_enabled"` // Junction switches between VPN exits by itself
	// The VPN exits and how to check them: endpoints, probe ports, profiles,
	// thresholds (see API.md, "failover"). Older installs keep them in a separate
	// file (failover_config, the old vpn-failover daemon's format); used only when
	// this section is absent.
	Failover       json.RawMessage `json:"failover"`
	FailoverConfig string          `json:"failover_config"`

	// DHCP: listen passively on :67 for hostnames until the gateway runs its own
	// DHCP server; then set dhcp_listen=false (dnsmasq needs the port) and names
	// come from the lease file instead.
	DHCPListen    *bool  `json:"dhcp_listen"`
	DnsmasqLeases string `json:"dnsmasq_leases"`

	RouterURL      string `json:"router_url"` // "https://192.168.1.1"; empty = don't read the router
	RouterUser     string `json:"router_user"`
	RouterPassword string `json:"router_password"`

	// v1.6. Empty values are worked out at start (see resolve).
	GatewayIP      string `json:"gateway_ip"`      // the address devices use as gateway; "" = keepalived's floating address, else the interface's
	DefaultProfile string `json:"default_profile"` // VPN profile for devices without their own; "" = the failover's, else the first one
	BrandName      string `json:"brand_name"`      // "Junction"
	BackupName     string `json:"backup_name"`     // "Backup": what the backup gateway box is called in the app
	BackupIP       string `json:"backup_ip"`       // "" = keepalived's unicast_peer
	UpdateURL      string `json:"update_url"`      // GitHub releases API URL; "" = the official releases (defaultUpdateURL)

	foAPISet bool // the file named failover_api itself (even as "")
}

func defaultConfig() Config {
	return Config{
		Listen:         ":80",
		DB:             "/var/lib/junction/junction.db",
		SingBoxConfig:  "/etc/sing-box/config.json",
		ClashAPI:       "http://127.0.0.1:9090",
		Interface:      "eth0",
		FailoverConfig: "/etc/vpn-failover/config.json",
		DnsmasqLeases:  "/var/lib/misc/dnsmasq.leases",
		BrandName:      "Junction",
		BackupName:     "Backup",
	}
}

// keepalivedConf is where the floating gateway address is set up (a var for tests).
var keepalivedConf = "/etc/keepalived/keepalived.conf"

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	var keys map[string]json.RawMessage
	json.Unmarshal(b, &keys)
	_, c.foAPISet = keys["failover_api"]
	return c, nil
}

// resolve fills the values that default to something found on this box.
func (c *Config) resolve() (gatewayDetected bool) {
	if c.TimeZone == "" {
		c.TimeZone = detectTimezone("UTC")
	}
	if c.LAN == "" {
		c.LAN = detectLAN(c.Interface)
	}
	if c.GatewayIP == "" {
		c.GatewayIP, gatewayDetected = detectGatewayIP(c.Interface), true
	}
	if !c.foAPISet && c.GatewayIP != "" {
		c.FailoverAPI = "http://" + c.GatewayIP + ":7070/api/status"
	}
	return gatewayDetected
}

// detectLAN is the network of the interface's first IPv4 address, e.g.
// 192.168.1.7/24 -> "192.168.1.0/24"; "" when there's none (no sweep then).
func detectLAN(iface string) string {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return (&net.IPNet{IP: ipn.IP.Mask(ipn.Mask), Mask: ipn.Mask}).String()
		}
	}
	return ""
}

// detectGatewayIP: the floating address when keepalived manages one (that is
// what devices use; the box's own address is different), else the interface's
// first IPv4 address.
func detectGatewayIP(iface string) string {
	if vip := keepalivedValue("virtual_ipaddress"); vip != "" {
		return vip
	}
	return ifaceIPv4(iface)
}

func ifaceIPv4(iface string) string {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

// keepalivedValue returns the first IPv4 address inside a block of
// keepalived.conf ("virtual_ipaddress { 192.168.1.4/24 dev eth0 }", "unicast_peer { ... }").
func keepalivedValue(block string) string {
	f, err := os.Open(keepalivedConf)
	if err != nil {
		return ""
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		line, _, _ = strings.Cut(line, "!")
		for _, w := range strings.Fields(line) {
			switch {
			case w == block:
				in = true
			case !in:
			case w == "{":
			case w == "}":
				in = false
			default:
				ip, _, _ := strings.Cut(w, "/")
				if p := net.ParseIP(ip); p != nil && p.To4() != nil {
					return p.String()
				}
			}
		}
	}
	return ""
}

// clashSecret pulls experimental.clash_api.secret out of the sing-box config.
func clashSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var sb struct {
		Experimental struct {
			ClashAPI struct {
				Secret string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(b, &sb); err != nil {
		return "", err
	}
	return sb.Experimental.ClashAPI.Secret, nil
}

// defaultUpdateURL: where new versions are looked for when update_url isn't set.
const defaultUpdateURL = "https://api.github.com/repos/XIVIX134/junction/releases/latest"

// updateURL is update_url, else the official releases.
func (c Config) updateURL() string {
	if c.UpdateURL != "" {
		return c.UpdateURL
	}
	return defaultUpdateURL
}

// updateCheckDefault: checks are on by default only where update_url was set by hand.
func (c Config) updateCheckDefault() string {
	if c.UpdateURL != "" {
		return "1"
	}
	return "0"
}
