package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type devState struct {
	devRow
	rateDown, rateUp float64
	exit             string
	conns            int
	dirty            bool
}

type connPrev struct{ up, down int64 }

// State holds everything Junction knows in memory. Usage is accumulated here
// and flushed to SQLite every 30 s.
type State struct {
	recMu      sync.Mutex // serialises recompute (evaluate + apply)
	exHist     exitHistory
	totalBytes int64 // everything counted through the gateway, ever (persisted)
	// Bytes on the wire (wire.go): per hour until flushed, a running total (persisted)
	// and the last raw counter values, so a restart picks up where it left off.
	wirePending map[int64]int64
	wireTotal   int64
	wireLast    string
	ohMu        sync.Mutex // the measured overhead, cached: it sums 7 days of usage
	ohAt        time.Time
	ohF         float64
	flushMu     sync.Mutex // flush and forgetting a device never interleave
	cfg         Config
	store       *Store
	loc         *time.Location
	secret      string
	selfIP      map[string]bool

	mu       sync.RWMutex
	devs     map[string]*devState
	ipToID   map[string]string
	nextSeq  int
	prev     map[string]connPrev
	primed   bool
	liveDown float64
	liveUp   float64
	pending  map[usageKey][2]int64
	hosts    map[[3]string]int64

	selector    clashSelector
	failover    *failoverStatus
	failoverErr error
	clashErr    error
	directMs    *float64
	lastClashOK time.Time

	fo        *Failover
	profiles  map[string][]ProfileHealth
	foMode    SelectorControl
	routerErr error
	merges    [][2]string     // placeholder -> real device, applied to the database at the next flush
	leased    map[string]bool // MACs with a live lease from the gateway's DHCP (told to use .4)

	rules       []Rule
	exempt      map[[2]string]int64 // {rule id, device id} -> until (unix)
	eff         map[string]Effective
	evaluated   bool
	selectors   map[string]string   // sing-box selector -> outbound in use
	periodUsage map[string][3]int64 // device -> {day, week, month} bytes, refreshed by evaluate
	gwLogOffset int64
	shaper      *Shaper
	routerAt    time.Time
	routerDevs  int

	// v1.6
	base         Config       // the config file as read, before settings saved in the app
	gwDetectedAt *time.Time   // when the gateway address was worked out (nil: it is configured)
	exitList     []foEndpoint // VPN exits from the failover config: key + display name
	sbOrder      []string     // sing-box outbound tags in config order (orders the profiles)
	clockWait    atomic.Bool  // waiting for the clock to be set (see run)
	brand        Brand        // cached: the overview sends it every 2 s (mu)
	auth         *Auth
	health       healthState
	restart      func(why string) // exit so the supervisor starts Junction again with new settings
	restartOnce  sync.Once
	update       updateState
}

func newState(cfg Config, store *Store) (*State, error) {
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %w", cfg.TimeZone, err)
	}
	secret, err := clashSecret(cfg.SingBoxConfig)
	if err != nil {
		log.Printf("could not read Clash API secret: %v", err)
	}
	s := &State{
		cfg: cfg, base: cfg, store: store, loc: loc, secret: secret,
		exitList: readExitList(cfg.FailoverConfig), sbOrder: singboxOrder(cfg.SingBoxConfig),
		restart:     func(string) {},
		selfIP:      map[string]bool{"127.0.0.1": true},
		devs:        map[string]*devState{},
		wirePending: map[int64]int64{},
		ipToID:      map[string]string{},
		selectors:   map[string]string{},
		eff:         map[string]Effective{},
		exempt:      map[[2]string]int64{},
		shaper:      &Shaper{dev: cfg.Interface},
		prev:        map[string]connPrev{},
		pending:     map[usageKey][2]int64{},
		hosts:       map[[3]string]int64{},
	}
	s.refreshSelf()
	rows, err := store.loadDevices()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.devs[r.ID] = &devState{devRow: r}
		s.ipToID[r.IP] = r.ID
		if r.Seq >= s.nextSeq {
			s.nextSeq = r.Seq + 1
		}
	}
	if s.nextSeq == 0 {
		s.nextSeq = 1
	}
	s.brand = s.loadBrand()
	s.auth = newAuth(store)
	s.markExistingSetup()
	return s, nil
}

