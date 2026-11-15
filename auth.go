package main

// Optional sign-in (v1.6). Off by default: the app is open to the home
// network, as before. With a password set and access.required on, every /api/
// call except session/login/logout needs a session cookie; the web app's own
// files stay public so it can show the sign-in screen.
//
// The password is kept only as PBKDF2-SHA256 (600k rounds, random salt) and
// sessions only as the SHA-256 of their cookie, so a copy of the database
// signs nobody in.

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie   = "junction_session"
	loginFailLimit  = 5 // wrong passwords per minute per address
	foreverSessions = 10 * 365 * 24 * time.Hour
)

var pbkdf2Iter = 600_000 // a var so tests can go faster

type Auth struct {
	store    *Store
	mu       sync.Mutex
	hash     string           // "" = no password
	req      bool             // sign-in required
	days     int              // session length; 0 = until signed out
	sessions map[string]int64 // sha256(cookie) hex -> expiry (unix)
	fails    map[string][]time.Time
	now      func() time.Time
}

func newAuth(store *Store) *Auth {
	a := &Auth{store: store, sessions: map[string]int64{}, fails: map[string][]time.Time{}, now: time.Now}
	a.reload()
	if ss, err := store.loadSessions(a.now().Unix()); err == nil {
		a.sessions = ss
	}
	return a
}

// reload re-reads the saved access settings.
func (a *Auth) reload() {
	h, _ := a.store.getSetting("auth_hash")
	r, _ := a.store.getSetting("auth_required")
	days := 30
	if v, ok := a.store.getSetting("session_days"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	a.mu.Lock()
	a.hash, a.req, a.days = h, r == "1" && h != "", days
	a.mu.Unlock()
}

func (a *Auth) passwordSet() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hash != ""
}

func (a *Auth) required() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.req
}

func (a *Auth) settings() AccessSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AccessSettings{Required: a.req, PasswordSet: a.hash != "", SessionDays: a.days}
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iter, b64(salt), b64(k)), nil
}

func checkPassword(pw, enc string) bool {
	f := strings.Split(enc, "$")
	if len(f) != 4 || f[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(f[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(f[2])
	want, err2 := base64.RawStdEncoding.DecodeString(f[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func tokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// sessionOf returns the hash of the request's valid session, or "".
func (a *Auth) sessionOf(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	h := []byte(tokenHash(c.Value))
	now := a.now().Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	found := ""
	for k, exp := range a.sessions {
		if subtle.ConstantTimeCompare([]byte(k), h) == 1 && exp > now {
			found = k
		}
	}
	return found
}

func (a *Auth) startSession(w http.ResponseWriter, r *http.Request) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	a.mu.Lock()
	life := time.Duration(a.days) * 24 * time.Hour
	a.mu.Unlock()
	if life <= 0 {
		life = foreverSessions
	}
	exp := a.now().Add(life)
	h := tokenHash(tok)
	if err := a.store.addSession(h, exp.Unix()); err != nil {
		return fmt.Errorf("could not save the session: %w", err)
	}
	a.mu.Lock()
	a.sessions[h] = exp.Unix()
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", MaxAge: int(life.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return nil
}

// endSessions drops every session except keep (a hash, or "").
func (a *Auth) endSessions(keep string) {
	a.mu.Lock()
	for k := range a.sessions {
		if k != keep {
			delete(a.sessions, k)
		}
	}
	a.mu.Unlock()
	a.store.clearSessions(keep)
}

var errTooMany = errors.New("too many attempts")

// attempt counts a login attempt from ip as a failure until it succeeds, so
// parallel guesses can't get past the limit.
func (a *Auth) attempt(ip string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	var recent []time.Time
	for _, t := range a.fails[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= loginFailLimit {
		a.fails[ip] = recent
		return errTooMany
	}
	a.fails[ip] = append(recent, now)
	if len(a.fails) > 1000 { // forget idle addresses
		for k, ts := range a.fails {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Minute {
				delete(a.fails, k)
			}
		}
	}
	return nil
}

func (a *Auth) succeeded(ip string) {
	a.mu.Lock()
	delete(a.fails, ip)
	a.mu.Unlock()
}

// middleware: while sign-in is required, the API needs a session.
func (a *Auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case !strings.HasPrefix(p, "/api/"), p == "/api/session", p == "/api/login", p == "/api/logout":
		case a.required() && a.sessionOf(r) == "":
			fail(w, http.StatusUnauthorized, "Sign in to use Junction.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resetPassword is junctiond -reset-password: forget the password and turn sign-in off.
func resetPassword(store *Store) error {
	if err := store.setSetting("auth_hash", ""); err != nil {
		return err
	}
	if err := store.setSetting("auth_required", "0"); err != nil {
		return err
	}
	return store.clearSessions("")
}

// ---- API ----

type Session struct {
	Required bool `json:"required"`
	SignedIn bool `json:"signedIn"`
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	send(w, Session{Required: a.s.auth.required(), SignedIn: a.s.auth.sessionOf(r) != ""})
}

func readPassword(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Password *string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Password == nil {
		fail(w, 400, `The request body must be JSON like {"password": "..."}.`)
		return "", false
	}
	return *body.Password, true
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	au := a.s.auth
	pw, ok := readPassword(w, r)
	if !ok {
		return
	}
	au.mu.Lock()
	hash := au.hash
	au.mu.Unlock()
	if hash == "" {
		fail(w, 409, "No password is set, so there is nothing to sign in to.")
		return
	}
	ip := clientIP(r)
	if au.attempt(ip) != nil {
		fail(w, 429, "Too many wrong passwords. Wait a minute, then try again.")
		return
	}
	if !checkPassword(pw, hash) {
		log.Printf("auth: wrong password from %s", ip)
		fail(w, 401, "That password isn't right.")
		return
	}
	au.succeeded(ip)
	if err := au.startSession(w, r); err != nil {
		fail(w, 500, "Could not sign in: "+err.Error())
		return
	}
	log.Printf("auth: signed in from %s", ip)
	send(w, Session{Required: au.required(), SignedIn: true})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	au := a.s.auth
	if h := au.sessionOf(r); h != "" {
		au.mu.Lock()
		delete(au.sessions, h)
		au.mu.Unlock()
		au.store.deleteSession(h)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	send(w, Session{Required: au.required(), SignedIn: false})
}

func (a *API) setPassword(w http.ResponseWriter, r *http.Request) {
	pw, ok := readPassword(w, r)
	if !ok {
		return
	}
	if len([]rune(pw)) < 8 {
		fail(w, 400, "The password must be at least 8 characters.")
		return
	}
	if len(pw) > 256 {
		fail(w, 400, "The password can be at most 256 characters.")
		return
	}
	h, err := hashPassword(pw)
	if err != nil {
		fail(w, 500, "Could not set the password: "+err.Error())
		return
	}
	if err := a.s.store.setSetting("auth_hash", h); err != nil {
		fail(w, 500, "Could not save the password: "+err.Error())
		return
	}
	a.s.auth.reload()
	// A new password signs out every other browser.
	a.s.auth.endSessions(a.s.auth.sessionOf(r))
	log.Printf("auth: password changed (from %s)", clientIP(r))
	send(w, a.s.settings())
}
