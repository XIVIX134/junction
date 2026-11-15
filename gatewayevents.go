package main

// Turns new lines of the keepalived notify log into timeline events.

import (
	"bufio"
	"os"
	"strings"
	"time"
)

func (s *State) pollGatewayLog() {
	f, err := os.Open(gatewayLogFile)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if s.gwLogOffset == 0 || st.Size() < s.gwLogOffset {
		// first look (or the log was rotated): start from the end, don't replay history
		s.gwLogOffset = st.Size()
		return
	}
	if _, err := f.Seek(s.gwLogOffset, 0); err != nil {
		return
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text()) // 2026-11-13T20:22:54Z gateway MASTER
		if len(fl) != 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, fl[0])
		if err != nil {
			at = time.Now()
		}
		text := map[string]string{
			"MASTER": "This gateway took over: VPN and controls are active",
			"BACKUP": "The backup on the Proxmox server took over: internet goes direct, no VPN",
			"FAULT":  "Gateway health check failed: the backup took over (internet direct, no VPN)",
			"STOP":   "Gateway failover stopped",
		}[fl[2]]
		if text != "" {
			s.store.addEvent(at, "gateway", "", 0, text)
		}
	}
	s.gwLogOffset = st.Size()
}
