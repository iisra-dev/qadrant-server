package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const calendarCollection = "qadrant_calendar"

func ensureCalendarCollection(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(calendarCollection); err == nil {
		return nil
	}
	c := core.NewBaseCollection(calendarCollection)
	c.Fields.Add(
		&core.TextField{Name: "url", Required: true, Max: 2000},
		&core.JSONField{Name: "events", MaxSize: 4 << 20},
		&core.DateField{Name: "fetchedAt"},
		&core.TextField{Name: "error"},
	)
	return app.Save(c)
}

// calendarRecord returns the single calendar record, or nil when none is set.
func calendarRecord(app core.App) *core.Record {
	records, err := app.FindRecordsByFilter(calendarCollection, "", "", 1, 0)
	if err != nil || len(records) == 0 {
		return nil
	}
	return records[0]
}

// refreshCalendar downloads the feed and keeps the last good events if it fails.
func refreshCalendar(app core.App, now time.Time) error {
	record := calendarRecord(app)
	if record == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events, err := fetchCalendar(ctx, record.GetString("url"), now, time.Local)
	if err != nil {
		record.Set("error", err.Error())
	} else {
		record.Set("events", events)
		record.Set("fetchedAt", now)
		record.Set("error", "")
	}
	return app.Save(record)
}

// calendarHandlers are mounted under /api/qadrant (behind the access key).
func calendarGet(app core.App) func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record := calendarRecord(app)
		if record == nil {
			return e.JSON(http.StatusOK, map[string]any{"connected": false, "events": []CalendarEvent{}})
		}
		events := []CalendarEvent{}
		if raw := record.GetString("events"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &events)
		}
		body := map[string]any{"connected": true, "events": events}
		if t := record.GetDateTime("fetchedAt"); !t.IsZero() {
			body["fetchedAt"] = t.Time().UTC().Format(time.RFC3339)
		}
		if msg := record.GetString("error"); msg != "" {
			body["error"] = msg
		}
		// The address itself never goes back to the app: only the server keeps it (docs/02).
		return e.JSON(http.StatusOK, body)
	}
}

func calendarPut(app core.App) func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var body struct {
			URL string `json:"url"`
		}
		if err := e.BindBody(&body); err != nil {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		}
		address, err := normaliseCalendarURL(body.URL)
		if err != nil {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		// Check it before saving, so a wrong address is reported at once.
		ctx, cancel := context.WithTimeout(e.Request.Context(), 30*time.Second)
		defer cancel()
		now := time.Now()
		events, err := fetchCalendar(ctx, address, now, time.Local)
		if err != nil {
			return e.JSON(http.StatusBadRequest, map[string]string{"error": "calendar: " + err.Error()})
		}
		record := calendarRecord(app)
		if record == nil {
			collection, err := app.FindCollectionByNameOrId(calendarCollection)
			if err != nil {
				return err
			}
			record = core.NewRecord(collection)
		}
		record.Set("url", address)
		record.Set("events", events)
		record.Set("fetchedAt", now)
		record.Set("error", "")
		if err := app.Save(record); err != nil {
			return err
		}
		return e.JSON(http.StatusOK, map[string]any{"connected": true, "events": events})
	}
}

func calendarDelete(app core.App) func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if record := calendarRecord(app); record != nil {
			if err := app.Delete(record); err != nil {
				return err
			}
		}
		return e.NoContent(http.StatusNoContent)
	}
}
