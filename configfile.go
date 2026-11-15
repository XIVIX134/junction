package main

// Changing config.json from the app (Settings). The file is edited as plain
// JSON, so keys Junction doesn't know about stay; the new file must still load,
// it is written via a temporary file and a rename, and the previous one is kept
// next to it as config.json.prev. Junction reads config.json only at start, so
// every change here restarts it (a few seconds; traffic doesn't depend on it).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

func editConfigFile(path string, edit func(m map[string]any) error) error {
	m := map[string]any{}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := json.NewDecoder(bytes.NewReader(old))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("%s isn't valid JSON (%v); fix it by hand first", path, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	if err := edit(m); err != nil {
		return err
	}
	cleanSeeds(m)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	out := buf.Bytes()
	var c Config
	if err := json.Unmarshal(out, &c); err != nil {
		return fmt.Errorf("the new config wouldn't load: %v", err)
	}
	if bytes.Equal(old, out) {
		return nil
	}
	if len(old) > 0 {
		if err := writeFileAtomic(path+".prev", old, 0o600); err != nil {
			return fmt.Errorf("keeping a copy of %s: %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, out, 0o600)
}

// cleanSeeds drops starting names/groups whose key isn't a MAC address (they
// can never match a device) and empty maps.
func cleanSeeds(m map[string]any) {
	for _, k := range []string{"seed_names", "seed_groups"} {
		v, ok := m[k].(map[string]any)
		if !ok {
			continue
		}
		for mac := range v {
			if _, err := net.ParseMAC(mac); err != nil {
				delete(v, mac)
			}
		}
		if len(v) == 0 {
			delete(m, k)
		}
	}
}

// setOrDelete sets m[k] = v, or removes the key when v is the zero value (so the default applies).
func setOrDelete(m map[string]any, k string, v any) {
	switch x := v.(type) {
	case string:
		if x == "" {
			delete(m, k)
			return
		}
	case nil:
		delete(m, k)
		return
	}
	m[k] = v
}
