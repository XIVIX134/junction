package main

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadWireCounters(t *testing.T) {
	old := iptablesSave
	t.Cleanup(func() { iptablesSave = old })
	iptablesSave = func() ([]byte, error) {
		return []byte(`*filter
:INPUT ACCEPT [10:1000]
[5:500] -A INPUT -j JUNCTION_WIRE_IN
[1:60] -A JUNCTION_WIRE_IN -s 192.168.0.0/16 -j RETURN
[42:546400000] -A JUNCTION_WIRE_IN -m comment --comment junction-wire -j RETURN
[40:12000000] -A JUNCTION_WIRE_OUT -m comment --comment junction-wire -j RETURN
COMMIT
`), nil
	}
	in, out, err := readWireCounters()
	if err != nil || in != 546400000 || out != 12000000 {
		t.Fatalf("in %d out %d err %v", in, out, err)
	}
	iptablesSave = func() ([]byte, error) { return []byte("*filter\nCOMMIT\n"), nil }
	if _, _, err := readWireCounters(); err == nil {
		t.Fatal("missing rules should be an error")
	}
}

func TestEnsureWireRules(t *testing.T) {
	old := iptables
	t.Cleanup(func() { iptables = old })
	var calls []string
	chains := map[string]bool{}
	iptables = func(args ...string) ([]byte, error) {
		a := strings.Join(args, " ")
		calls = append(calls, a)
		switch {
		case strings.Contains(a, " -L "):
			if !chains[args[len(args)-1]] {
				return nil, errors.New("no chain")
			}
		case strings.Contains(a, " -N "):
			chains[args[len(args)-1]] = true
		case strings.Contains(a, " -C "):
			return nil, errors.New("no rule")
		}
		return nil, nil
	}
	if err := ensureWireRules(); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{
		"-t filter -N JUNCTION_WIRE_IN", "-t filter -N JUNCTION_WIRE_OUT",
		"-t filter -A JUNCTION_WIRE_IN -s 192.168.0.0/16 -j RETURN",
		"-t filter -A JUNCTION_WIRE_OUT -d 10.0.0.0/8 -j RETURN",
		"-t filter -A JUNCTION_WIRE_IN -m comment --comment junction-wire -j RETURN",
		"-t filter -I INPUT 1 -j JUNCTION_WIRE_IN", "-t filter -I OUTPUT 1 -j JUNCTION_WIRE_OUT",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	// It only ever adds RETURN rules and jumps to its own chains.
	for _, c := range calls {
		if strings.Contains(c, " -j ") && !strings.HasSuffix(c, "-j RETURN") && !strings.HasSuffix(c, "-j JUNCTION_WIRE_IN") && !strings.HasSuffix(c, "-j JUNCTION_WIRE_OUT") {
			t.Errorf("unexpected rule: %s", c)
		}
		if strings.Contains(c, "-D ") || strings.Contains(c, "-X ") || strings.Contains(c, " -F INPUT") || strings.Contains(c, " -F OUTPUT") {
			t.Errorf("touches rules it doesn't own: %s", c)
		}
	}
}

func TestBilled(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	s := &State{loc: time.UTC, store: st, wirePending: map[int64]int64{}}
	h0 := time.Now().Truncate(time.Hour).Add(-5 * time.Hour)
	H := func(i int) int64 { return h0.Add(time.Duration(i) * time.Hour).Unix() }
	// Wire counting started 20 min into hour 2: hour 2 is partial, hours 3+ are counted in full.
	st.setSetting("wire_since", strconv.FormatInt(h0.Add(2*time.Hour+20*time.Minute).Unix(), 10))
	st.addWire(map[int64]int64{H(2): 300, H(3): 1070})
	s.wirePending[H(4)] = 535
	content := map[int64]int64{H(1): 1000, H(2): 1000, H(3): 1000, H(4): 500}
	got, f := s.billed(content, h0, time.Now().Add(time.Hour))
	if f != defaultOverhead {
		t.Fatalf("overhead %v, want the default until 1 GB is measured", f)
	}
	want := map[int64]int64{H(1): 1061, H(2): 1061, H(3): 1070, H(4): 535}
	for h, w := range want {
		if got[h] != w {
			t.Errorf("hour %d: %d, want %d", (h-h0.Unix())/3600, got[h], w)
		}
	}
}

// Old hours always use the fixed 6.1%, however the live measurement moves.
func TestOldHoursDontDrift(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	s := &State{loc: time.UTC, store: st, wirePending: map[int64]int64{}}
	s.ohF, s.ohAt = 1.09, time.Now() // a heavy-upload evening
	h := time.Now().Truncate(time.Hour).Add(-48 * time.Hour).Unix()
	got, f := s.billed(map[int64]int64{h: 1000}, time.Unix(h, 0), time.Now())
	if f != defaultOverhead || got[h] != 1061 {
		t.Fatalf("old hour billed %d with %v, want 1061 with %v", got[h], f, defaultOverhead)
	}
}
