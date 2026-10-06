// Qadrant's optional server: PocketBase used as a Go framework.
// Phase 3: access key, VAPID keys, push subscriptions, reminders and a cron
// that sends due and follow-up notices with webpush-go (docs/02, docs/06).
// Optional: a read-only calendar from a secret iCal address, refreshed every 15 min.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const (
	remindersCollection     = "qadrant_reminders"
	subscriptionsCollection = "qadrant_subscriptions"
)

func main() {
	app := pocketbase.New()

	var accessKey string
	var vapid vapidKeys

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		key, created, err := loadOrCreateAccessKey(app.DataDir())
		if err != nil {
			return err
		}
		accessKey = key
		if created {
			log.Printf("Clave de acceso de Qadrant (pégala en Ajustes): %s", key)
		}
		if vapid, err = loadOrCreateVAPID(app.DataDir()); err != nil {
			return err
		}
		if err := ensureCollections(app); err != nil {
			return err
		}
		return ensureCalendarCollection(app)
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		api := se.Router.Group("/api/qadrant")
		api.BindFunc(func(e *core.RequestEvent) error {
			if !authorized(e.Request.Header.Get("Authorization"), accessKey) {
				return e.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}
			return e.Next()
		})

		api.GET("/ping", func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, map[string]any{"ok": true, "version": 1})
		})

		api.GET("/vapid", func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, map[string]string{"publicKey": vapid.Public})
		})

		api.POST("/subscriptions", func(e *core.RequestEvent) error {
			var sub webpush.Subscription
			if err := e.BindBody(&sub); err != nil || sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid subscription"})
			}
			if err := saveSubscription(app, sub); err != nil {
				return err
			}
			return e.NoContent(http.StatusNoContent)
		})

		api.DELETE("/subscriptions", func(e *core.RequestEvent) error {
			var body struct {
				Endpoint string `json:"endpoint"`
			}
			if err := e.BindBody(&body); err != nil || body.Endpoint == "" {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid endpoint"})
			}
			if err := deleteSubscription(app, body.Endpoint); err != nil {
				return err
			}
			return e.NoContent(http.StatusNoContent)
		})

		// The client sends the full list each time tasks change; completed or
		// deleted tasks simply are not in it any more.
		api.PUT("/reminders", func(e *core.RequestEvent) error {
			var list []Reminder
			if err := json.NewDecoder(http.MaxBytesReader(e.Response, e.Request.Body, 1<<20)).Decode(&list); err != nil {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
			}
			if err := validateReminders(list); err != nil {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			if err := replaceReminders(app, list); err != nil {
				return err
			}
			return e.JSON(http.StatusOK, map[string]int{"count": len(list)})
		})

		api.DELETE("/reminders/{taskId}", func(e *core.RequestEvent) error {
			if err := deleteReminders(app, e.Request.PathValue("taskId")); err != nil {
				return err
			}
			return e.NoContent(http.StatusNoContent)
		})

		// Optional calendar (secret iCal address), read-only.
		api.GET("/calendar", calendarGet(app))
		api.PUT("/calendar", calendarPut(app))
		api.DELETE("/calendar", calendarDelete(app))

		return se.Next()
	})

	app.Cron().MustAdd("qadrant_calendar", "*/15 * * * *", func() {
		if err := refreshCalendar(app, time.Now()); err != nil {
			app.Logger().Error("refreshing calendar", "error", err)
		}
	})

	app.Cron().MustAdd("qadrant_reminders", "* * * * *", func() {
		if err := sendDue(app, vapid, time.Now()); err != nil {
			app.Logger().Error("sending reminders", "error", err)
		}
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func ensureCollections(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(remindersCollection); err != nil {
		c := core.NewBaseCollection(remindersCollection)
		c.Fields.Add(
			&core.TextField{Name: "taskId", Required: true, Max: maxTaskID},
			&core.TextField{Name: "kind", Required: true},
			&core.DateField{Name: "at", Required: true},
			&core.TextField{Name: "title", Max: maxTitle * 4},
			&core.BoolField{Name: "sent"},
		)
		c.AddIndex("idx_qadrant_reminders_task", true, "taskId, kind", "")
		if err := app.Save(c); err != nil {
			return err
		}
	}
	if _, err := app.FindCollectionByNameOrId(subscriptionsCollection); err != nil {
		c := core.NewBaseCollection(subscriptionsCollection)
		c.Fields.Add(
			&core.TextField{Name: "endpoint", Required: true, Max: 2000},
			&core.TextField{Name: "p256dh", Required: true},
			&core.TextField{Name: "auth", Required: true},
		)
		c.AddIndex("idx_qadrant_subscriptions_endpoint", true, "endpoint", "")
		if err := app.Save(c); err != nil {
			return err
		}
	}
	// No API rules: the collections are reachable only through /api/qadrant.
	return nil
}

func storedReminders(app core.App) ([]Reminder, []*core.Record, error) {
	records, err := app.FindAllRecords(remindersCollection)
	if err != nil {
		return nil, nil, err
	}
	list := make([]Reminder, len(records))
	for i, r := range records {
		list[i] = Reminder{
			TaskID: r.GetString("taskId"),
			Kind:   r.GetString("kind"),
			At:     r.GetDateTime("at").Time(),
			Title:  r.GetString("title"),
			Sent:   r.GetBool("sent"),
		}
	}
	return list, records, nil
}

func replaceReminders(app core.App, incoming []Reminder) error {
	return app.RunInTransaction(func(tx core.App) error {
		existing, records, err := storedReminders(tx)
		if err != nil {
			return err
		}
		for _, r := range records {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		collection, err := tx.FindCollectionByNameOrId(remindersCollection)
		if err != nil {
			return err
		}
		for _, r := range mergeReminders(existing, incoming) {
			record := core.NewRecord(collection)
			record.Set("taskId", r.TaskID)
			record.Set("kind", r.Kind)
			record.Set("at", r.At)
			record.Set("title", r.Title)
			record.Set("sent", r.Sent)
			if err := tx.Save(record); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteReminders(app core.App, taskID string) error {
	records, err := app.FindRecordsByFilter(remindersCollection, "taskId = {:id}", "", 0, 0, dbx.Params{"id": taskID})
	if err != nil {
		return err
	}
	for _, r := range records {
		if err := app.Delete(r); err != nil {
			return err
		}
	}
	return nil
}

func saveSubscription(app core.App, sub webpush.Subscription) error {
	record, err := app.FindFirstRecordByData(subscriptionsCollection, "endpoint", sub.Endpoint)
	if err != nil {
		collection, err := app.FindCollectionByNameOrId(subscriptionsCollection)
		if err != nil {
			return err
		}
		record = core.NewRecord(collection)
	}
	record.Set("endpoint", sub.Endpoint)
	record.Set("p256dh", sub.Keys.P256dh)
	record.Set("auth", sub.Keys.Auth)
	return app.Save(record)
}

func deleteSubscription(app core.App, endpoint string) error {
	record, err := app.FindFirstRecordByData(subscriptionsCollection, "endpoint", endpoint)
	if err != nil {
		return nil // already gone
	}
	return app.Delete(record)
}

// sendDue notifies every subscription of the reminders whose time has come.
func sendDue(app core.App, vapid vapidKeys, now time.Time) error {
	list, records, err := storedReminders(app)
	if err != nil {
		return err
	}
	due := dueReminders(list, now)
	if len(due) == 0 {
		return nil
	}
	subs, err := app.FindAllRecords(subscriptionsCollection)
	if err != nil {
		return err
	}
	options := &webpush.Options{
		Subscriber:      subscriber(),
		VAPIDPublicKey:  vapid.Public,
		VAPIDPrivateKey: vapid.Private,
		TTL:             24 * 60 * 60,
		Urgency:         webpush.UrgencyHigh,
	}
	gone := map[string]*core.Record{}
	for _, r := range due {
		body, _ := json.Marshal(payloadFor(r))
		for _, s := range subs {
			res, err := webpush.SendNotification(body, &webpush.Subscription{
				Endpoint: s.GetString("endpoint"),
				Keys:     webpush.Keys{P256dh: s.GetString("p256dh"), Auth: s.GetString("auth")},
			}, options)
			if err != nil {
				app.Logger().Warn("push failed", "error", err)
				continue
			}
			res.Body.Close()
			// 404 and 410: the browser dropped the subscription.
			if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
				gone[s.Id] = s
			}
		}
	}
	for _, s := range gone {
		_ = app.Delete(s)
	}
	for i, r := range list {
		if !r.Sent && !r.At.After(now) {
			records[i].Set("sent", true)
			if err := app.Save(records[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// subscriber is the VAPID contact; push services may use it to reach the owner.
func subscriber() string {
	if s := os.Getenv("QADRANT_VAPID_SUBJECT"); s != "" {
		return s
	}
	return "mailto:admin@localhost"
}
