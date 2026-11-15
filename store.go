package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,          -- lowercased MAC, or "ip:<addr>"
  seq INTEGER NOT NULL,         -- short id number, D01...
  ip TEXT NOT NULL,
  mac TEXT,
  name TEXT,
  grp TEXT,
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL,
  last_gateway INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS wire_hour (
  hour INTEGER PRIMARY KEY,     -- unix seconds, truncated to the hour (UTC)
  bytes INTEGER NOT NULL        -- whole packets between the gateway and the internet (wire.go)
);
CREATE TABLE IF NOT EXISTS usage_hour (
  hour INTEGER NOT NULL,        -- unix seconds, truncated to the hour (UTC)
  dev TEXT NOT NULL,
  exit TEXT NOT NULL,
  down INTEGER NOT NULL DEFAULT 0,
  up INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, dev, exit)
);
CREATE TABLE IF NOT EXISTS hosts_day (
  day TEXT NOT NULL,            -- local date YYYY-MM-DD
  dev TEXT NOT NULL,
  host TEXT NOT NULL,
  bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, dev, host)
);
CREATE TABLE IF NOT EXISTS settings (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ord INTEGER NOT NULL,
  body TEXT NOT NULL            -- the Rule as JSON (name, enabled, match, when, then, graph)
);
CREATE TABLE IF NOT EXISTS exemptions (
  rule_id INTEGER NOT NULL,
  device_id TEXT NOT NULL,
  until INTEGER NOT NULL,
  PRIMARY KEY (rule_id, device_id)
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  kind TEXT NOT NULL,
  device_id TEXT,
  rule_id INTEGER,
  text TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_at ON events (at);
CREATE TABLE IF NOT EXISTS bundle_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,           -- "renewal" or "check"
  at INTEGER NOT NULL,          -- unix seconds
  amount INTEGER NOT NULL,      -- bytes: bundle size, or what's left (a correction)
  expires INTEGER               -- unix seconds, renewals only
);
CREATE TABLE IF NOT EXISTS sessions (
  hash TEXT PRIMARY KEY,        -- SHA-256 of the sign-in cookie; the cookie itself is never stored
  expires INTEGER NOT NULL      -- unix seconds
);
`

// migrations add columns to databases created by older versions; errors mean "already there".
var migrations = []string{
	`ALTER TABLE devices ADD COLUMN auto_name TEXT`,
	`ALTER TABLE devices ADD COLUMN auto_source TEXT`,
	`ALTER TABLE devices ADD COLUMN model TEXT`,
	`ALTER TABLE devices ADD COLUMN vpn INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE devices ADD COLUMN paused_until INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE devices ADD COLUMN profile TEXT`,
	`ALTER TABLE devices ADD COLUMN speed_limit REAL NOT NULL DEFAULT 0`,
	`ALTER TABLE devices ADD COLUMN link_to TEXT`,
	`ALTER TABLE bundle_events ADD COLUMN hour_before INTEGER NOT NULL DEFAULT -1`,
}

func openStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; keeps memory small in a 1 GB container
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	for _, m := range migrations {
		db.Exec(m)
	}
	if err := dropWELeftovers(db); err != nil {
		log.Printf("store: removing the old WE counter's data: %v", err)
	}
	// Fold the write-ahead log into the database file at every start: SQLite's
	// automatic checkpoints didn't keep up on the gateway (the log sat at 4 MB and
	// the file kept pages that VACUUM had already rewritten).
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("store: checkpoint: %v", err)
	}
	return &Store{db: db}, nil
}

// dropWELeftovers removes what the WE ISP counter (dropped in v1.7) stored: its
// saved session (login tokens), login state and readings. Deleted rows linger in
// SQLite's free pages, so this overwrites them (secure_delete) and compacts the
// file (VACUUM). It only does work the first time.
func dropWELeftovers(db *sql.DB) error {
	var n int
	db.QueryRow(`SELECT (SELECT count(*) FROM settings WHERE k LIKE 'isp\_%' ESCAPE '\') + (SELECT count(*) FROM sqlite_master WHERE name = 'isp_readings')`).Scan(&n)
	if n == 0 {
		return nil
	}
	ctx := context.Background()
	c, err := db.Conn(ctx) // one connection: the pragma applies per connection
	if err != nil {
		return err
	}
	defer c.Close()
	for _, q := range []string{
		`PRAGMA secure_delete = ON`,
		`DELETE FROM settings WHERE k LIKE 'isp\_%' ESCAPE '\'`,
		`DROP TABLE IF EXISTS isp_readings`,
		`VACUUM`,
	} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	log.Printf("store: removed the old WE counter's saved session and readings")
	return nil
}

