package main

import (
	"testing"
)

func TestAuthorized(t *testing.T) {
	cases := map[string]bool{
		"Bearer secret":  true,
		"Bearer  secret": true,
		"Bearer other":   false,
		"secret":         false,
		"":               false,
		"Basic secret":   false,
	}
	for header, want := range cases {
		if got := authorized(header, "secret"); got != want {
			t.Errorf("%q: got %v, want %v", header, got, want)
		}
	}
	if authorized("Bearer ", "") {
		t.Error("an empty key must never authorize")
	}
}

func TestAccessKeyIsGeneratedOnceAndKept(t *testing.T) {
	t.Setenv(accessKeyEnv, "")
	dir := t.TempDir()
	first, created, err := loadOrCreateAccessKey(dir)
	if err != nil || !created || len(first) < 40 {
		t.Fatalf("first key: %q created=%v err=%v", first, created, err)
	}
	second, created, err := loadOrCreateAccessKey(dir)
	if err != nil || created || second != first {
		t.Fatalf("second key: %q created=%v err=%v", second, created, err)
	}
	t.Setenv(accessKeyEnv, "from-env")
	if env, _, _ := loadOrCreateAccessKey(dir); env != "from-env" {
		t.Fatalf("env key ignored: %q", env)
	}
}

func TestVAPIDKeysAreKept(t *testing.T) {
	dir := t.TempDir()
	a, err := loadOrCreateVAPID(dir)
	if err != nil || a.Public == "" {
		t.Fatalf("vapid: %+v %v", a, err)
	}
	b, _ := loadOrCreateVAPID(dir)
	if a != b {
		t.Fatal("VAPID keys changed between starts")
	}
}
