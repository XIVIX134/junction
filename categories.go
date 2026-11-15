package main

// What the network is used for: every site name Junction saw (from DNS and TLS)
// is put in a category by its domain, using the built-in list below. Traffic
// to a bare IP address (no name seen) is its own category: no guessing.
//
// GET /api/categories?days=7[&device=id] -> the categories over the last days
// (today counts as a day), with the same-length period before for comparison,
// each with its top sites and devices.
// GET /api/devices/daily?days=14 -> every device's bytes per day.

import (
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type category struct{ Key, Name string }

var categoryList = []category{
	{"video", "Video streaming"},
	{"social", "Social media"},
	{"messaging", "Messaging & calls"},
	{"ai", "AI & developer tools"},
	{"gaming", "Gaming"},
	{"music", "Music & podcasts"},
	{"updates", "Updates & downloads"},
	{"cloud", "Cloud & backup"},
	{"speedtest", "Speed tests"},
	{"web", "Browsing & other"},
	{"unnamed", "No site name"},
}

// categoryDomains: domain (and its subdomains) -> category. First match from
// the most specific suffix wins.
var categoryDomains = map[string]string{
	// video
	"googlevideo.com": "video", "youtube.com": "video", "ytimg.com": "video", "youtu.be": "video", "youtube-nocookie.com": "video",
	"nflxvideo.net": "video", "netflix.com": "video", "nflximg.net": "video", "nflxext.com": "video",
	"pv-cdn.net": "video", "aiv-cdn.net": "video", "primevideo.com": "video", "media-amazon.com": "video",
	"disneyplus.com": "video", "dssott.com": "video", "bamgrid.com": "video", "hbomax.com": "video", "max.com": "video",
	"shahid.net": "video", "mbc.net": "video", "osn.com": "video", "watchit.com": "video", "vimeo.com": "video", "vimeocdn.com": "video",
	"twitch.tv": "video", "ttvnw.net": "video", "jtvnw.net": "video", "dailymotion.com": "video", "phncdn.com": "video",
	"crunchyroll.com": "video", "vdocipher.com": "video", "kick.com": "video",
	// social
	"fbcdn.net": "social", "facebook.com": "social", "fb.com": "social", "instagram.com": "social", "cdninstagram.com": "social",
	"twimg.com": "social", "x.com": "social", "twitter.com": "social", "t.co": "social",
	"tiktokcdn.com": "social", "tiktokv.com": "social", "tiktok.com": "social", "ibyteimg.com": "social", "byteoversea.com": "social",
	"tiktokcdn-us.com": "social", "muscdn.com": "social", "snapchat.com": "social", "sc-cdn.net": "social", "snapkit.com": "social",
	"reddit.com": "social", "redd.it": "social", "redditmedia.com": "social", "redditstatic.com": "social",
	"pinterest.com": "social", "pinimg.com": "social", "linkedin.com": "social", "licdn.com": "social", "threads.net": "social",
	// messaging & calls
	"whatsapp.net": "messaging", "whatsapp.com": "messaging", "telegram.org": "messaging", "t.me": "messaging", "tdesktop.com": "messaging",
	"discord.com": "messaging", "discordapp.com": "messaging", "discordapp.net": "messaging", "discord.gg": "messaging", "discord.media": "messaging",
	"signal.org": "messaging", "zoom.us": "messaging", "zoom.com": "messaging", "messenger.com": "messaging", "viber.com": "messaging",
	"skype.com": "messaging", "teams.microsoft.com": "messaging", "slack.com": "messaging", "slack-edge.com": "messaging",
	// AI & developer tools
	"anthropic.com": "ai", "claude.ai": "ai", "openai.com": "ai", "chatgpt.com": "ai", "oaiusercontent.com": "ai",
	"gemini.google.com": "ai", "perplexity.ai": "ai", "mistral.ai": "ai", "huggingface.co": "ai", "hf.co": "ai",
	"opencode.ai": "ai", "near.ai": "ai", "cursor.sh": "ai", "cursor.com": "ai", "x.ai": "ai", "grok.com": "ai",
	"github.com": "ai", "githubusercontent.com": "ai", "githubassets.com": "ai", "gitlab.com": "ai",
	"npmjs.org": "ai", "npmjs.com": "ai", "pypi.org": "ai", "pythonhosted.org": "ai", "docker.io": "ai", "docker.com": "ai",
	"golang.org": "ai", "go.dev": "ai", "crates.io": "ai", "stackoverflow.com": "ai",
	// gaming
	"xboxlive.com": "gaming", "playstation.net": "gaming", "playstation.com": "gaming", "sonyentertainmentnetwork.com": "gaming",
	"steampowered.com": "gaming", "steamcontent.com": "gaming", "steamserver.net": "gaming", "steamstatic.com": "gaming",
	"epicgames.com": "gaming", "unrealengine.com": "gaming", "rockstargames.com": "gaming", "activision.com": "gaming",
	"callofduty.com": "gaming", "riotgames.com": "gaming", "riotcdn.net": "gaming", "ea.com": "gaming", "blizzard.com": "gaming",
	"battle.net": "gaming", "nintendo.net": "gaming", "nintendo.com": "gaming", "roblox.com": "gaming", "rbxcdn.com": "gaming",
	"minecraft.net": "gaming", "mojang.com": "gaming", "ubi.com": "gaming", "ubisoft.com": "gaming", "pubg.com": "gaming",
	"gamedownloads-rockstargames-com.akamaized.net": "gaming",
	// music & podcasts
	"spotify.com": "music", "spotifycdn.com": "music", "scdn.co": "music", "anghami.com": "music", "anghcdn.co": "music",
	"deezer.com": "music", "dzcdn.net": "music", "soundcloud.com": "music", "sndcdn.com": "music", "music.apple.com": "music",
	// updates & downloads
	"windowsupdate.com": "updates", "delivery.mp.microsoft.com": "updates", "update.microsoft.com": "updates",
	"swcdn.apple.com": "updates", "swdist.apple.com": "updates", "mesu.apple.com": "updates", "updates.cdn-apple.com": "updates",
	"dell.com": "updates", "hp.com": "updates", "lenovo.com": "updates", "ubuntu.com": "updates", "debian.org": "updates",
	"alpinelinux.org": "updates", "archlinux.org": "updates", "fedoraproject.org": "updates", "raspberrypi.com": "updates",
	"dl.google.com": "updates", "gvt1.com": "updates", "mi-img.com": "updates", "samsungcloudsolution.com": "updates",
	// cloud & backup
	"googleapis.com": "cloud", "googleusercontent.com": "cloud", "icloud.com": "cloud", "icloud-content.com": "cloud",
	"dropbox.com": "cloud", "dropboxusercontent.com": "cloud", "onedrive.com": "cloud", "1drv.com": "cloud", "sharepoint.com": "cloud",
	"backblazeb2.com": "cloud", "amazonaws.com": "cloud", "mega.nz": "cloud", "mega.co.nz": "cloud",
	// speed tests
	"ooklaserver.net": "speedtest", "speedtest.net": "speedtest", "fast.com": "speedtest", "proof.ovh.net": "speedtest",
}

// categoryOf returns the category key of a site name.
func categoryOf(host string) string {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" || net.ParseIP(strings.Trim(h, "[]")) != nil {
		return "unnamed"
	}
	for {
		if c, ok := categoryDomains[h]; ok {
			return c
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			return "web"
		}
		h = h[i+1:]
	}
}

