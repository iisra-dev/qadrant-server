package main

import "testing"

func TestSubscriberLeavesMailtoToWebpush(t *testing.T) {
	cases := map[string]string{
		"":                        "admin@localhost",
		"mailto:me@example.com":   "me@example.com",
		" mailto:me@example.com ": "me@example.com",
		"me@example.com":          "me@example.com",
		"https://example.com":     "https://example.com",
	}
	for env, want := range cases {
		t.Setenv("QADRANT_VAPID_SUBJECT", env)
		if got := subscriber(); got != want {
			t.Errorf("QADRANT_VAPID_SUBJECT=%q: subscriber() = %q, want %q", env, got, want)
		}
	}
}

func TestServiceHostKeepsOnlyTheHost(t *testing.T) {
	if got := serviceHost("https://web.push.apple.com/QGx-secret-token"); got != "web.push.apple.com" {
		t.Errorf("serviceHost = %q", got)
	}
}
