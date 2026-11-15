package main

// After a device's VPN or pause setting changes, sing-box routes its NEW
// connections the new way within a second, but connections that are already
// open keep their old path until the app reconnects (browsers hold them for
// minutes). closeDeviceConnections makes the change immediate: it waits for
// sing-box to reload the rule-set, then closes that device's open connections
// so apps reconnect straight away through the new path.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

func (s *State) closeDeviceConnections(ips map[string]bool) {
	if len(ips) == 0 {
		return
	}
	go func() {
		time.Sleep(1500 * time.Millisecond) // sing-box reloads the rule-set within ~1 s
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var cc clashConns
		if err := getJSON(ctx, s.cfg.ClashAPI+"/connections", s.secret, &cc); err != nil {
			log.Printf("reconnect: %v", err)
			return
		}
		closed := 0
		for _, c := range cc.Connections {
			if !ips[c.Metadata.SourceIP] {
				continue
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, s.cfg.ClashAPI+"/connections/"+c.ID, nil)
			req.Header.Set("Authorization", "Bearer "+s.secret)
			if resp, err := httpc.Do(req); err == nil {
				resp.Body.Close()
				closed++
			}
		}
		log.Printf("reconnect: closed %d open connection(s) of %s so the change applies now", closed, fmt.Sprint(keys(ips)))
	}()
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