// device returns (creating if needed) the device for an id. Caller holds mu.
func (s *State) device(id, ip, mac string, now time.Time) *devState {
	d, ok := s.devs[id]
	if !ok {
		d = &devState{devRow: devRow{ID: id, Seq: s.nextSeq, IP: ip, Mac: mac, FirstSeen: now, LastSeen: now}}
		if mac != "" {
			d.Name = s.cfg.SeedNames[mac]
			d.Group = s.cfg.SeedGroups[mac]
		}
		s.nextSeq++
		s.devs[id] = d
		d.dirty = true
		if s.evaluated && !strings.HasPrefix(id, "ip:") {
			text := "New device on the network: " + ip
			if v := vendorOf(mac); v != "" {
				text += " (" + v + ")"
			} else if mac != "" && randomMAC(mac) {
				text += " (private MAC)"
			}
			go s.store.addEvent(now, "device", id, 0, text)
		}
	}
	if d.IP != ip {
		d.IP, d.dirty = ip, true
	}
	return d
}

func (s *State) idForIP(ip string) string {
	if id, ok := s.ipToID[ip]; ok {
		return id
	}
	return "ip:" + ip
}

// ---------- loops ----------

// run starts the loops. After a power cut the host can boot with a wrong clock
// (dead RTC battery: it comes up as 13 April) and no internet to fix it. Junction
// then waits until the clock is past its own last sign of life, so usage isn't
// filed under the wrong day and schedules don't fire at the wrong time. Traffic
// is unaffected: sing-box keeps routing with the rule-sets already on disk.
func (s *State) run(ctx context.Context) {
	floor := s.clockFloor()
	if !time.Now().Before(floor) {
		s.startLoops(ctx)
		return
	}
	s.clockWait.Store(true)
	log.Printf("clock: system time %s is before Junction's last activity %s; waiting for the time to be set (traffic keeps flowing)",
		time.Now().UTC().Format(time.RFC3339), floor.UTC().Format(time.RFC3339))
	go func() {
		for time.Now().Before(floor) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		log.Printf("clock: time is set (%s), starting", time.Now().UTC().Format(time.RFC3339))
		s.clockWait.Store(false)
		s.startLoops(ctx)
	}()
}

// clockFloor is the last time Junction was known to be running, minus a margin.
func (s *State) clockFloor() time.Time {
	v, ok := s.store.getSetting("last_alive")
	if !ok {
		return time.Time{}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0).Add(-2 * time.Minute)
}

func (s *State) startLoops(ctx context.Context) {
	if v, ok := s.store.getSetting("gateway_total_bytes"); ok {
		n, _ := strconv.ParseInt(v, 10, 64)
		s.mu.Lock()
		s.totalBytes = n
		s.mu.Unlock()
	}
	if v, ok := s.store.getSetting("wire_total_bytes"); ok {
		n, _ := strconv.ParseInt(v, 10, 64)
		s.mu.Lock()
		s.wireTotal = n
		s.mu.Unlock()
	}
	go s.wireLoop(ctx)
	go s.every(ctx, time.Minute, func(context.Context) {
		s.store.setSetting("last_alive", strconv.FormatInt(time.Now().Unix(), 10))
	})
	go s.every(ctx, time.Second, s.pollConnections)
	go s.every(ctx, 15*time.Second, s.pollARP)
	go s.every(ctx, 3*time.Minute, func(context.Context) { sweep(s.cfg.LAN) })
	go s.every(ctx, 5*time.Second, s.pollStatus)
	go s.every(ctx, 30*time.Second, func(context.Context) { s.pollDirect() })
	go s.every(ctx, time.Minute, s.sampleExits)
	go s.every(ctx, 30*time.Second, func(context.Context) { s.flush() })
	go s.every(ctx, 6*time.Hour, func(context.Context) { s.store.prune(time.Now()) })
	if s.cfg.DHCPListen == nil || *s.cfg.DHCPListen {
		go s.listenDHCP(ctx)
	}
	go s.every(ctx, 20*time.Second, s.pollLeases)
	s.importPolicy()
	if err := s.loadRules(); err != nil {
		log.Printf("rules: %v", err)
	}
	// Re-evaluate regularly: schedules start and end, usage grows, pauses expire.
	go s.every(ctx, 15*time.Second, func(context.Context) { s.recompute(); s.pollGatewayLog() })
	go func() {
		time.Sleep(20 * time.Second) // let the first sweep fill the device list
		go s.every(ctx, 10*time.Minute, s.probeMDNS)
		go s.every(ctx, 15*time.Minute, s.probeSSDP)
	}()
}

