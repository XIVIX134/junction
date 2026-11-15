package main

import (
	"testing"
	"time"
)

func at(day time.Weekday, hm string) time.Time {
	// 2026-11-15 is a Sunday
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	m, _ := parseHM(hm)
	return base.AddDate(0, 0, int(day)).Add(time.Duration(m) * time.Minute)
}

func TestScheduleActive(t *testing.T) {
	// Kids' bedtime: Sun-Thu 22:00-07:00 (past midnight belongs to the start day)
	bed := &RuleSchedule{Days: []int{0, 1, 2, 3, 4}, Start: "22:00", End: "07:00"}
	cases := []struct {
		t    time.Time
		want bool
	}{
		{at(time.Sunday, "21:59"), false},
		{at(time.Sunday, "22:00"), true},
		{at(time.Monday, "06:59"), true}, // Sunday night's window
		{at(time.Monday, "07:00"), false},
		{at(time.Thursday, "23:30"), true},
		{at(time.Friday, "03:00"), true},  // Thursday night's window
		{at(time.Friday, "23:00"), false}, // Friday isn't in the list
		{at(time.Saturday, "03:00"), false},
		{at(time.Sunday, "03:00"), false}, // Saturday night isn't in the list
	}
	for _, c := range cases {
		if got := scheduleActive(bed, c.t); got != c.want {
			t.Errorf("bedtime at %s %s: got %v want %v", c.t.Weekday(), c.t.Format("15:04"), got, c.want)
		}
	}
	// Daytime window: weekdays 08:00-14:00
	school := &RuleSchedule{Days: []int{1, 2, 3, 4, 5}, Start: "08:00", End: "14:00"}
	if !scheduleActive(school, at(time.Wednesday, "10:00")) || scheduleActive(school, at(time.Wednesday, "14:00")) || scheduleActive(school, at(time.Sunday, "10:00")) {
		t.Error("school window wrong")
	}
}

func TestScheduleText(t *testing.T) {
	for _, c := range []struct {
		days []int
		want string
	}{
		{[]int{0, 1, 2, 3, 4}, "Sun–Thu 22:00–07:00"},
		{[]int{0, 1, 2, 3, 4, 5, 6}, "every day 22:00–07:00"},
		{[]int{5, 6}, "Fri, Sat 22:00–07:00"},
		{[]int{1, 3, 5}, "Mon, Wed, Fri 22:00–07:00"},
	} {
		got := scheduleText(&RuleSchedule{Days: c.days, Start: "22:00", End: "07:00"})
		if got != c.want {
			t.Errorf("%v: got %q want %q", c.days, got, c.want)
		}
	}
}

func TestScheduleEnd(t *testing.T) {
	bed := &RuleSchedule{Days: []int{0}, Start: "22:00", End: "07:00"}
	e := scheduleEnd(bed, at(time.Sunday, "23:00"))
	if e.Weekday() != time.Monday || e.Format("15:04") != "07:00" {
		t.Errorf("skip tonight should end Monday 07:00, got %s", e)
	}
}
