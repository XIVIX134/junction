package main

// Floating-gateway status for the app: who holds the gateway address (this box
// or the backup box) and every handover, from the keepalived
// notify script's files.

import (
	"os"
	"strings"
)

const (
	gatewayStateFile = "/run/gateway-state"
	gatewayLogFile   = "/var/log/gateway-failover.log"
)

type GatewayEvent struct {
	At    string `json:"at"`
	State string `json:"state"` // MASTER = this container holds .4; BACKUP/FAULT = the backup does
}

type GatewayStatus struct {
	Enabled bool           `json:"enabled"` // floating gateway set up on this container
	Role    string         `json:"role"`    // "master" | "backup" | "fault" | "stopped"
	Since   *string        `json:"since"`
	Events  []GatewayEvent `json:"events"` // newest first, up to 20
}

func readGatewayStatus() *GatewayStatus {
	if _, err := os.Stat(keepalivedConf); err != nil {
		return nil
	}
	gs := &GatewayStatus{Enabled: true, Role: "stopped", Events: []GatewayEvent{}}
	if b, err := os.ReadFile(gatewayStateFile); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 1 {
			gs.Role = strings.ToLower(f[0])
		}
		if len(f) >= 2 {
			gs.Since = &f[1]
		}
	}
	if b, err := os.ReadFile(gatewayLogFile); err == nil {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := len(lines) - 1; i >= 0 && len(gs.Events) < 20; i-- {
			f := strings.Fields(lines[i]) // 2026-11-13T20:22:54Z gateway MASTER
			if len(f) == 3 {
				gs.Events = append(gs.Events, GatewayEvent{At: f[0], State: f[2]})
			}
		}
	}
	return gs
}