func (s *State) logf(format string, args ...any) { log.Printf(format, args...) }

func (s *State) every(ctx context.Context, d time.Duration, f func(context.Context)) {
	f(ctx)
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f(ctx)
		}
	}
}

func (s *State) pollConnections(ctx context.Context) {
	var cc clashConns
	err := getJSON(ctx, s.cfg.ClashAPI+"/connections", s.secret, &cc)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clashErr = err
	if err != nil {
		return
	}
	s.lastClashOK = now

	hour := now.Truncate(time.Hour).Unix()
	day := now.In(s.loc).Format("2006-01-02")
	next := make(map[string]connPrev, len(cc.Connections))
	type agg struct {
		down, up int64
		conns    int
		exit     string
		best     int64
		routed   bool // routed through the gateway (TPROXY), not just using its proxy port
	}
	per := map[string]*agg{}

	for _, c := range cc.Connections {
		next[c.ID] = connPrev{up: c.Upload, down: c.Download}
		m := c.Metadata
		if strings.HasPrefix(m.Type, "mixed/probe-") || s.selfIP[m.SourceIP] || m.SourceIP == "" || isPrivate(m.DestinationIP) {
			continue
		}
		exit := "direct"
		if len(c.Chains) > 0 {
			exit = c.Chains[0]
		}
		p := s.prev[c.ID]
		dDown, dUp := c.Download-p.down, c.Upload-p.up
		if !s.primed || dDown < 0 || dUp < 0 {
			dDown, dUp = 0, 0 // first poll after start: don't recount bytes from before we were watching
		}
		a := per[m.SourceIP]
		if a == nil {
			a = &agg{}
			per[m.SourceIP] = a
		}
		a.conns++
		a.down += dDown
		a.up += dUp
		if strings.HasPrefix(m.Type, "tproxy/") {
			a.routed = true
		}
		if dDown+dUp >= a.best {
			a.best, a.exit = dDown+dUp, exit
		}
		if dDown+dUp == 0 {
			continue
		}
		id := s.idForIP(m.SourceIP)
		k := usageKey{Hour: hour, Dev: id, Exit: exit}
		v := s.pending[k]
		s.pending[k] = [2]int64{v[0] + dDown, v[1] + dUp}
		s.totalBytes += dDown + dUp
		host := m.Host
		if host == "" {
			host = m.DestinationIP
		}
		s.hosts[[3]string{day, id, host}] += dDown + dUp
	}
	s.prev, s.primed = next, true

	const alpha = 0.4 // ~3 s smoothing at 1 s polls
	var totDown, totUp int64
	seen := map[string]bool{}
	for ip, a := range per {
		id := s.idForIP(ip)
		mac := ""
		if !strings.HasPrefix(id, "ip:") {
			mac = id
		}
		d := s.device(id, ip, mac, now)
		seen[id] = true
		d.rateDown = alpha*float64(a.down) + (1-alpha)*d.rateDown
		d.rateUp = alpha*float64(a.up) + (1-alpha)*d.rateUp
		d.conns = a.conns
		d.exit = a.exit
		d.LastSeen = now
		if a.routed {
			d.LastGateway = now // only real routing counts: proxy use doesn't make the switches work
		}
		totDown += a.down
		totUp += a.up
	}
	for id, d := range s.devs {
		if !seen[id] {
			d.rateDown *= 1 - alpha
			d.rateUp *= 1 - alpha
			d.conns = 0
		}
	}
	s.liveDown = alpha*float64(totDown) + (1-alpha)*s.liveDown
	s.liveUp = alpha*float64(totUp) + (1-alpha)*s.liveUp
}

