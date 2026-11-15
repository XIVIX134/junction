package main

// Per-device speed caps with Linux tc on the gateway's LAN interface.
//
// Every device's internet traffic passes the gateway: downloads leave eth0 towards
// the device (shaped by an HTB class per capped device, fq_codel inside), uploads
// arrive on eth0 from the device (policed on ingress). Everything else - other
// devices, keepalived's announcements, the gateway's own traffic - stays in an
// unlimited default class. With no caps at all, every qdisc is removed and the
// interface is exactly as before.

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

type Shaper struct {
	dev  string
	mu   sync.Mutex
	last string // last applied plan, to skip no-op rebuilds
	err  error
}

// Apply sets the caps: IP -> Mbit/s. Rebuilds only when the set changed.
func (sh *Shaper) Apply(caps map[string]float64) {
	if sh == nil { // no shaper (tests, or no tc): nothing to cap
		return
	}
	ips := make([]string, 0, len(caps))
	for ip, v := range caps {
		if v > 0 {
			ips = append(ips, ip)
		}
	}
	sort.Strings(ips)
	var plan strings.Builder
	for _, ip := range ips {
		fmt.Fprintf(&plan, "%s=%g;", ip, caps[ip])
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if plan.String() == sh.last {
		return
	}
	if _, err := exec.LookPath("tc"); err != nil {
		if len(ips) > 0 {
			log.Printf("shaper: tc is not installed; speed caps are not enforced")
		}
		sh.last = plan.String()
		return
	}
	// Start clean every time: a full rebuild is simpler to reason about than edits.
	exec.Command("tc", "qdisc", "del", "dev", sh.dev, "root").Run()
	exec.Command("tc", "qdisc", "del", "dev", sh.dev, "ingress").Run()
	if len(ips) == 0 {
		sh.last, sh.err = "", nil
		log.Printf("shaper: no speed caps")
		return
	}
	var b bytes.Buffer
	d := sh.dev
	fmt.Fprintf(&b, "qdisc add dev %s root handle 1: htb default 9999\n", d)
	fmt.Fprintf(&b, "class add dev %s parent 1: classid 1:1 htb rate 10gbit ceil 10gbit quantum 60000\n", d)
	// unlimited lane for everything that isn't capped
	fmt.Fprintf(&b, "class add dev %s parent 1:1 classid 1:9999 htb rate 9gbit ceil 10gbit prio 0 quantum 60000\n", d)
	fmt.Fprintf(&b, "qdisc add dev %s ingress\n", d)
	for i, ip := range ips {
		id := 10 + i
		mbit := caps[ip]
		burst := int(mbit*1e6/8/10) + 16000 // ~100 ms worth, at least 16 kB
		fmt.Fprintf(&b, "class add dev %s parent 1:1 classid 1:%d htb rate %gmbit ceil %gmbit prio 1 quantum 1514\n", d, id, mbit, mbit)
		fmt.Fprintf(&b, "qdisc add dev %s parent 1:%d handle %d: fq_codel\n", d, id, id)
		fmt.Fprintf(&b, "filter add dev %s parent 1: protocol ip prio 1 u32 match ip dst %s/32 flowid 1:%d\n", d, ip, id)
		fmt.Fprintf(&b, "filter add dev %s parent ffff: protocol ip prio 1 u32 match ip src %s/32 police rate %gmbit burst %d drop flowid :1\n", d, ip, mbit, burst)
	}
	cmd := exec.Command("tc", "-batch", "-")
	cmd.Stdin = &b
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Never leave a half-built shaper behind: remove everything.
		exec.Command("tc", "qdisc", "del", "dev", sh.dev, "root").Run()
		exec.Command("tc", "qdisc", "del", "dev", sh.dev, "ingress").Run()
		sh.err = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		sh.last = ""
		log.Printf("shaper: could not apply caps, removed them: %v", sh.err)
		return
	}
	sh.last, sh.err = plan.String(), nil
	log.Printf("shaper: caps %s", plan.String())
}
