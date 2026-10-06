package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/SherClockHolmes/webpush-go"
)

const accessKeyEnv = "QADRANT_ACCESS_KEY"

// loadOrCreateAccessKey returns the key every call must carry. It comes from
// QADRANT_ACCESS_KEY or is generated once and kept in the data directory.
func loadOrCreateAccessKey(dataDir string) (key string, created bool, err error) {
	if env := strings.TrimSpace(os.Getenv(accessKeyEnv)); env != "" {
		return env, false, nil
	}
	path := filepath.Join(dataDir, "qadrant_access_key")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), false, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, err
	}
	key = base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", false, err
	}
	return key, true, os.WriteFile(path, []byte(key+"\n"), 0o600)
}

// authorized checks "Authorization: Bearer <key>" in constant time.
func authorized(header, key string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(key)) == 1
}

type vapidKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// loadOrCreateVAPID keeps the VAPID key pair in the data directory: changing
// it would invalidate every push subscription.
func loadOrCreateVAPID(dataDir string) (vapidKeys, error) {
	path := filepath.Join(dataDir, "qadrant_vapid.json")
	var keys vapidKeys
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &keys); err == nil && keys.Public != "" && keys.Private != "" {
			return keys, nil
		}
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return keys, err
	}
	keys = vapidKeys{Public: public, Private: private}
	b, _ := json.Marshal(keys)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return keys, err
	}
	return keys, os.WriteFile(path, b, 0o600)
}