// refreshSelf re-reads our own addresses: the floating gateway address comes
// and goes with keepalived. Caller must not hold mu.
func (s *State) refreshSelf() {
	self := map[string]bool{"127.0.0.1": true}
	if s.cfg.GatewayIP != "" {
		self[s.cfg.GatewayIP] = true // the floating address, also while the backup holds it
	}
	if ifc, err := net.InterfaceByName(s.cfg.Interface); err == nil {
		if addrs, err := ifc.Addrs(); err == nil {
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok {
					self[ipn.IP.String()] = true
				}
			}
		}
	}
	s.mu.Lock()
	s.selfIP = self
	s.mu.Unlock()
}

func (s *State) pollARP(context.Context) {
	s.refreshSelf()
	ents, err := readARP(s.cfg.Interface)
	if err != nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range ents {
		if s.selfIP[e.IP] {
			continue
		}
		s.ipToID[e.IP] = e.MAC
		d := s.device(e.MAC, e.IP, e.MAC, now)
		if ph, ok := s.devs["ip:"+e.IP]; ok {
			// Traffic arrived before we knew this address's MAC: fold the placeholder in,
			// keeping any settings made on it (e.g. VPN on for a known address).
			if !d.VPN && ph.VPN {
				d.VPN = true
			}
			if d.PausedUntil < ph.PausedUntil {
				d.PausedUntil = ph.PausedUntil
			}
			if d.Name == "" {
				d.Name = ph.Name
			}
			if d.Group == "" {
				d.Group = ph.Group
			}
			d.dirty = true
			delete(s.devs, "ip:"+e.IP)
			s.merges = append(s.merges, [2]string{"ip:" + e.IP, e.MAC})
		}
		d.LastSeen = now
		d.dirty = true
	}
}

func (s *State) pollStatus(ctx context.Context) {
	var all struct {
		Proxies map[string]struct {
			Type string   `json:"type"`
			Now  string   `json:"now"`
			All  []string `json:"all"`
		} `json:"proxies"`
	}
	var sel clashSelector
	selErr := getJSON(ctx, s.cfg.ClashAPI+"/proxies", s.secret, &all)
	if selErr == nil {
		sels := map[string]string{}
		for name, p := range all.Proxies {
			if p.Type == "Selector" {
				sels[name] = p.Now
			}
		}
		if v, ok := all.Proxies["vpn-active"]; ok {
			sel = clashSelector{Now: v.Now, All: v.All}
		}
		s.mu.Lock()
		s.selectors = sels
		s.mu.Unlock()
		setProfiles(sels, s.sbOrder)
	}
	if s.fo != nil { // we are the failover; nothing to poll
		s.mu.Lock()
		if selErr == nil {
			s.selector = sel
		}
		s.mu.Unlock()
		return
	}
	var fo failoverStatus
	foErr := getJSON(ctx, s.cfg.FailoverAPI, "", &fo)
	s.mu.Lock()
	defer s.mu.Unlock()
	if selErr == nil {
		s.selector = sel
	}
	s.failoverErr = foErr
	if foErr == nil {
		s.failover = &fo
	}
}

func (s *State) pollDirect() {
	d, ok := directLatency()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		ms := float64(d.Microseconds()) / 1000
		s.directMs = &ms
	} else {
		s.directMs = nil
	}
}

