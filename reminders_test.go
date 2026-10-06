package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func TestValidateReminders(t *testing.T) {
	ok := []Reminder{{TaskID: "a", Kind: KindDue, At: t0, Title: "Llamar al taller"}, {TaskID: "a", Kind: KindFollowUp, At: t0, Title: "x"}}
	if err := validateReminders(ok); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
	bad := map[string][]Reminder{
		"no task id":  {{Kind: KindDue, At: t0}},
		"kind":        {{TaskID: "a", Kind: "other", At: t0}},
		"no time":     {{TaskID: "a", Kind: KindDue}},
		"long title":  {{TaskID: "a", Kind: KindDue, At: t0, Title: strings.Repeat("ñ", 201)}},
		"duplicated":  {{TaskID: "a", Kind: KindDue, At: t0}, {TaskID: "a", Kind: KindDue, At: t0.Add(time.Hour)}},
		"long taskId": {{TaskID: strings.Repeat("a", 65), Kind: KindDue, At: t0}},
	}
	for name, list := range bad {
		if err := validateReminders(list); !errors.Is(err, errInvalid) {
			t.Errorf("%s: want errInvalid, got %v", name, err)
		}
	}
}

func TestMergeKeepsSentOnlyWhenUnchanged(t *testing.T) {
	existing := []Reminder{
		{TaskID: "a", Kind: KindDue, At: t0, Sent: true},
		{TaskID: "b", Kind: KindDue, At: t0, Sent: true},
	}
	incoming := []Reminder{
		{TaskID: "a", Kind: KindDue, At: t0.In(time.FixedZone("CEST", 7200))},
		{TaskID: "b", Kind: KindDue, At: t0.Add(24 * time.Hour)},
		{TaskID: "c", Kind: KindFollowUp, At: t0},
	}
	got := mergeReminders(existing, incoming)
	if len(got) != 3 || !got[0].Sent || got[1].Sent || got[2].Sent {
		t.Fatalf("unexpected merge: %+v", got)
	}
}

func TestDueReminders(t *testing.T) {
	list := []Reminder{
		{TaskID: "past", At: t0.Add(-time.Minute)},
		{TaskID: "now", At: t0},
		{TaskID: "sent", At: t0.Add(-time.Hour), Sent: true},
		{TaskID: "future", At: t0.Add(time.Minute)},
	}
	got := dueReminders(list, t0)
	if len(got) != 2 || got[0].TaskID != "past" || got[1].TaskID != "now" {
		t.Fatalf("unexpected due list: %+v", got)
	}
}

func TestPayload(t *testing.T) {
	if p := payloadFor(Reminder{TaskID: "a", Kind: KindDue, Title: "Pagar recibo"}); p.Body != "Due: Pagar recibo" || p.TaskTitle != "Pagar recibo" || p.Title != "Qadrant" {
		t.Fatalf("due payload: %+v", p)
	}
	if p := payloadFor(Reminder{TaskID: "a", Kind: KindFollowUp, Title: "Reservar sala"}); p.Body != "Check: Reservar sala" {
		t.Fatalf("follow-up payload: %+v", p)
	}
}
