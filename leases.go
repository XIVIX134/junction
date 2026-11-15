package main

// Names from dnsmasq's lease file, once the gateway hands out addresses itself.
// Format per line: <expiry> <mac> <ip> <hostname or *> <client-id or *>

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

func (s *State) pollLeases(context.Context) {
	f, err := os.Open(s.cfg.DnsmasqLeases)
	if err != nil {
		return // not switched over yet
	}
	defer f.Close()
	leased := map[string]bool{}
	leaseNames, leaseIPs := map[string]string{}, map[string]string{}
	now := time.Now().Unix()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 4 {
			continue
		}
		if exp, err := strconv.ParseInt(fl[0], 10, 64); err == nil && (exp == 0 || exp > now) {
			mac := strings.ToLower(fl[1])
			leased[mac] = true
			leaseIPs[mac] = fl[2]
			leaseNames[mac] = mac
			if fl[3] != "*" {
				leaseNames[mac] = fl[3]
			}
		}
		if fl[3] == "*" {
			continue
		}
		name, model := prettyName(fl[3])
		if name == "" && model == "" {
			continue
		}
		s.applyHint(nameHint{MAC: strings.ToLower(fl[1]), Name: name, Source: "dhcp", Model: model})
	}
	hosts := map[string]string{}
	for mac, n := range leaseNames {
		if n != mac {
			hosts[mac] = n
		}
	}
	s.autoLink(hosts)
	s.mu.Lock()
	prev := s.leased
	s.leased = leased
	s.mu.Unlock()
	if prev != nil {
		for mac := range leased {
			if !prev[mac] {
				s.store.addEvent(time.Now(), "dhcp", mac, 0, leaseNames[mac]+" got its address from the gateway ("+leaseIPs[mac]+")")
			}
		}
	}
}
