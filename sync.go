package main

// Sync (phase 4, docs/02 "Sincronización"): the server keeps one opaque copy
// per record with a correlative version and never reads its content, so it
// can be encrypted later without changing the protocol. Merging happens in
// the app.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	syncCollection  = "qadrant_sync"
	stateCollection = "qadrant_state"

	// apiVersion 2 adds sync; /ping returns it so the app knows.
	apiVersion = 2

	maxContent   = 256 << 10
	maxPushItems = 500
	maxRecordID  = 64
	maxPullPage  = 1000
	defaultPage  = 200

	stateSyncVersion      = "syncVersion"
	stateSyncID           = "syncId"
	stateRemindersVersion = "remindersVersion"
)

var syncCollections = map[string]bool{"tasks": true, "goals": true, "people": true, "corrections": true, "settings": true}

var errInvalidSync = errors.New("invalid sync record")

// PushItem is one record a device uploads: the version it started from and its content.
type PushItem struct {
	Collection  string `json:"collection"`
	ID          string `json:"id"`
	BaseVersion int64  `json:"baseVersion"`
	Content     string `json:"content"`
}

// PushResult says whether a record was accepted. When it was not, it carries
// the version and content on the server (empty if the server has none).
type PushResult struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
	OK         bool   `json:"ok"`
	Version    int64  `json:"version"`
	Content    string `json:"content,omitempty"`
}

// SyncRecord is a record as the server keeps it.
type SyncRecord struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Version    int64  `json:"version"`
	Content    string `json:"content"`
}

func ensureSyncCollections(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(syncCollection); err != nil {
		c := core.NewBaseCollection(syncCollection)
		c.Fields.Add(
			&core.TextField{Name: "collection", Required: true, Max: 32},
			&core.TextField{Name: "recordId", Required: true, Max: maxRecordID},
			&core.NumberField{Name: "version", Required: true, OnlyInt: true},
			&core.TextField{Name: "content", Required: true, Max: maxContent},
		)
		c.AddIndex("idx_qadrant_sync_record", true, "collection, recordId", "")
		c.AddIndex("idx_qadrant_sync_version", false, "version", "")
		if err := app.Save(c); err != nil {
			return err
		}
	}
	if _, err := app.FindCollectionByNameOrId(stateCollection); err != nil {
		c := core.NewBaseCollection(stateCollection)
		c.Fields.Add(
			&core.TextField{Name: "key", Required: true, Max: 64},
			&core.TextField{Name: "value", Max: 256},
		)
		c.AddIndex("idx_qadrant_state_key", true, "key", "")
		if err := app.Save(c); err != nil {
			return err
		}
	}
	return nil
}

func getState(app core.App, key string) (string, error) {
	record, err := app.FindFirstRecordByData(stateCollection, "key", key)
	if err != nil {
		return "", nil // not set yet
	}
	return record.GetString("value"), nil
}

func setState(app core.App, key, value string) error {
	record, err := app.FindFirstRecordByData(stateCollection, "key", key)
	if err != nil {
		collection, err := app.FindCollectionByNameOrId(stateCollection)
		if err != nil {
			return err
		}
		record = core.NewRecord(collection)
		record.Set("key", key)
	}
	record.Set("value", value)
	return app.Save(record)
}

func getStateInt(app core.App, key string) (int64, bool, error) {
	value, err := getState(app, key)
	if err != nil || value == "" {
		return 0, false, err
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil, err
}

// latestVersion is the highest version ever given. It never goes back, not
// even after a reset, so a device's cursor stays meaningful.
func latestVersion(app core.App) (int64, error) {
	n, _, err := getStateInt(app, stateSyncVersion)
	return n, err
}

// syncID names this copy of the synced data; it changes when it is emptied,
// so devices know to upload everything again.
func syncID(app core.App) (string, error) {
	id, err := getState(app, stateSyncID)
	if err != nil || id != "" {
		return id, err
	}
	id = newSyncID()
	return id, setState(app, stateSyncID, id)
}

func newSyncID() string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func validatePush(items []PushItem) error {
	if len(items) > maxPushItems {
		return fmt.Errorf("%w: more than %d records", errInvalidSync, maxPushItems)
	}
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		if !syncCollections[item.Collection] {
			return fmt.Errorf("%w %d: collection", errInvalidSync, i)
		}
		if item.ID == "" || len(item.ID) > maxRecordID {
			return fmt.Errorf("%w %d: id", errInvalidSync, i)
		}
		if item.Content == "" || len(item.Content) > maxContent {
			return fmt.Errorf("%w %d: content", errInvalidSync, i)
		}
		if item.BaseVersion < 0 {
			return fmt.Errorf("%w %d: baseVersion", errInvalidSync, i)
		}
		key := item.Collection + "\x00" + item.ID
		if seen[key] {
			return fmt.Errorf("%w %d: duplicated record", errInvalidSync, i)
		}
		seen[key] = true
	}
	return nil
}

