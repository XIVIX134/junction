package main

// Updates from the app (v1.6), only when config update_url names a GitHub
// releases API address. Junction downloads the release for this machine,
// checks it against SHA256SUMS, unpacks it and hands it to safe-update.sh,
// which installs it, watches the new version and rolls back if it is unwell.
// safe-update.sh writes the outcome to updates.log.

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	updatesLog       = "/var/lib/junction/updates.log"
	updateDir        = "/var/lib/junction/update"
	safeUpdateScript = "/usr/local/lib/junction/safe-update.sh"
	updateHTTP       = &http.Client{Timeout: 100 * time.Second}
)

const updateCheckEvery = 6 * time.Hour

type updateState struct {
	mu        sync.Mutex
	checkedAt time.Time
	checking  bool
	available string // newest release tag, "" = none known
	pending   *HealthUpdate
	run       sync.Mutex // one install at a time
}

var reVersion = regexp.MustCompile(`^v?[0-9][0-9A-Za-z._-]{0,40}$`)

// semver reads "v1.6.0" (also "1.6", "v1.6.0-3-gabc") into numbers.
func semver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newerVersion: a is a later release than b. A build that isn't a release
// ("dev") is never offered one.
func newerVersion(a, b string) bool {
	x, ok1 := semver(a)
	y, ok2 := semver(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func readUpdateHistory(limit int) []HealthUpdate {
	out := []HealthUpdate{}
	b, err := os.ReadFile(updatesLog)
	if err != nil {
		return out
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	decided := map[string]bool{} // safe-update.sh logs "pending", then the outcome: show one line per update
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		f := strings.Fields(lines[i]) // 2026-11-15T10:00:00Z v1.6.0 healthy [note]
		if len(f) < 3 {
			continue
		}
		if _, err := time.Parse(time.RFC3339, f[0]); err != nil {
			continue
		}
		if f[2] == "pending" && decided[f[1]] {
			decided[f[1]] = false
			continue
		}
		decided[f[1]] = f[2] != "pending"
		u := HealthUpdate{At: f[0], Version: f[1], Result: f[2]}
		if len(f) > 3 {
			n := strings.Join(f[3:], " ")
			u.Note = &n
		}
		out = append(out, u)
	}
	return out
}

func (s *State) versionInfo() HealthVersion {
	v := HealthVersion{Current: version, History: readUpdateHistory(10)}
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			t := st.ModTime().UTC().Format(time.RFC3339)
			v.InstalledAt = &t
		}
	}
	u := &s.update
	u.mu.Lock()
	defer u.mu.Unlock()
	if p := u.pending; p != nil {
		done := false // safe-update.sh logged the outcome
		for _, h := range v.History {
			if h.Version == p.Version && h.At >= p.At {
				done = true
			}
		}
		if !done {
			v.History = append([]HealthUpdate{*p}, v.History...)
			if len(v.History) > 10 {
				v.History = v.History[:10]
			}
		}
	}
	if s.updateChecks() {
		if !u.checking && time.Since(u.checkedAt) > updateCheckEvery {
			u.checking = true
			go s.checkForUpdate()
		}
		if newerVersion(u.available, version) {
			a := u.available
			v.Available = &a
		}
	}
	return v
}

func (s *State) checkForUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := fetchRelease(ctx, s.cfg.UpdateURL, "")
	u := &s.update
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checking, u.checkedAt = false, time.Now()
	if err != nil {
		log.Printf("update: checking for a new version: %v", err)
		u.checkedAt = time.Now().Add(-updateCheckEvery + 30*time.Minute) // try again in 30 min
		return
	}
	u.available = rel.Tag
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func getRelease(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "junction/"+version)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s answered HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// fetchRelease finds release tag (""= the newest) at a GitHub releases API
// address: .../releases (a list) or .../releases/latest (one).
func fetchRelease(ctx context.Context, base, tag string) (*ghRelease, error) {
	var raw json.RawMessage
	if err := getRelease(ctx, base, &raw); err != nil {
		return nil, err
	}
	var list []ghRelease
	if json.Unmarshal(raw, &list) != nil {
		var one ghRelease
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("the update address didn't return GitHub releases")
		}
		list = []ghRelease{one}
	}
	for i := range list {
		r := &list[i]
		if r.Draft || r.Tag == "" {
			continue
		}
		if tag == r.Tag || (tag == "" && !r.Prerelease) {
			return r, nil
		}
	}
	if tag != "" && strings.HasSuffix(base, "/releases/latest") {
		var one ghRelease
		if err := getRelease(ctx, strings.TrimSuffix(base, "latest")+"tags/"+url.PathEscape(tag), &one); err != nil {
			return nil, err
		}
		return &one, nil
	}
	if tag == "" {
		return nil, errors.New("no release found")
	}
	return nil, fmt.Errorf("there is no release %s", tag)
}

