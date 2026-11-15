package main

import (
	"encoding/binary"
	"testing"
)

func TestPrettyName(t *testing.T) {
	cases := []struct{ in, name, model string }{
		{"Galaxy-S23", "Galaxy S23", "Android"},
		{"Johns-iPhone.local.", "Johns iPhone", "iPhone"},
		{"LGwebOSTV-bY3K-1.local.", "LG TV", "LG webOS TV"},
		{"android-1a2b3c4d5e6f7a8b", "", "Android"},
		{"DESKTOP-7Q2K9LM", "DESKTOP-7Q2K9LM", "Windows"},
		{"", "", ""},
		{"Alex-s-S26-Ultra", "Alex's S26 Ultra", ""},
		{"realme-11-Pro-5G", "realme 11 Pro 5G", ""},
	}
	for _, c := range cases {
		n, m := prettyName(c.in)
		if n != c.name || m != c.model {
			t.Errorf("prettyName(%q) = %q, %q; want %q, %q", c.in, n, m, c.name, c.model)
		}
	}
}

func TestParseDHCP(t *testing.T) {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	copy(b[28:], []byte{0x3e, 0x00, 0x00, 0x00, 0xa5, 0x23})
	binary.BigEndian.PutUint32(b[236:], 0x63825363)
	b = append(b, 53, 1, 3) // DHCPREQUEST
	b = append(b, 12, byte(len("Galaxy-A54")))
	b = append(b, "Galaxy-A54"...)
	b = append(b, 60, byte(len("android-dhcp-14")))
	b = append(b, "android-dhcp-14"...)
	b = append(b, 255)
	h, ok := parseDHCP(b)
	if !ok || h.MAC != "3e:00:00:00:a5:23" || h.Name != "Galaxy A54" || h.Model != "Android" || h.Source != "dhcp" {
		t.Fatalf("got %+v ok=%v", h, ok)
	}
	if _, ok := parseDHCP(b[:100]); ok {
		t.Fatal("short packet accepted")
	}
}

func TestParseRouterDevices(t *testing.T) {
	page := `var UserDevList = new Array(new USERDevice("d.1","192\x2e168\x2e1\x2e26","02\x3a4a\x3a00\x3a00\x3a00\x3a0c","SSID5","DHCP","\x2d\x2d","Online","WIFI","2\x3a13","realme\x2d11\x2dPro\x2d5G","1","1","0","","0","0","0","0"),new USERDevice("d.2","192\x2e168\x2e1\x2e4","bc\x3a24\x3a11\x3a00\x3a00\x3a54","LAN1","Static","\x2d\x2d","Online","ETH","4\x3a45","","1","1","0","","0","0","0","0"),null);`
	d := parseRouterDevices(page)
	if len(d) != 2 || d[0].IP != "192.168.1.26" || d[0].MAC != "02:4a:00:00:00:0c" || d[0].HostName != "realme-11-Pro-5G" || d[1].HostName != "" {
		t.Fatalf("got %+v", d)
	}
}

func TestPtrOwner(t *testing.T) {
	if got := ptrOwnerIP("238.1.168.192.in-addr.arpa."); got != "192.168.1.238" {
		t.Fatalf("got %q", got)
	}
}

func TestHostKey(t *testing.T) {
	if hostKey("Alex-s-S26-Ultra") != hostKey("alex-s26-ultra") || hostKey("alex-s26-ultra") == "" {
		t.Errorf("S26 names should match: %q vs %q", hostKey("Alex-s-S26-Ultra"), hostKey("alex-s26-ultra"))
	}
	for _, generic := range []string{"android", "iPhone", "android-1a2b3c4d5e6f", "laptop", "abc"} {
		if hostKey(generic) != "" {
			t.Errorf("%q must not be used for linking", generic)
		}
	}
	if hostKey("realme-11-Pro-5G") == hostKey("POCO-F3") {
		t.Error("different phones must not match")
	}
}

func TestAdjacentMACs(t *testing.T) {
	s := &State{devs: map[string]*devState{}}
	for _, m := range []string{"68:6c:e6:00:10:d6", "68:6c:e6:00:10:d9", "68:6c:e6:00:10:f0", "bc:24:11:00:00:4c", "3e:00:00:00:a5:23", "3e:00:00:00:a5:24"} {
		s.devs[m] = &devState{devRow: devRow{ID: m, Mac: m}}
	}
	got := s.adjacentMACs()
	if len(got) != 1 {
		t.Fatalf("want only the Xbox pair (random MACs and far-apart ones ignored), got %v", got)
	}
	s.devs["68:6c:e6:00:10:d6"].Name = "Xbox Series X"
	s.linkMembers(got[0], "is another connection of")
	if s.devs["68:6c:e6:00:10:d9"].LinkTo != "68:6c:e6:00:10:d6" || s.devs["68:6c:e6:00:10:d6"].LinkTo != "" {
		t.Fatalf("named device should be the primary: %+v %+v", s.devs["68:6c:e6:00:10:d9"].devRow, s.devs["68:6c:e6:00:10:d6"].devRow)
	}
	s.devs["68:6c:e6:00:10:d9"].LinkTo = "-" // user unlinked it: never relink
	s.linkMembers(got[0], "is another connection of")
	if s.devs["68:6c:e6:00:10:d9"].LinkTo != "-" {
		t.Fatal("unlinked device was relinked")
	}
}
