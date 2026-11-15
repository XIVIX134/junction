package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Failover settings live in Junction's config; the old separate file is only a fallback.
func TestFailoverSettingsSource(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old.json")
	os.WriteFile(legacy, []byte(`{"endpoints":[{"key":"old","name":"Old VPN","probe_ports":{"default":1}}]}`), 0o600)

	cfg := Config{FailoverConfig: legacy}
	if e := readExitList(cfg); len(e) != 1 || e[0].Key != "old" {
		t.Fatalf("legacy file not used: %+v", e)
	}
	cfg.Failover = []byte(`{"default_profile":"streaming","endpoints":[{"key":"home","name":"Home VPN","probe_ports":{"streaming":1}},{"key":"travel","name":"Travel VPN","probe_ports":{"streaming":2}}]}`)
	if e := readExitList(cfg); len(e) != 2 || e[0].Key != "home" {
		t.Fatalf("config section not preferred: %+v", e)
	}
	if p := failoverDefaultProfile(cfg); p != "streaming" {
		t.Fatalf("default profile %q", p)
	}
	f, err := loadFailover(cfg, &State{})
	if err != nil || len(f.cfg.Endpoints) != 2 || f.cfg.StatePath != "/var/lib/junction/failover-state.json" {
		t.Fatalf("loadFailover: %v %+v", err, f)
	}
}