func (s *State) flush() {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	total := s.totalBytes
	wire, wireTotal, wireLast := s.wirePending, s.wireTotal, s.wireLast
	s.wirePending = map[int64]int64{}
	pending, hosts := s.pending, s.hosts
	s.pending, s.hosts = map[usageKey][2]int64{}, map[[3]string]int64{}
	merges := s.merges
	s.merges = nil
	var dirty []devRow
	for _, d := range s.devs {
		if d.dirty {
			dirty = append(dirty, d.devRow)
			d.dirty = false
		}
	}
	s.mu.Unlock()

	s.store.setSetting("gateway_total_bytes", strconv.FormatInt(total, 10))
	if err := s.store.addWire(wire); err != nil {
		log.Printf("flush wire: %v", err)
		s.mu.Lock()
		for h, b := range wire {
			s.wirePending[h] += b
		}
		s.mu.Unlock()
	} else if wireLast != "" {
		s.store.setSetting("wire_total_bytes", strconv.FormatInt(wireTotal, 10))
		s.store.setSetting("wire_counters", wireLast)
	}
	if err := s.store.addUsage(pending); err != nil {
		log.Printf("flush usage: %v", err)
		s.mu.Lock() // put it back so nothing is lost
		for k, v := range pending {
			o := s.pending[k]
			s.pending[k] = [2]int64{o[0] + v[0], o[1] + v[1]}
		}
		s.mu.Unlock()
	}
	if err := s.store.addHosts(hosts); err != nil {
		log.Printf("flush hosts: %v", err)
	}
	for _, r := range dirty {
		if err := s.store.upsertDevice(r); err != nil {
			log.Printf("save device %s: %v", r.ID, err)
		}
	}
	for _, m := range merges {
		if err := s.store.mergeDevice(m[0], m[1]); err != nil {
			log.Printf("merge %s into %s: %v", m[0], m[1], err)
		}
	}
}

// ---------- read models ----------

type usageSums struct {
	down, up int64
	byExit   map[string]int64
}

// sumUsage adds stored rows and not-yet-flushed buckets in [from, to). Caller holds no lock.
func (s *State) sumUsage(from, to time.Time, dev string, each func(hour int64, dev, exit string, down, up int64)) error {
	rows, err := s.store.usageRows(from, to, dev)
	if err != nil {
		return err
	}
	for _, r := range rows {
		each(r.Hour, r.Dev, r.Exit, r.Down, r.Up)
	}
	s.mu.RLock()
	for k, v := range s.pending {
		if k.Hour >= from.Truncate(time.Hour).Unix() && k.Hour < to.Unix() && (dev == "" || k.Dev == dev) {
			each(k.Hour, k.Dev, k.Exit, v[0], v[1])
		}
	}
	s.mu.RUnlock()
	return nil
}

func (s *State) dayStart(t time.Time) time.Time {
	y, m, d := t.In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.loc)
}

