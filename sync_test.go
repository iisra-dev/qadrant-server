package main

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tests"
)

func newSyncApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	if err := ensureSyncCollections(app); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestValidatePush(t *testing.T) {
	ok := []PushItem{{Collection: "tasks", ID: "a", Content: "{}"}, {Collection: "settings", ID: "settings", Content: "x"}}
	if err := validatePush(ok); err != nil {
		t.Fatalf("valid push rejected: %v", err)
	}
	bad := map[string][]PushItem{
		"collection":  {{Collection: "events", ID: "a", Content: "{}"}},
		"no id":       {{Collection: "tasks", Content: "{}"}},
		"long id":     {{Collection: "tasks", ID: strings.Repeat("a", 65), Content: "{}"}},
		"no content":  {{Collection: "tasks", ID: "a"}},
		"big content": {{Collection: "tasks", ID: "a", Content: strings.Repeat("a", maxContent+1)}},
		"negative":    {{Collection: "tasks", ID: "a", Content: "{}", BaseVersion: -1}},
		"duplicated":  {{Collection: "tasks", ID: "a", Content: "1"}, {Collection: "tasks", ID: "a", Content: "2"}},
	}
	for name, items := range bad {
		if err := validatePush(items); !errors.Is(err, errInvalidSync) {
			t.Errorf("%s: want errInvalidSync, got %v", name, err)
		}
	}
}

func TestPushAssignsCorrelativeVersionsAndRejectsStaleBases(t *testing.T) {
	app := newSyncApp(t)
	results, latest, err := pushRecords(app, []PushItem{
		{Collection: "tasks", ID: "a", Content: "A1"},
		{Collection: "goals", ID: "g", Content: "G1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if latest != 2 || !results[0].OK || results[0].Version != 1 || !results[1].OK || results[1].Version != 2 {
		t.Fatalf("first push: %+v latest=%d", results, latest)
	}

	// Another device still based on version 0 of "a": rejected with the current copy.
	results, latest, err = pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", Content: "A-other"}})
	if err != nil {
		t.Fatal(err)
	}
	if latest != 2 || results[0].OK || results[0].Version != 1 || results[0].Content != "A1" {
		t.Fatalf("stale push: %+v", results)
	}

	// Based on the current version: accepted with the next one.
	results, latest, _ = pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", BaseVersion: 1, Content: "A2"}})
	if !results[0].OK || results[0].Version != 3 || latest != 3 {
		t.Fatalf("push on current version: %+v", results)
	}

	// A base the server never had (it was emptied): rejected without content.
	results, _, _ = pushRecords(app, []PushItem{{Collection: "people", ID: "p", BaseVersion: 7, Content: "P"}})
	if results[0].OK || results[0].Version != 0 || results[0].Content != "" {
		t.Fatalf("unknown base: %+v", results)
	}
}

func TestContentIsStoredUntouched(t *testing.T) {
	app := newSyncApp(t)
	opaque := `{"record":{"title":"Llamar al taller ñ"},"changedAt":{}}` + "\n\x01binary-ish"
	if _, _, err := pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", Content: opaque}}); err != nil {
		t.Fatal(err)
	}
	page, _, _, err := pullRecords(app, 0, 10)
	if err != nil || len(page) != 1 || page[0].Content != opaque {
		t.Fatalf("content changed: %+v %v", page, err)
	}
}

func TestPullPagesInVersionOrder(t *testing.T) {
	app := newSyncApp(t)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, _, err := pushRecords(app, []PushItem{{Collection: "tasks", ID: id, Content: id}}); err != nil {
			t.Fatal(err)
		}
	}
	pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", BaseVersion: 1, Content: "a2"}})

	page, latest, more, err := pullRecords(app, 0, 2)
	if err != nil || len(page) != 2 || !more || latest != 6 || page[0].ID != "b" || page[1].ID != "c" {
		t.Fatalf("first page: %+v latest=%d more=%v %v", page, latest, more, err)
	}
	page, _, more, _ = pullRecords(app, page[1].Version, 10)
	if len(page) != 3 || more || page[2].ID != "a" || page[2].Content != "a2" || page[2].Version != 6 {
		t.Fatalf("second page: %+v more=%v", page, more)
	}
	page, latest, more, _ = pullRecords(app, 6, 10)
	if len(page) != 0 || more || latest != 6 {
		t.Fatalf("nothing new: %+v latest=%d", page, latest)
	}
}

func TestVersionsKeepGrowingAfterReset(t *testing.T) {
	app := newSyncApp(t)
	pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", Content: "a"}})
	before, _ := syncID(app)
	if err := resetSync(app); err != nil {
		t.Fatal(err)
	}
	after, _ := syncID(app)
	if before == "" || before == after {
		t.Fatalf("sync id must change on reset: %q -> %q", before, after)
	}
	page, latest, _, _ := pullRecords(app, 0, 10)
	if len(page) != 0 || latest != 1 {
		t.Fatalf("after reset: %+v latest=%d", page, latest)
	}
	results, _, _ := pushRecords(app, []PushItem{{Collection: "tasks", ID: "a", Content: "a"}})
	if results[0].Version != 2 {
		t.Fatalf("version went back: %+v", results)
	}
}

func TestRemindersVersion(t *testing.T) {
	app := newSyncApp(t)
	cases := []struct {
		version *int64
		accept  bool
	}{
		{nil, true},
		{ptr(5), true},
		{ptr(4), false},
		{ptr(5), true},
		{nil, true}, // devices that do not sync always replace the list
		{ptr(4), false},
		{ptr(9), true},
	}
	for i, c := range cases {
		ok, err := acceptRemindersVersion(app, c.version)
		if err != nil || ok != c.accept {
			t.Fatalf("case %d: got %v %v, want %v", i, ok, err, c.accept)
		}
	}
}

func ptr(v int64) *int64 { return &v }

func TestHubDeliversTheLatestVersion(t *testing.T) {
	h := newHub()
	ch, cancel := h.subscribe()
	defer cancel()
	h.publish(3)
	h.publish(4) // a slow reader gets the newest, not a queue
	if got := <-ch; got != 4 {
		t.Fatalf("got %d, want 4", got)
	}
	cancel()
	h.publish(5) // no panic after unsubscribing
}

func TestEventsStreamSendsVersionAndHeartbeat(t *testing.T) {
	h := newHub()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveEvents(w, r, h, 7, 20*time.Millisecond)
	}))
	defer server.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewScanner(res.Body)
	next := func() string {
		for lines.Scan() {
			if l := lines.Text(); l != "" {
				return l
			}
		}
		t.Fatal("stream ended")
		return ""
	}
	if l := next(); l != "event: version" {
		t.Fatalf("got %q", l)
	}
	if l := next(); l != `data: {"version":7}` {
		t.Fatalf("got %q", l)
	}
	if l := next(); l != ": ping" {
		t.Fatalf("heartbeat: got %q", l)
	}
	h.publish(8)
	for {
		l := next()
		if l == `data: {"version":8}` {
			break
		}
		if l != ": ping" && l != "event: version" {
			t.Fatalf("unexpected %q", l)
		}
	}
}