// --- devices ---

type devRow struct {
	ID, IP, Mac, Name, Group    string
	AutoName, AutoSource, Model string
	VPN                         bool    // follows the VPN setting (vs. always direct)
	PausedUntil                 int64   // unix seconds; 0 = not paused
	Profile                     string  // "" = follow the global profile
	SpeedLimit                  float64 // Mbit/s; 0 = none
	LinkTo                      string  // another connection of the same device: settings and usage live there
	Seq                         int
	FirstSeen, LastSeen         time.Time
	LastGateway                 time.Time
}

func (s *Store) loadDevices() ([]devRow, error) {
	rows, err := s.db.Query(`SELECT id, seq, ip, COALESCE(mac,''), COALESCE(name,''), COALESCE(grp,''), first_seen, last_seen, last_gateway,
  COALESCE(auto_name,''), COALESCE(auto_source,''), COALESCE(model,''), vpn, paused_until, COALESCE(profile,''), speed_limit, COALESCE(link_to,'') FROM devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []devRow
	for rows.Next() {
		var r devRow
		var fs, ls, lg int64
		if err := rows.Scan(&r.ID, &r.Seq, &r.IP, &r.Mac, &r.Name, &r.Group, &fs, &ls, &lg, &r.AutoName, &r.AutoSource, &r.Model, &r.VPN, &r.PausedUntil, &r.Profile, &r.SpeedLimit, &r.LinkTo); err != nil {
			return nil, err
		}
		r.FirstSeen, r.LastSeen, r.LastGateway = time.Unix(fs, 0), time.Unix(ls, 0), time.Unix(lg, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) upsertDevice(r devRow) error {
	_, err := s.db.Exec(`INSERT INTO devices (id, seq, ip, mac, name, grp, first_seen, last_seen, last_gateway, auto_name, auto_source, model, vpn, paused_until, profile, speed_limit, link_to)
VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''))
ON CONFLICT(id) DO UPDATE SET ip=excluded.ip, mac=excluded.mac, name=excluded.name, grp=excluded.grp,
  last_seen=excluded.last_seen, last_gateway=excluded.last_gateway,
  auto_name=excluded.auto_name, auto_source=excluded.auto_source, model=excluded.model,
  vpn=excluded.vpn, paused_until=excluded.paused_until, profile=excluded.profile, speed_limit=excluded.speed_limit, link_to=excluded.link_to`,
		r.ID, r.Seq, r.IP, r.Mac, r.Name, r.Group, r.FirstSeen.Unix(), r.LastSeen.Unix(), r.LastGateway.Unix(),
		r.AutoName, r.AutoSource, r.Model, r.VPN, r.PausedUntil, r.Profile, r.SpeedLimit, r.LinkTo)
	return err
}

// --- usage ---

type usageKey struct {
	Hour int64
	Dev  string
	Exit string
}

func (s *Store) addUsage(buckets map[usageKey][2]int64) error {
	if len(buckets) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO usage_hour (hour, dev, exit, down, up) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(hour, dev, exit) DO UPDATE SET down = down + excluded.down, up = up + excluded.up`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for k, v := range buckets {
		if _, err := stmt.Exec(k.Hour, k.Dev, k.Exit, v[0], v[1]); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	return tx.Commit()
}