func (s *State) deviceView(d *devState, now time.Time, today map[string][2]int64) Device {
	var mac *string
	if d.Mac != "" {
		m := d.Mac
		mac = &m
	}
	name, source := d.Name, "user"
	octet := d.IP[strings.LastIndex(d.IP, ".")+1:]
	switch {
	case name != "":
	case d.AutoName != "":
		name, source = d.AutoName, d.AutoSource
	case vendorOf(d.Mac) != "":
		name, source = vendorOf(d.Mac)+" ."+octet, "vendor"
	default:
		name, source = "Device ."+octet, "ip"
	}
	var grp, vendor, exit *string
	if d.Group != "" {
		g := d.Group
		grp = &g
	}
	if v := vendorOf(d.Mac); v != "" {
		vendor = &v
	}
	if d.exit != "" {
		e := d.exit
		exit = &e
	}
	t := today[d.ID]
	return Device{
		ID: d.ID, Short: fmt.Sprintf("D%02d", d.Seq), IP: d.IP, Mac: mac, Name: name, Group: grp, Vendor: vendor,
		NameSource: source, AutoName: strPtr(d.AutoName), Model: strPtr(d.Model),
		VPN: d.VPN, Paused: d.PausedUntil > now.Unix(), PausedUntil: pausedUntil(d.PausedUntil, now),
		Controllable: s.leased[d.Mac] || (d.LastGateway.Unix() > 0 && now.Sub(d.LastGateway) < 10*time.Minute),
		Profile:      strPtr(d.Profile),
		SpeedLimitMbps: func() *float64 {
			if d.SpeedLimit > 0 {
				v := d.SpeedLimit
				return &v
			}
			return nil
		}(),
		WeekDown: s.periodUsage[d.ID][1], MonthDown: s.periodUsage[d.ID][2], // down+up totals (per-direction split isn't kept per period)
		Effective: func() Effective {
			if e, ok := s.eff[d.ID]; ok {
				return e
			}
			return Effective{Rules: []RuleRef{}, Exemptions: []Exemption{}}
		}(),
		RandomMac:   d.Mac != "" && randomMAC(d.Mac),
		Online:      now.Sub(d.LastSeen) < 5*time.Minute,
		UsesGateway: s.leased[d.Mac] || (d.LastGateway.Unix() > 0 && now.Sub(d.LastGateway) < 10*time.Minute),
		FirstSeen:   d.FirstSeen.UTC().Format(time.RFC3339), LastSeen: d.LastSeen.UTC().Format(time.RFC3339),
		RateDown: round1(d.rateDown), RateUp: round1(d.rateUp),
		TodayDown: t[0], TodayUp: t[1], Exit: exit, Connections: d.conns,
	}
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func (s *State) devices(now time.Time) ([]Device, map[string]int64, error) {
	today := map[string][2]int64{}
	exitToday := map[string]int64{}
	err := s.sumUsage(s.dayStart(now), now.Add(time.Hour), "", func(_ int64, dev, exit string, down, up int64) {
		v := today[dev]
		today[dev] = [2]int64{v[0] + down, v[1] + up}
		exitToday[exit] += down + up
	})
	if err != nil {
		return nil, nil, err
	}
	s.mu.RLock()
	out := make([]Device, 0, len(s.devs))
	idx := map[string]int{}
	var secondaries []*devState
	for _, d := range s.devs {
		if s.linkTarget(d) != "" {
			secondaries = append(secondaries, d)
			continue
		}
		idx[d.ID] = len(out)
		v := s.deviceView(d, now, today)
		v.Links = []DeviceLink{}
		out = append(out, v)
	}
	for _, d := range secondaries {
		i, ok := idx[s.linkTarget(d)]
		if !ok {
			continue
		}
		sv := s.deviceView(d, now, today)
		p := &out[i]
		p.Links = append(p.Links, DeviceLink{ID: sv.ID, IP: sv.IP, Mac: sv.Mac, Name: sv.Name, Online: sv.Online})
		p.RateDown, p.RateUp = round1(p.RateDown+sv.RateDown), round1(p.RateUp+sv.RateUp)
		p.TodayDown, p.TodayUp = p.TodayDown+sv.TodayDown, p.TodayUp+sv.TodayUp
		p.WeekDown, p.MonthDown = p.WeekDown+sv.WeekDown, p.MonthDown+sv.MonthDown
		p.Connections += sv.Connections
		p.Online = p.Online || sv.Online
		p.UsesGateway = p.UsesGateway || sv.UsesGateway
		p.Controllable = p.Controllable || sv.Controllable
	}
	s.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		if out[i].RateDown != out[j].RateDown {
			return out[i].RateDown > out[j].RateDown
		}
		if out[i].TodayDown != out[j].TodayDown {
			return out[i].TodayDown > out[j].TodayDown
		}
		return out[i].Short < out[j].Short
	})
	return out, exitToday, nil
}

