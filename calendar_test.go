package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNormaliseCalendarURL(t *testing.T) {
	got, err := normaliseCalendarURL(" webcal://p01-caldav.icloud.com/published/2/abc ")
	if err != nil || got != "https://p01-caldav.icloud.com/published/2/abc" {
		t.Fatalf("webcal: %q %v", got, err)
	}
	for _, bad := range []string{"http://example.com/cal.ics", "ftp://x", "nonsense", ""} {
		if _, err := normaliseCalendarURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestParseCalendarExpandsInTheWindow(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skip("no tzdata")
	}
	f, err := os.Open("testdata/calendar.ics")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, madrid)
	from, to := calendarWindow(now)
	events, err := parseCalendar(f, from, to, madrid)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string][]CalendarEvent{}
	for _, e := range events {
		byTitle[e.Title] = append(byTitle[e.Title], e)
	}
	// Weekly on Mondays from 28 Sep to 1 Nov (window end), minus the excluded 12 Oct; after the
	// DST change (25 Oct) 10:00 Madrid is 09:00Z.
	standups := byTitle["Reunión de equipo"]
	var starts []string
	for _, e := range standups {
		starts = append(starts, e.Start)
	}
	want := []string{"2026-09-28T08:00:00Z", "2026-10-05T08:00:00Z", "2026-10-19T08:00:00Z", "2026-10-26T09:00:00Z"}
	if len(starts) != len(want) {
		t.Fatalf("standups: got %v, want %v", starts, want)
	}
	for i := range want {
		if starts[i] != want[i] {
			t.Fatalf("standups: got %v, want %v", starts, want)
		}
	}
	if h := byTitle["Fiesta Nacional"]; len(h) != 1 || !h[0].AllDay || h[0].Start != "2026-10-12" || h[0].End != "2026-10-13" {
		t.Fatalf("all-day: %+v", h)
	}
	if d := byTitle["Dentista"]; len(d) != 1 || d[0].AllDay || d[0].End != "2026-10-05T16:00:00Z" {
		t.Fatalf("timed: %+v", d)
	}
	if len(byTitle["Cancelada"]) != 0 || len(byTitle["Fuera de rango"]) != 0 {
		t.Fatalf("cancelled or out of range events kept: %+v", events)
	}
	ids := map[string]bool{}
	for _, e := range events {
		if ids[e.ID] {
			t.Fatalf("duplicated id %s", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestFetchCalendarOverHTTPS(t *testing.T) {
	ics, err := os.ReadFile("testdata/calendar.ics")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secret.ics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write(ics)
	}))
	defer srv.Close()
	previous := calendarClient
	calendarClient = srv.Client()
	defer func() { calendarClient = previous }()

	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	events, err := fetchCalendar(context.Background(), srv.URL+"/secret.ics", now, time.UTC)
	if err != nil || len(events) == 0 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	if _, err := fetchCalendar(context.Background(), srv.URL+"/missing.ics", now, time.UTC); err == nil {
		t.Fatal("a 404 must be an error")
	}
}
