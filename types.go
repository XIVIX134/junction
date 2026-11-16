package main

// JSON shapes from API.md.

type Device struct {
	ID          string  `json:"id"`
	Short       string  `json:"short"`
	IP          string  `json:"ip"`
	Mac         *string `json:"mac"`
	Name        string  `json:"name"`
	NameSource  string  `json:"nameSource"`
	AutoName    *string `json:"autoName"`
	Model       *string `json:"model"`
	Group       *string `json:"group"`
	Vendor      *string `json:"vendor"`
	RandomMac   bool    `json:"randomMac"`
	Online      bool    `json:"online"`
	UsesGateway bool    `json:"usesGateway"`
	FirstSeen   string  `json:"firstSeen"`
	LastSeen    string  `json:"lastSeen"`
	RateDown    float64 `json:"rateDown"`
	RateUp      float64 `json:"rateUp"`
	TodayDown   int64   `json:"todayDown"`
	TodayUp     int64   `json:"todayUp"`
	Exit        *string `json:"exit"`
	Connections int     `json:"connections"`
	// Routing controls (they only work while the device uses the gateway).
	VPN            bool         `json:"vpn"`            // follows the VPN setting; false = always direct
	Paused         bool         `json:"paused"`         // internet blocked
	PausedUntil    *string      `json:"pausedUntil"`    // null = until resumed
	Controllable   bool         `json:"controllable"`   // the gateway carries its traffic, so the switches work
	Profile        *string      `json:"profile"`        // manual VPN profile; null = follow the global one
	SpeedLimitMbps *float64     `json:"speedLimitMbps"` // manual cap
	WeekDown       int64        `json:"weekDown"`
	WeekUp         int64        `json:"weekUp"`
	MonthDown      int64        `json:"monthDown"`
	MonthUp        int64        `json:"monthUp"`
	Effective      Effective    `json:"effective"`
	Links          []DeviceLink `json:"links"` // other connections of this same device (e.g. a phone's second Wi-Fi link)
}

type ProfileHealth struct {
	Profile   string   `json:"profile"`
	Known     bool     `json:"known"`
	Healthy   bool     `json:"healthy"`
	LatencyMs *float64 `json:"latencyMs"`
	NextCheck string   `json:"nextCheck"`
}

type SelectorControl struct {
	Mode           string `json:"mode"`
	Profile        string `json:"profile"`
	ManualEndpoint string `json:"manualEndpoint"`
}

type ExitHealth struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Healthy     bool     `json:"healthy"`
	Probed      bool     `json:"probed"`
	LatencyMs   *float64 `json:"latencyMs"`
	LastSuccess *string  `json:"lastSuccess"`
	LastFailure *string  `json:"lastFailure"`
	LastError   *string  `json:"lastError"`
	BytesToday  int64    `json:"bytesToday"`
	// Per SNI profile, when Junction runs failover (empty otherwise).
	Profiles  []ProfileHealth `json:"profiles"`
	NextCheck *string         `json:"nextCheck"` // next probe of the current profile while it is down
}

type Selector struct {
	Selected   string  `json:"selected"`
	Mode       string  `json:"mode"`
	LastSwitch *string `json:"lastSwitch"`
	Reason     *string `json:"reason"`
	// Set when Junction controls the selector; then PUT /api/selector works.
	Control *SelectorControl `json:"control"`
}

type Rate struct {
	Down float64 `json:"down"`
	Up   float64 `json:"up"`
}

type Top struct {
	DeviceID string  `json:"deviceId"`
	Name     string  `json:"name"`
	RateDown float64 `json:"rateDown"`
}

type DirectInfo struct {
	LatencyMs  *float64 `json:"latencyMs"`
	BytesToday int64    `json:"bytesToday"`
}

type Anchor struct {
	Kind   string `json:"kind"`
	At     string `json:"at"`
	Amount int64  `json:"amount"`
}

type Bundle struct {
	RenewedAt        string  `json:"renewedAt"`
	Size             int64   `json:"size"`
	ExpiresAt        *string `json:"expiresAt"`
	Anchor           Anchor  `json:"anchor"`
	UsedSinceRenewal int64   `json:"usedSinceRenewal"`
	Tunnel           int64   `json:"tunnel"`
	Direct           int64   `json:"direct"`
	Left             int64   `json:"left"`
	OverheadPct      float64 `json:"overheadPct"` // packet overhead added to content for hours not counted on the wire
	PacePerDay       int64   `json:"pacePerDay"`
	RunsOut          *string `json:"runsOut"`
	// How your ISP's count compares to Junction's, from the figures you logged (nil: not known yet).
	Calibration *Calibration `json:"calibration"`
}

type BundleEvent struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	At        string  `json:"at"`
	Amount    int64   `json:"amount"`
	ExpiresAt *string `json:"expiresAt"`
	// Checks: what Junction said was left at that moment, counting from the entry before.
	Expected *int64 `json:"expected,omitempty"`
}

type BundleInfo struct {
	Current *Bundle       `json:"current"`
	Events  []BundleEvent `json:"events"`
}

type Counts struct {
	Online     int `json:"online"`
	ViaGateway int `json:"viaGateway"`
	Waiting    int `json:"waiting"`
}

type Overview struct {
	Now      string         `json:"now"`
	Live     Rate           `json:"live"`
	Top      *Top           `json:"top"`
	Selector Selector       `json:"selector"`
	Exits    []ExitHealth   `json:"exits"`
	Direct   DirectInfo     `json:"direct"`
	Bundle   *Bundle        `json:"bundle"`
	Gateway  *GatewayStatus `json:"gateway"`
	Counts   Counts         `json:"counts"`
	Devices  []Device       `json:"devices"`
	// v1.6: so the app needn't hardcode them
	Brand      Brand  `json:"brand"`
	GatewayIP  string `json:"gatewayIP"`
	Timezone   string `json:"timezone"`
	BackupName string `json:"backupName"` // what the backup gateway box is called
	SpeedUnit  string `json:"speedUnit"`  // "bits" or "bytes" (Settings)
	TimeFormat string `json:"timeFormat"` // "24h" or "12h" (Settings)
}

type HourUsage struct {
	Hour   string           `json:"hour"`
	Down   int64            `json:"down"`
	Up     int64            `json:"up"`
	ByExit map[string]int64 `json:"byExit"`
}

type DayUsage struct {
	Day  string `json:"day"`
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
}

type HostBytes struct {
	Host  string `json:"host"`
	Bytes int64  `json:"bytes"`
}

type DeviceDetail struct {
	Device
	Hours    []HourUsage `json:"hours"`
	Days     []DayUsage  `json:"days"`
	TopHosts []HostBytes `json:"topHosts"`
}

type UsageDay struct {
	Day    string           `json:"day"`
	Tunnel int64            `json:"tunnel"`
	Direct int64            `json:"direct"`
	ByExit map[string]int64 `json:"byExit"`
	Billed int64            `json:"billed"` // whole packets, what the ISP bills (wire.go)
}

type DeviceLink struct {
	ID     string  `json:"id"`
	IP     string  `json:"ip"`
	Mac    *string `json:"mac"`
	Name   string  `json:"name"`
	Online bool    `json:"online"`
}