func (s *State) exitsView(exitToday map[string]int64) ([]ExitHealth, Selector) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sel := Selector{Selected: s.selector.Now}
	byEndpoint := map[string]int64{}
	for exit, b := range exitToday {
		if i := strings.IndexByte(exit, '-'); i > 0 {
			byEndpoint[exit[:i]] += b
		}
	}
	var out []ExitHealth
	if fo := s.failover; fo != nil {
		sel.Mode = fo.State.Mode
		sel.LastSwitch = strPtr(fo.State.LastSwitch)
		sel.Reason = strPtr(fo.State.LastReason)
		if s.fo != nil {
			c := s.foMode
			sel.Control = &c
		}
		probed := map[string]string{}
		for _, e := range fo.Endpoints {
			probed[e.Key] = e.Name
		}
		keys := map[string]bool{}
		for k := range fo.State.Health {
			keys[k] = true
		}
		for k := range probed {
			keys[k] = true
		}
		for _, e := range s.exitList { // configured exits show even while nothing probes them
			keys[e.Key] = true
		}
		for k := range keys {
			h := fo.State.Health[k]
			name := probed[k]
			if name == "" {
				name = s.exitName(k)
			}
			_, isProbed := probed[k]
			if s.fo != nil && s.fo.endpoint(k) != nil {
				isProbed = true // Junction probes every endpoint, backing off while one is down
			}
			eh := ExitHealth{
				Key: k, Name: name, Healthy: isProbed && h.Known && h.Healthy, Probed: isProbed,
				LastSuccess: timePtr(h.LastSuccess), LastFailure: timePtr(h.LastFailure), LastError: strPtr(h.LastError),
				BytesToday: byEndpoint[k],
				Profiles:   s.profiles[k],
			}
			if !eh.Healthy && s.fo != nil {
				for _, p := range s.profiles[k] {
					if p.Profile == s.foMode.Profile {
						nc := p.NextCheck
						eh.NextCheck = &nc
					}
				}
			}
			if isProbed && h.Known && h.Healthy && h.LatencyMs > 0 {
				ms := float64(h.LatencyMs)
				eh.LatencyMs = &ms
			}
			out = append(out, eh)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Healthy != out[j].Healthy {
			return out[i].Healthy
		}
		return out[i].Key < out[j].Key
	})
	return out, sel
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timePtr drops Go's zero time ("0001-01-01...") which failoverd emits for "never".
func timePtr(s string) *string {
	if s == "" || strings.HasPrefix(s, "0001-") {
		return nil
	}
	return &s
}

func (s *State) overview(now time.Time) (Overview, error) {
	devs, exitToday, err := s.devices(now)
	if err != nil {
		return Overview{}, err
	}
	exits, sel := s.exitsView(exitToday)

	bundle, err := s.bundle(now)
	if err != nil {
		return Overview{}, err
	}

	s.mu.RLock()
	ov := Overview{
		Now:        now.UTC().Format(time.RFC3339),
		Live:       Rate{Down: round1(s.liveDown), Up: round1(s.liveUp)},
		Selector:   sel,
		Exits:      exits,
		Direct:     DirectInfo{LatencyMs: s.directMs, BytesToday: exitToday["direct"]},
		Bundle:     bundle,
		Gateway:    readGatewayStatus(),
		Devices:    devs,
		Brand:      brandNoImage(s.brand), // the image is up to 300 KB: the app gets it from /api/settings, not every 2 s
		GatewayIP:  s.cfg.GatewayIP,
		Timezone:   s.cfg.TimeZone,
		BackupName: s.cfg.BackupName,
	}
	s.mu.RUnlock()
	for _, d := range devs {
		if d.Online {
			ov.Counts.Online++
		}
		if d.UsesGateway {
			ov.Counts.ViaGateway++
		}
		if ov.Top == nil || d.RateDown > ov.Top.RateDown {
			if d.RateDown > 0 {
				ov.Top = &Top{DeviceID: d.ID, Name: d.Name, RateDown: d.RateDown}
			}
		}
	}
	return ov, nil
}
