package main

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Reminder is all the server knows about a task (docs/02, "Servidor propio"):
// its id, when to notify, the kind of notice and the title.
type Reminder struct {
	TaskID string    `json:"taskId"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Title  string    `json:"title"`
	Sent   bool      `json:"sent,omitempty"`
}

const (
	KindDue      = "due"
	KindFollowUp = "follow-up"

	maxReminders = 2000
	maxTitle     = 200
	maxTaskID    = 64
)

var errInvalid = errors.New("invalid reminder")

// validateReminders checks a full list sent by the client.
func validateReminders(list []Reminder) error {
	if len(list) > maxReminders {
		return fmt.Errorf("%w: more than %d reminders", errInvalid, maxReminders)
	}
	seen := make(map[string]bool, len(list))
	for i, r := range list {
		if r.TaskID == "" || len(r.TaskID) > maxTaskID {
			return fmt.Errorf("%w %d: taskId", errInvalid, i)
		}
		if r.Kind != KindDue && r.Kind != KindFollowUp {
			return fmt.Errorf("%w %d: kind must be %q or %q", errInvalid, i, KindDue, KindFollowUp)
		}
		if r.At.IsZero() {
			return fmt.Errorf("%w %d: at", errInvalid, i)
		}
		if utf8.RuneCountInString(r.Title) > maxTitle {
			return fmt.Errorf("%w %d: title longer than %d characters", errInvalid, i, maxTitle)
		}
		key := r.TaskID + "\x00" + r.Kind
		if seen[key] {
			return fmt.Errorf("%w %d: duplicated taskId and kind", errInvalid, i)
		}
		seen[key] = true
	}
	return nil
}

// mergeReminders replaces the stored list with the incoming one, keeping the
// sent flag of reminders that did not change, so nothing is notified twice.
func mergeReminders(existing, incoming []Reminder) []Reminder {
	sent := make(map[string]bool, len(existing))
	for _, r := range existing {
		if r.Sent {
			sent[r.TaskID+"\x00"+r.Kind+"\x00"+r.At.UTC().Format(time.RFC3339)] = true
		}
	}
	out := make([]Reminder, 0, len(incoming))
	for _, r := range incoming {
		r.At = r.At.UTC()
		r.Sent = sent[r.TaskID+"\x00"+r.Kind+"\x00"+r.At.Format(time.RFC3339)]
		out = append(out, r)
	}
	return out
}

// dueReminders returns the reminders whose time has come and that were not sent.
func dueReminders(list []Reminder, now time.Time) []Reminder {
	var out []Reminder
	for _, r := range list {
		if !r.Sent && !r.At.After(now) {
			out = append(out, r)
		}
	}
	return out
}

// PushPayload is what the service worker receives in its push event.
type PushPayload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	TaskID    string `json:"taskId"`
	Kind      string `json:"kind"`
	TaskTitle string `json:"taskTitle"`
}

func payloadFor(r Reminder) PushPayload {
	// English fallback; the service worker rewords it in the app language from taskTitle and kind.
	body := "Due: " + r.Title
	if r.Kind == KindFollowUp {
		body = "Check: " + r.Title
	}
	return PushPayload{Title: "Qadrant", Body: body, TaskID: r.TaskID, Kind: r.Kind, TaskTitle: r.Title}
}