func releaseArch() string {
	if runtime.GOARCH == "arm" {
		return "armv7"
	}
	return runtime.GOARCH
}

// download saves u to path and returns its SHA-256.
func download(ctx context.Context, u, path string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "junction/"+version)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", errors.New("the file is larger than expected")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sumFor finds a file's hash in a sha256sum listing.
func sumFor(sums, name string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// unpack extracts a release tarball into dest, dropping its top folder
// (junction-<v>-linux-<arch>/). Only plain files and folders, never outside dest.
func unpack(tgz, dest, top string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == top || name == "." {
			continue
		}
		name = strings.TrimPrefix(name, top+"/")
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("the archive has an unsafe path %q", h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > 256<<20 {
				return errors.New("the archive is too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, h.Size))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

// installUpdate downloads, checks and unpacks release v, then starts
// safe-update.sh; done runs when the script ends (not on an error).
func (s *State) installUpdate(ctx context.Context, v string, done func()) error {
	rel, err := fetchRelease(ctx, s.cfg.UpdateURL, v)
	if err != nil {
		return fmt.Errorf("couldn't find version %s: %v", v, err)
	}
	name := fmt.Sprintf("junction-%s-linux-%s", v, releaseArch())
	var tarURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case name + ".tar.gz":
			tarURL = a.URL
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if tarURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s.tar.gz or SHA256SUMS for this machine", v, name)
	}
	if err := os.MkdirAll(updateDir, 0o750); err != nil {
		return err
	}
	tgz := filepath.Join(updateDir, name+".tar.gz")
	defer os.Remove(tgz)
	got, err := download(ctx, tarURL, tgz, 200<<20)
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}
	sumsPath := filepath.Join(updateDir, "SHA256SUMS")
	defer os.Remove(sumsPath)
	if _, err := download(ctx, sumsURL, sumsPath, 1<<20); err != nil {
		return fmt.Errorf("couldn't download the checksums: %v", err)
	}
	sums, _ := os.ReadFile(sumsPath)
	if want := sumFor(string(sums), name+".tar.gz"); want == "" || want != got {
		return errors.New("the download doesn't match its checksum, so nothing was installed")
	}
	dir := filepath.Join(updateDir, v)
	os.RemoveAll(dir)
	if err := unpack(tgz, dir, name); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("couldn't unpack the update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junctiond")); err != nil {
		return errors.New("the update has no junctiond in it")
	}
	logf, err := os.OpenFile(filepath.Join(dir, "update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	// The release's own safe-update.sh when it has one (it may know more than the installed one).
	script := safeUpdateScript
	if p := filepath.Join(dir, "gateway", "safe-update.sh"); fileExists(p) {
		script = p
	}
	cmd := exec.Command("/bin/sh", script, dir)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd) // it restarts Junction: it must outlive this process
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("couldn't start %s: %v", filepath.Base(script), err)
	}
	go func() {
		cmd.Wait()
		logf.Close()
		done()
	}()
	return nil
}

func (a *API) postUpdate(w http.ResponseWriter, r *http.Request) {
	s := a.s
	if s.cfg.UpdateURL == "" {
		fail(w, http.StatusNotImplemented, "Updating from the app isn't set up on this gateway: add update_url (a GitHub releases API address) to /etc/junction/config.json, or install the new version by hand.")
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !reVersion.MatchString(body.Version) {
		fail(w, 400, `The request body must be JSON like {"version": "v1.6.0"}.`)
		return
	}
	if !s.update.run.TryLock() {
		fail(w, 409, "An update is already being installed.")
		return
	}
	log.Printf("update: installing %s (asked from %s)", body.Version, clientIP(r))
	// The lock is held until safe-update.sh ends, so two installs never overlap.
	if err := s.installUpdate(r.Context(), body.Version, s.update.run.Unlock); err != nil {
		s.update.run.Unlock()
		log.Printf("update: %s: %v", body.Version, err)
		fail(w, 502, "Couldn't install "+body.Version+": "+err.Error()+".")
		return
	}
	log.Printf("update: %s handed to safe-update.sh", body.Version)
	s.update.mu.Lock()
	s.update.pending = &HealthUpdate{Version: body.Version, At: time.Now().UTC().Format(time.RFC3339), Result: "pending"}
	s.update.mu.Unlock()
	send(w, s.healthNow(r.Context()))
}