func findSyncRecord(app core.App, collection, id string) *core.Record {
	record, err := app.FindFirstRecordByFilter(syncCollection, "collection = {:c} && recordId = {:id}", dbx.Params{"c": collection, "id": id})
	if err != nil {
		return nil
	}
	return record
}

// pushRecords accepts each record whose version on the server is still its
// baseVersion and gives it the next version. The rest come back with what
// the server has, for the device to merge and try again.
func pushRecords(app core.App, items []PushItem) ([]PushResult, int64, error) {
	results := make([]PushResult, len(items))
	var latest int64
	err := app.RunInTransaction(func(tx core.App) error {
		var err error
		if latest, err = latestVersion(tx); err != nil {
			return err
		}
		collection, err := tx.FindCollectionByNameOrId(syncCollection)
		if err != nil {
			return err
		}
		accepted := false
		for i, item := range items {
			record := findSyncRecord(tx, item.Collection, item.ID)
			var current int64
			if record != nil {
				current = int64(record.GetInt("version"))
			}
			if current != item.BaseVersion {
				results[i] = PushResult{Collection: item.Collection, ID: item.ID, Version: current}
				if record != nil {
					results[i].Content = record.GetString("content")
				}
				continue
			}
			if record == nil {
				record = core.NewRecord(collection)
				record.Set("collection", item.Collection)
				record.Set("recordId", item.ID)
			}
			latest++
			record.Set("version", latest)
			record.Set("content", item.Content)
			if err := tx.Save(record); err != nil {
				return err
			}
			accepted = true
			results[i] = PushResult{Collection: item.Collection, ID: item.ID, OK: true, Version: latest}
		}
		if !accepted {
			return nil
		}
		return setState(tx, stateSyncVersion, strconv.FormatInt(latest, 10))
	})
	return results, latest, err
}

// pullRecords returns the records with a version above since, oldest first,
// at most limit of them, and whether there are more.
func pullRecords(app core.App, since int64, limit int) ([]SyncRecord, int64, bool, error) {
	latest, err := latestVersion(app)
	if err != nil {
		return nil, 0, false, err
	}
	records, err := app.FindRecordsByFilter(syncCollection, "version > {:since}", "version", limit+1, 0, dbx.Params{"since": since})
	if err != nil {
		return nil, 0, false, err
	}
	more := len(records) > limit
	if more {
		records = records[:limit]
	}
	page := make([]SyncRecord, len(records))
	for i, r := range records {
		page[i] = SyncRecord{
			Collection: r.GetString("collection"),
			ID:         r.GetString("recordId"),
			Version:    int64(r.GetInt("version")),
			Content:    r.GetString("content"),
		}
	}
	return page, latest, more, nil
}

// resetSync empties the synced data and gives it a new id; the version keeps
// counting. Devices notice the new id and upload what they have again.
func resetSync(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		records, err := tx.FindAllRecords(syncCollection)
		if err != nil {
			return err
		}
		for _, r := range records {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		return setState(tx, stateSyncID, newSyncID())
	})
}

// acceptRemindersVersion tells whether a reminder list made from that sync
// version may replace the stored one: an older device must not drop the
// notices of a newer one. Lists without a version (devices that do not sync)
// are always accepted.
func acceptRemindersVersion(app core.App, version *int64) (bool, error) {
	if version == nil {
		return true, nil
	}
	stored, ok, err := getStateInt(app, stateRemindersVersion)
	if err != nil {
		return false, err
	}
	if ok && *version < stored {
		return false, nil
	}
	return true, setState(app, stateRemindersVersion, strconv.FormatInt(*version, 10))
}

// hub tells the open event streams about the latest version.
type hub struct {
	mu   sync.Mutex
	subs map[chan int64]struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan int64]struct{}{}}
}

func (h *hub) subscribe() (<-chan int64, func()) {
	ch := make(chan int64, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// publish never blocks: a reader that is behind gets only the newest version.
func (h *hub) publish(version int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case <-ch:
		default:
		}
		ch <- version
	}
}

// heartbeat keeps the stream alive: Cloudflare closes responses that send
// nothing for 100 s.
const heartbeat = 30 * time.Second

// serveEvents streams "version" events: the current one first, then one per
// change, with a comment line as heartbeat.
func serveEvents(w http.ResponseWriter, r *http.Request, h *hub, current int64, every time.Duration) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the server's WriteTimeout would cut the stream
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := h.subscribe()
	defer cancel()
	send := func(version int64) error {
		data, _ := json.Marshal(map[string]int64{"version": version})
		if _, err := fmt.Fprintf(w, "event: version\ndata: %s\n\n", data); err != nil {
			return err
		}
		return rc.Flush()
	}
	if send(current) != nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case version := <-ch:
			if send(version) != nil {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// pullLimit reads ?limit=, within bounds.
func pullLimit(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return defaultPage
	}
	return min(n, maxPullPage)
}
