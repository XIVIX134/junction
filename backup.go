package main

// GET /api/backup: the database and config.json as one .tar.gz. Sign-in
// sessions, the router's password and the failover's API token stay out; VPN
// keys live in sing-box's config, which isn't included.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (a *API) getBackup(w http.ResponseWriter, r *http.Request) {
	s := a.s
	dir, err := os.MkdirTemp(filepath.Dir(s.cfg.DB), ".backup")
	if err != nil {
		dir, err = os.MkdirTemp("", "junction-backup")
	}
	if err != nil {
		fail(w, 500, "Could not make the backup: "+err.Error())
		return
	}
	defer os.RemoveAll(dir)
	dbCopy := filepath.Join(dir, "junction.db")
	if _, err := s.store.db.Exec(`VACUUM INTO ?`, dbCopy); err != nil {
		fail(w, 500, "Could not copy the database: "+err.Error())
		return
	}
	if db, err := sql.Open("sqlite", "file:"+dbCopy); err == nil {
		db.Exec(`DELETE FROM sessions`)
		db.Exec(`VACUUM`)
		db.Close()
	}
	dbBytes, err := os.ReadFile(dbCopy)
	if err != nil {
		fail(w, 500, "Could not read the database copy: "+err.Error())
		return
	}
	conf := []byte("{}\n")
	if b, err := os.ReadFile(s.cfgPath); err == nil {
		m := map[string]any{}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if dec.Decode(&m) == nil {
			delete(m, "router_password")
			if fo, ok := m["failover"].(map[string]any); ok {
				delete(fo, "token")
				delete(fo, "clash_secret")
			}
			conf, _ = json.MarshalIndent(m, "", "  ")
			conf = append(conf, '\n')
		}
	}
	now := time.Now()
	readme := fmt.Sprintf("Junction %s backup, %s.\n\njunction.db: devices, names, rules, usage history and settings (sign-in sessions removed).\nconfig.json: the gateway's config, without the router's password and the failover's API token.\n\nTo restore: stop junctiond, put junction.db where \"db\" points (default /var/lib/junction/junction.db),\nmerge config.json into /etc/junction/config.json (add the router password again), start junctiond.\n", version, now.Format(time.RFC3339))

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		b    []byte
	}{{"junction-backup/README.txt", []byte(readme)}, {"junction-backup/config.json", conf}, {"junction-backup/junction.db", dbBytes}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o600, Size: int64(len(f.b)), ModTime: now, Typeflag: tar.TypeReg})
		tw.Write(f.b)
	}
	tw.Close()
	gz.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="junction-backup-%s.tar.gz"`, now.In(s.loc).Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}