// hosts is keyed by {day, device id, host}.
func (s *Store) addHosts(hosts map[[3]string]int64) error {
	if len(hosts) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO hosts_day (day, dev, host, bytes) VALUES (?, ?, ?, ?)
ON CONFLICT(day, dev, host) DO UPDATE SET bytes = bytes + excluded.bytes`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for k, b := range hosts {
		if _, err := stmt.Exec(k[0], k[1], k[2], b); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	return tx.Commit()
}

// usageRows returns per-hour rows in [from, to), optionally for one device.
func (s *Store) usageRows(from, to time.Time, dev string) ([]struct {
	Hour      int64
	Dev, Exit string
	Down, Up  int64
}, error) {
	q := `SELECT hour, dev, exit, down, up FROM usage_hour WHERE hour >= ? AND hour < ?`
	args := []any{from.Unix(), to.Unix()}
	if dev != "" {
		q += ` AND dev = ?`
		args = append(args, dev)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Hour      int64
		Dev, Exit string
		Down, Up  int64
	}
	for rows.Next() {
		var r struct {
			Hour      int64
			Dev, Exit string
			Down, Up  int64
		}
		if err := rows.Scan(&r.Hour, &r.Dev, &r.Exit, &r.Down, &r.Up); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) topHosts(day, dev string, n int) ([]HostBytes, error) {
	rows, err := s.db.Query(`SELECT host, bytes FROM hosts_day WHERE day = ? AND dev = ? ORDER BY bytes DESC LIMIT ?`, day, dev, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostBytes{}
	for rows.Next() {
		var h HostBytes
		if err := rows.Scan(&h.Host, &h.Bytes); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) addWire(m map[int64]int64) error {
	for h, b := range m {
		if _, err := s.db.Exec(`INSERT INTO wire_hour (hour, bytes) VALUES (?, ?) ON CONFLICT(hour) DO UPDATE SET bytes = bytes + excluded.bytes`, h, b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) wireRows(from, to time.Time) (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT hour, bytes FROM wire_hour WHERE hour >= ? AND hour < ?`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]int64{}
	for rows.Next() {
		var h, b int64
		if err := rows.Scan(&h, &b); err != nil {
			return nil, err
		}
		m[h] = b
	}
	return m, rows.Err()
}

// prune forgets usage older than usageDays and site lists older than sitesDays
// (Settings, Data). The bundle's wire counts stay at least 100 days.
func (s *Store) prune(now time.Time, usageDays, sitesDays int) {
	s.db.Exec(`DELETE FROM usage_hour WHERE hour < ?`, now.AddDate(0, 0, -usageDays).Unix())
	s.db.Exec(`DELETE FROM wire_hour WHERE hour < ?`, now.AddDate(0, 0, -max(usageDays, 100)).Unix())
	s.db.Exec(`DELETE FROM hosts_day WHERE day < ?`, now.AddDate(0, 0, -sitesDays).Format("2006-01-02"))
}

// --- settings ---

func (s *Store) getSetting(k string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT v FROM settings WHERE k = ?`, k).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) setSetting(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO settings (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// --- sign-in sessions ---

func (s *Store) loadSessions(now int64) (map[string]int64, error) {
	s.db.Exec(`DELETE FROM sessions WHERE expires <= ?`, now)
	rows, err := s.db.Query(`SELECT hash, expires FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var h string
		var e int64
		if err := rows.Scan(&h, &e); err != nil {
			return nil, err
		}
		out[h] = e
	}
	return out, rows.Err()
}

func (s *Store) addSession(hash string, expires int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO sessions (hash, expires) VALUES (?, ?)`, hash, expires)
	return err
}

func (s *Store) deleteSession(hash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE hash = ?`, hash)
	return err
}

// clearSessions deletes every session except keep ("" = all).
func (s *Store) clearSessions(keep string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE hash != ?`, keep)
	return err
}

// hasHistory: devices or bundle entries exist (an install in use, not a fresh one).
func (s *Store) hasHistory() bool {
	var n int
	s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM devices) + (SELECT COUNT(*) FROM bundle_events)`).Scan(&n)
	return n > 0
}

// --- bundle ---

type bundleEvent struct {
	ID      int64
	Kind    string
	At      time.Time
	Amount  int64
	Expires *time.Time
	// HourBefore: bytes the gateway had already counted in the entry's clock hour
	// when it was logged (-1 = unknown). Usage is stored per hour, so this keeps
	// a "set what's left" correction from also subtracting traffic from earlier in that hour.
	HourBefore int64
}

