package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCategoryOf(t *testing.T) {
	for host, want := range map[string]string{
		"rr1---sn-abc.googlevideo.com":                  "video",
		"instagram.fxyz1-3.fna.fbcdn.net":               "social",
		"video.twimg.com":                               "social",
		"v77.tiktokcdn.com":                             "social",
		"media.fxyz1-3.fna.whatsapp.net":                "messaging",
		"api.anthropic.com":                             "ai",
		"release-assets.githubusercontent.com":          "ai",
		"1d.tlu.dl.delivery.mp.microsoft.com":           "updates",
		"server-1.prod.hosts.ooklaserver.net":           "speedtest",
		"audio-cf.spotifycdn.com":                       "music",
		"d2.xboxlive.com":                               "gaming",
		"backup.googleapis.com":                         "cloud",
		"www.example.org":                               "web",
		"203.0.113.9":                                   "unnamed",
		"[2001:db8::1]":                                 "unnamed",
		"":                                              "unnamed",
		"GOOGLEVIDEO.COM.":                              "video",
		"gamedownloads-rockstargames-com.akamaized.net": "gaming",
		"other.akamaized.net":                           "web",
	} {
		if got := categoryOf(host); got != want {
			t.Errorf("%q: %s, want %s", host, got, want)
		}
	}
	for _, c := range categoryDomains {
		if categoryName(c) == c {
			t.Errorf("category %q has no name", c)
		}
	}
}

func TestCategoriesAndDaily(t *testing.T) {
	s := testState(t)
	now := time.Now()
	day := func(d int) string { return s.dayStart(now).AddDate(0, 0, d).Format("2006-01-02") }
	ins := func(d, dev, host string, b int64) {
		if _, err := s.store.db.Exec(`INSERT INTO hosts_day (day, dev, host, bytes) VALUES (?, ?, ?, ?)`, d, dev, host, b); err != nil {
			t.Fatal(err)
		}
	}
	ins(day(0), "aa", "rr1.googlevideo.com", 5e9)
	ins(day(-1), "bb", "rr2.googlevideo.com", 1e9)
	ins(day(0), "aa", "api.anthropic.com", 2e9)
	ins(day(-2), "bb", "203.0.113.9", 1e9)
	ins(day(-9), "aa", "rr1.googlevideo.com", 3e9)  // the week before
	ins(day(-30), "aa", "rr1.googlevideo.com", 9e9) // outside both periods
	s.hosts = map[[3]string]int64{{day(0), "bb", "www.example.org"}: 5e8}
	s.devs["aa"] = &devState{devRow: devRow{ID: "aa", Name: "Laptop"}}

	v, err := s.categories(now, 7, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if v.Total != 9.5e9 || v.PrevTotal != 3e9 || len(v.Categories) != 4 || v.Categories[0].Key != "video" || v.Categories[0].Bytes != 6e9 || v.Categories[0].Prev != 3e9 {
		t.Fatalf("categories: %s", b)
	}
	if v.Categories[0].Devices[0].Name != "Laptop" || v.Categories[0].Sites[0].Host != "rr1.googlevideo.com" {
		t.Fatalf("top of video: %s", b)
	}
	only, _ := s.categories(now, 7, "bb")
	if only.Total != 2.5e9 {
		t.Fatalf("one device: %+v", only)
	}

	rec := do(testHandler(s), call{method: "GET", path: "/api/categories?days=0"})
	if rec.Code != 400 {
		t.Fatalf("days=0: %d", rec.Code)
	}
	rec = do(testHandler(s), call{method: "GET", path: "/api/devices/daily?days=3"})
	var d struct {
		Days    []string           `json:"days"`
		Devices map[string][]int64 `json:"devices"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || len(d.Days) != 3 || d.Days[2] != day(0) {
		t.Fatalf("daily: %d %s", rec.Code, rec.Body)
	}
}
