package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/apognu/gocal"
)

// CalendarEvent is a concrete occurrence, already expanded (docs/04, version 2).
// All-day events carry dates ("2026-10-12"), the rest RFC 3339 instants.
type CalendarEvent struct {
	ID     string `json:"id"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Title  string `json:"title"`
	AllDay bool   `json:"allDay"`
}

const (
	calendarPastDays   = 7
	calendarFutureDays = 30
	maxCalendarBytes   = 5 << 20
	maxEvents          = 5000
)

var errCalendarURL = errors.New("the calendar address must be https:// or webcal://")

// normaliseCalendarURL accepts the secret iCal address as calendars show it.
func normaliseCalendarURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errCalendarURL
	}
	switch u.Scheme {
	case "webcal":
		u.Scheme = "https"
	case "https":
	default:
		return "", errCalendarURL
	}
	return u.String(), nil
}

// calendarWindow is the range the app receives: the last 7 days and the next 30.
func calendarWindow(now time.Time) (time.Time, time.Time) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -calendarPastDays), day.AddDate(0, 0, calendarFutureDays+1)
}

func isAllDay(raw gocal.RawDate) bool {
	return raw.Params["VALUE"] == "DATE" || len(raw.Value) == 8
}

// parseCalendar expands repetitions and time zones in [from, to).
func parseCalendar(r io.Reader, from, to time.Time, local *time.Location) ([]CalendarEvent, error) {
	parser := gocal.NewParser(r)
	parser.Start, parser.End = &from, &to
	parser.AllDayEventsTZ = local
	// Lenient: real feeds sometimes miss DTSTAMP; drop bad attributes, not the whole feed.
	parser.Strict.Mode = gocal.StrictModeFailAttribute
	if err := parser.Parse(); err != nil {
		return nil, err
	}
	events := make([]CalendarEvent, 0, len(parser.Events))
	for _, e := range parser.Events {
		if e.Start == nil || strings.EqualFold(e.Status, "CANCELLED") {
			continue
		}
		end := e.End
		if end == nil {
			fallback := *e.Start
			if e.Duration != nil {
				fallback = fallback.Add(*e.Duration)
			}
			end = &fallback
		}
		ev := CalendarEvent{Title: e.Summary, AllDay: isAllDay(e.RawStart)}
		if ev.AllDay {
			ev.Start = e.Start.In(local).Format(time.DateOnly)
			// Exclusive end date, as in DTEND;VALUE=DATE (gocal may give 23:59:59 of the last day).
			ev.End = end.In(local).Add(time.Second).Format(time.DateOnly)
		} else {
			ev.Start = e.Start.UTC().Format(time.RFC3339)
			ev.End = end.UTC().Format(time.RFC3339)
		}
		sum := sha1.Sum([]byte(e.Uid + "\x00" + ev.Start))
		ev.ID = hex.EncodeToString(sum[:10])
		events = append(events, ev)
		if len(events) >= maxEvents {
			break
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Start < events[j].Start })
	return events, nil
}

var calendarClient = &http.Client{Timeout: 20 * time.Second}

// fetchCalendar downloads the iCal feed. Only the server does this: calendars
// do not allow CORS, and so the app never talks to Google, Apple or Microsoft.
func fetchCalendar(ctx context.Context, address string, now time.Time, local *time.Location) ([]CalendarEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/calendar")
	res, err := calendarClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calendar answered %d", res.StatusCode)
	}
	from, to := calendarWindow(now.In(local))
	return parseCalendar(io.LimitReader(res.Body, maxCalendarBytes), from, to, local)
}