func (s *Store) bundleEvents(limit int) ([]bundleEvent, error) {
	rows, err := s.db.Query(`SELECT id, kind, at, amount, expires, hour_before FROM bundle_events ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []bundleEvent
	for rows.Next() {
		var e bundleEvent
		var at int64
		var exp sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Kind, &at, &e.Amount, &exp, &e.HourBefore); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		if exp.Valid {
			t := time.Unix(exp.Int64, 0)
			e.Expires = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) addBundleEvent(kind string, at time.Time, amount int64, expires *time.Time, hourBefore int64) error {
	var exp any
	if expires != nil {
		exp = expires.Unix()
	}
	_, err := s.db.Exec(`INSERT INTO bundle_events (kind, at, amount, expires, hour_before) VALUES (?, ?, ?, ?, ?)`, kind, at.Unix(), amount, exp, hourBefore)
	return err
}

func (s *Store) deleteBundleEvent(id int64) (bool, error) {
	r, err := s.db.Exec(`DELETE FROM bundle_events WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// mergeDevice moves usage and hosts from one device id to another, then deletes it.
func (s *Store) mergeDevice(from, into string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmts := []string{
		`INSERT INTO usage_hour (hour, dev, exit, down, up) SELECT hour, ?, exit, down, up FROM usage_hour WHERE dev = ?
		 ON CONFLICT(hour, dev, exit) DO UPDATE SET down = down + excluded.down, up = up + excluded.up`,
		`INSERT INTO hosts_day (day, dev, host, bytes) SELECT day, ?, host, bytes FROM hosts_day WHERE dev = ?
		 ON CONFLICT(day, dev, host) DO UPDATE SET bytes = bytes + excluded.bytes`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, into, from); err != nil {
			tx.Rollback()
			return err
		}
	}
	for _, q := range []string{`DELETE FROM usage_hour WHERE dev = ?`, `DELETE FROM hosts_day WHERE dev = ?`, `DELETE FROM devices WHERE id = ?`} {
		if _, err := tx.Exec(q, from); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// deleteDevice forgets a device and its history.
func (s *Store) deleteDevice(id string) error {
	for _, q := range []string{`DELETE FROM usage_hour WHERE dev = ?`, `DELETE FROM hosts_day WHERE dev = ?`, `DELETE FROM devices WHERE id = ?`} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// --- rules ---

type ruleRow struct {
	ID   int64
	Ord  int
	Body []byte
}

func (s *Store) loadRules() ([]ruleRow, error) {
	rows, err := s.db.Query(`SELECT id, ord, body FROM rules ORDER BY ord, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ruleRow
	for rows.Next() {
		var r ruleRow
		if err := rows.Scan(&r.ID, &r.Ord, &r.Body); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) insertRule(ord int, body []byte) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO rules (ord, body) VALUES (?, ?)`, ord, body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) updateRule(id int64, ord int, body []byte) error {
	_, err := s.db.Exec(`UPDATE rules SET ord = ?, body = ? WHERE id = ?`, ord, body, id)
	return err
}

func (s *Store) deleteRule(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM exemptions WHERE rule_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id)
	return err
}

func (s *Store) loadExemptions() (map[[2]string]int64, error) {
	rows, err := s.db.Query(`SELECT rule_id, device_id, until FROM exemptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string]int64{}
	for rows.Next() {
		var rid int64
		var dev string
		var until int64
		if err := rows.Scan(&rid, &dev, &until); err != nil {
			return nil, err
		}
		out[[2]string{fmt.Sprint(rid), dev}] = until
	}
	return out, rows.Err()
}

func (s *Store) setExemption(ruleID int64, dev string, until int64) error {
	if until == 0 {
		_, err := s.db.Exec(`DELETE FROM exemptions WHERE rule_id = ? AND device_id = ?`, ruleID, dev)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO exemptions (rule_id, device_id, until) VALUES (?, ?, ?)
ON CONFLICT(rule_id, device_id) DO UPDATE SET until = excluded.until`, ruleID, dev, until)
	return err
}

// --- events ---

func (s *Store) addEvent(at time.Time, kind, dev string, ruleID int64, text string) {
	var d, r any
	if dev != "" {
		d = dev
	}
	if ruleID != 0 {
		r = ruleID
	}
	s.db.Exec(`INSERT INTO events (at, kind, device_id, rule_id, text) VALUES (?, ?, ?, ?, ?)`, at.Unix(), kind, d, r, text)
}

func (s *Store) listEvents(limit int, dev string, before int64) ([]Event, error) {
	q := `SELECT id, at, kind, COALESCE(device_id,''), COALESCE(rule_id,0), text FROM events WHERE 1=1`
	var args []any
	if dev != "" {
		q += ` AND device_id = ?`
		args = append(args, dev)
	}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at int64
		var dev string
		var rid int64
		if err := rows.Scan(&e.ID, &at, &e.Kind, &dev, &rid, &e.Text); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC().Format(time.RFC3339)
		if dev != "" {
			e.DeviceID = &dev
		}
		if rid != 0 {
			e.RuleID = &rid
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
