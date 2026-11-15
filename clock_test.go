package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestClockGuardWaits(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	s := &State{store: st}
	if !s.clockFloor().IsZero() {
		t.Fatal("no last_alive yet: must not wait")
	}
	future := time.Now().Add(time.Hour)
	st.setSetting("last_alive", strconv.FormatInt(future.Unix(), 10))
	if !time.Now().Before(s.clockFloor()) {
		t.Fatal("clock behind last_alive must be detected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.run(ctx); close(done) }()
	select {
	case <-done: // run returns at once and waits in the background
	case <-time.After(2 * time.Second):
		t.Fatal("run blocked while waiting for the clock")
	}
	if v, _ := st.getSetting("last_alive"); v != strconv.FormatInt(future.Unix(), 10) {
		t.Fatal("loops started (last_alive overwritten) although the clock is behind")
	}
}
