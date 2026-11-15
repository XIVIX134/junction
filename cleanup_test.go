package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The WE ISP counter is gone (v1.7): its saved session and readings are removed
// at start, and nothing else is touched.
func TestDropsWELeftovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.db")
	st, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.setSetting("isp_session", `{"utoken":"secret"}`)
	st.setSetting("isp_login_fail", "1|1|locked")
	st.setSetting("ispx", "keep: not an isp_ key")
	st.setSetting("brand", `{"name":"Home"}`)
	if _, err := st.db.Exec(`CREATE TABLE IF NOT EXISTS isp_readings (at INTEGER, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	st.db.Close()

	st, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.db.Close()
	for _, k := range []string{"isp_session", "isp_login_fail"} {
		if _, ok := st.getSetting(k); ok {
			t.Errorf("%s still there", k)
		}
	}
	for _, k := range []string{"ispx", "brand"} {
		if _, ok := st.getSetting(k); !ok {
			t.Errorf("%s was removed too", k)
		}
	}
	var n int
	st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'isp_readings'`).Scan(&n)
	if n != 0 {
		t.Error("isp_readings still exists")
	}
}

// The deleted tokens must not linger in the database file's free pages.
func TestWELeftoversOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.db")
	st, _ := openStore(path)
	st.setSetting("isp_session", `{"utoken":"TOKEN-THAT-MUST-VANISH-1234567890"}`)
	st.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	st.db.Close()
	st, _ = openStore(path)
	st.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	st.db.Close()
	for _, f := range []string{path, path + "-wal"} {
		b, _ := os.ReadFile(f)
		if bytes.Contains(b, []byte("TOKEN-THAT-MUST-VANISH")) {
			t.Fatalf("token still readable in %s", filepath.Base(f))
		}
	}
}
