package queries

import (
	"testing"
	"time"
)

// The trend's day window is laid out in Go, from an anchor day the database
// hands over. These tests pin that layout deterministically — no Dolt server,
// no wall clock — so the live test in graph_test.go only has to prove one
// thing: that the anchor really is the database's own date.

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestTrendSeriesSpansTheWindowEndingOnTheAnchor(t *testing.T) {
	const days = 30
	anchor := day("2026-08-16")

	got := trendSeries(anchor, days, nil)

	if len(got) != days {
		t.Fatalf("series has %d points, want %d", len(got), days)
	}
	if last := got[len(got)-1].Date; last != "2026-08-16" {
		t.Errorf("series ends %q, want the anchor day 2026-08-16", last)
	}
	if first := got[0].Date; first != "2026-07-18" {
		t.Errorf("series starts %q, want anchor-29 = 2026-07-18", first)
	}
	for i := 1; i < len(got); i++ {
		gap := day(got[i].Date).Sub(day(got[i-1].Date))
		if gap != 24*time.Hour {
			t.Errorf("point %d is %v after the previous one, want 24h", i, gap)
		}
	}
	for i, p := range got {
		if p.Opened != 0 || p.Closed != 0 {
			t.Errorf("point %d = %+v, want zeros for a window with no buckets", i, p)
		}
	}
}

func TestTrendSeriesPlacesCountsOnTheirOwnDay(t *testing.T) {
	anchor := day("2026-08-16")
	byDay := map[string]trendCounts{
		"2026-08-16": {opened: 3, closed: 1}, // the anchor day itself
		"2026-08-14": {opened: 0, closed: 2},
		"2026-08-10": {opened: 5, closed: 0}, // the oldest day in a 7-day window
	}

	got := trendSeries(anchor, 7, byDay)

	want := []TrendPoint{
		{Date: "2026-08-10", Opened: 5, Closed: 0},
		{Date: "2026-08-11"},
		{Date: "2026-08-12"},
		{Date: "2026-08-13"},
		{Date: "2026-08-14", Opened: 0, Closed: 2},
		{Date: "2026-08-15"},
		{Date: "2026-08-16", Opened: 3, Closed: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("series has %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A bucket outside the window is dropped rather than stretching the series or
// leaking its counts onto a neighbouring day. Both edges matter: an event
// dated after the anchor (the clocks in the events table are the writer's
// local wall clock, so an agent in a further-east zone can stamp tomorrow) and
// one dated a day before the window opens.
func TestTrendSeriesDropsBucketsOutsideTheWindow(t *testing.T) {
	anchor := day("2026-08-16")
	byDay := map[string]trendCounts{
		"2026-08-17": {opened: 9, closed: 9}, // after the anchor
		"2026-08-09": {opened: 7, closed: 7}, // one day before the window opens
		"2026-08-13": {opened: 1, closed: 1}, // inside
	}

	got := trendSeries(anchor, 7, byDay)

	if len(got) != 7 {
		t.Fatalf("series has %d points, want 7", len(got))
	}
	var opened, closed int
	for _, p := range got {
		opened += p.Opened
		closed += p.Closed
		if p.Date == "2026-08-17" || p.Date == "2026-08-09" {
			t.Errorf("out-of-window day %s appeared in the series", p.Date)
		}
	}
	if opened != 1 || closed != 1 {
		t.Errorf("series totals = %d opened / %d closed, want 1/1: out-of-window buckets leaked in", opened, closed)
	}
}
