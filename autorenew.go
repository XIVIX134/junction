package main

// The bundle's monthly renewal (Settings, Bundle): on the chosen day of every
// month, a renewal of the plan's size is logged at midnight, as if it had been
// entered on the Internet page. Skipped when a renewal is already logged near
// that day (entered by hand, a day early or late), and for a day that came
// before the monthly renewal was switched on.

import (
	"log"
	"strconv"
	"time"
)

func (s *State) autoRenewNow() {
	a := s.autoRenew()
	if a == nil {
		return
	}
	now := time.Now().In(s.loc)
	y, m, _ := now.Date()
	at := time.Date(y, m, a.Day, 0, 0, 0, 0, s.loc)
	if now.Before(at) {
		return
	}
	if since, err := strconv.ParseInt(s.setting("bundle_autorenew_since", "0"), 10, 64); err == nil && time.Unix(since, 0).After(at) {
		return
	}
	evs, err := s.store.bundleEvents(50)
	if err != nil {
		return
	}
	for _, e := range evs {
		if e.Kind == "renewal" && e.At.After(at.Add(-20*24*time.Hour)) {
			return
		}
	}
	if err := s.store.addBundleEvent("renewal", at, int64(a.SizeGB*1e9), nil, s.hourBefore(at)); err != nil {
		log.Printf("bundle: logging the monthly renewal: %v", err)
		return
	}
	log.Printf("bundle: logged the monthly renewal of %g GB on %s", a.SizeGB, at.Format("2 Jan"))
}