func categoryName(key string) string {
	for _, c := range categoryList {
		if c.Key == key {
			return c.Name
		}
	}
	return key
}

type CategorySite struct {
	Host  string `json:"host"`
	Bytes int64  `json:"bytes"`
}
type CategoryDevice struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}
type CategoryUsage struct {
	Key     string           `json:"key"`
	Name    string           `json:"name"`
	Bytes   int64            `json:"bytes"`
	Prev    int64            `json:"prev"` // the period before, same length
	Sites   []CategorySite   `json:"sites"`
	Devices []CategoryDevice `json:"devices"`
}
type CategoriesView struct {
	From       string          `json:"from"` // first day, YYYY-MM-DD (local)
	To         string          `json:"to"`
	Days       int             `json:"days"`
	Total      int64           `json:"total"`
	PrevTotal  int64           `json:"prevTotal"`
	Categories []CategoryUsage `json:"categories"` // largest first; empty ones left out
}

// hostRows: (day, dev, host, bytes) from the database and what isn't flushed yet.
func (s *State) hostRows(fromDay, toDay, dev string, each func(day, dev, host string, b int64)) error {
	q := `SELECT day, dev, host, bytes FROM hosts_day WHERE day >= ? AND day <= ?`
	args := []any{fromDay, toDay}
	if dev != "" {
		q += ` AND dev = ?`
		args = append(args, dev)
	}
	rows, err := s.store.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d, v, h string
		var b int64
		if err := rows.Scan(&d, &v, &h, &b); err != nil {
			return err
		}
		each(d, v, h, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, b := range s.hosts {
		if k[0] >= fromDay && k[0] <= toDay && (dev == "" || k[1] == dev) {
			each(k[0], k[1], k[2], b)
		}
	}
	return nil
}

func (s *State) categories(now time.Time, days int, dev string) (CategoriesView, error) {
	day := func(t time.Time) string { return s.dayStart(t).Format("2006-01-02") }
	start := s.dayStart(now).AddDate(0, 0, -(days - 1))
	prevStart := start.AddDate(0, 0, -days)
	v := CategoriesView{From: day(start), To: day(now), Days: days, Categories: []CategoryUsage{}}
	type acc struct {
		bytes, prev int64
		sites       map[string]int64
		devs        map[string]int64
	}
	by := map[string]*acc{}
	get := func(k string) *acc {
		a := by[k]
		if a == nil {
			a = &acc{sites: map[string]int64{}, devs: map[string]int64{}}
			by[k] = a
		}
		return a
	}
	err := s.hostRows(day(prevStart), v.To, dev, func(d, dv, h string, b int64) {
		a := get(categoryOf(h))
		if d < v.From {
			a.prev += b
			v.PrevTotal += b
			return
		}
		a.bytes += b
		a.sites[h] += b
		a.devs[dv] += b
		v.Total += b
	})
	if err != nil {
		return v, err
	}
	s.mu.RLock()
	names := map[string]string{}
	for id, d := range s.devs {
		names[id] = displayName(d.devRow)
	}
	s.mu.RUnlock()
	for _, c := range categoryList {
		a := by[c.Key]
		if a == nil || (a.bytes == 0 && a.prev == 0) {
			continue
		}
		u := CategoryUsage{Key: c.Key, Name: c.Name, Bytes: a.bytes, Prev: a.prev, Sites: []CategorySite{}, Devices: []CategoryDevice{}}
		for h, b := range a.sites {
			u.Sites = append(u.Sites, CategorySite{h, b})
		}
		sort.Slice(u.Sites, func(i, j int) bool { return u.Sites[i].Bytes > u.Sites[j].Bytes })
		if len(u.Sites) > 6 {
			u.Sites = u.Sites[:6]
		}
		for id, b := range a.devs {
			n := names[id]
			if n == "" {
				n = id
			}
			u.Devices = append(u.Devices, CategoryDevice{id, n, b})
		}
		sort.Slice(u.Devices, func(i, j int) bool { return u.Devices[i].Bytes > u.Devices[j].Bytes })
		if len(u.Devices) > 5 {
			u.Devices = u.Devices[:5]
		}
		v.Categories = append(v.Categories, u)
	}
	sort.SliceStable(v.Categories, func(i, j int) bool { return v.Categories[i].Bytes > v.Categories[j].Bytes })
	return v, nil
}

func queryDays(w http.ResponseWriter, r *http.Request, def, max int) (int, bool) {
	n := def
	if v := r.URL.Query().Get("days"); v != "" {
		x, err := strconv.Atoi(v)
		if err != nil || x < 1 || x > max {
			fail(w, 400, "days must be a number from 1 to "+strconv.Itoa(max)+".")
			return 0, false
		}
		n = x
	}
	return n, true
}

func (a *API) getCategories(w http.ResponseWriter, r *http.Request) {
	n, ok := queryDays(w, r, 7, 200)
	if !ok {
		return
	}
	v, err := a.s.categories(time.Now(), n, r.URL.Query().Get("device"))
	if err != nil {
		fail(w, 500, "Could not read the sites from the database: "+err.Error())
		return
	}
	send(w, v)
}

// GET /api/devices/daily?days=14 -> { days: [YYYY-MM-DD...], devices: { id: [bytes per day] } }
func (a *API) devicesDaily(w http.ResponseWriter, r *http.Request) {
	n, ok := queryDays(w, r, 14, 60)
	if !ok {
		return
	}
	s := a.s
	now := time.Now()
	start := s.dayStart(now).AddDate(0, 0, -(n - 1))
	days := make([]string, n)
	idx := map[string]int{}
	for i := range days {
		days[i] = start.AddDate(0, 0, i).Format("2006-01-02")
		idx[days[i]] = i
	}
	out := map[string][]int64{}
	err := s.sumUsage(start, now.Add(time.Hour), "", func(h int64, dev, _ string, down, up int64) {
		i, ok := idx[s.dayStart(time.Unix(h, 0)).Format("2006-01-02")]
		if !ok {
			return
		}
		if out[dev] == nil {
			out[dev] = make([]int64, n)
		}
		out[dev][i] += down + up
	})
	if err != nil {
		fail(w, 500, "Could not read usage from the database: "+err.Error())
		return
	}
	send(w, map[string]any{"days": days, "devices": out})
}
