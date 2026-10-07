# Qadrant server

[Leer en castellano](README.es.md)

Optional companion server for [Qadrant](https://github.com/iisra-dev), an Eisenhower matrix agenda that runs on your device. Qadrant offers no server and no accounts: if you want **notices** (push reminders), your **calendar** in the agenda or **the same tasks on all your devices** (sync), you run this server yourself. Without it the app works the same.

Each server belongs to one person and serves all their devices with one access key. It is [PocketBase](https://pocketbase.io) used as a Go framework, with `webpush-go` for notices: a single binary with no dependencies.

## What it stores and who can read it
- **Reminders:** task id, time of the notice, kind (`due` or `follow-up`) and title. Without sync, nothing else about your tasks.
- **Sync (only if you turn it on in the app):** an unencrypted copy of your tasks, goals, people, corrections and shared settings (urgency, working hours, working days and holidays). Each record is kept as opaque content with a version number; the server never reads it, and the app merges the changes. The AI model, its learned classifiers and device settings (theme, language) never leave the devices.
- **Push subscriptions** of your browsers.
- **Calendar (optional):** the secret iCal address and the events of the current window.
- In `pb_data/`: the database, the access key (`qadrant_access_key`) and the VAPID keys (`qadrant_vapid.json`). Do not delete the VAPID keys: every push subscription would stop working.

**Who can read it:** whoever administers the machine and, if you publish it through a tunnel such as Cloudflare Tunnel, the tunnel service (it terminates HTTPS on its network). There is no end-to-end encryption yet; the protocol already treats records as opaque so it can be added later.

## Install
You need a Linux machine that is always on (a small VM, an LXC container, a Raspberry Pi), 512 MB of RAM and a domain name for HTTPS. The app only talks to `https://` addresses.

### Option A: binary with systemd (any Linux)
1. Download `qadrant-server-<version>-linux-amd64` (or `arm64`) and `SHA256SUMS` from the releases, check them (`sha256sum -c SHA256SUMS --ignore-missing`) and rename the binary to `qadrant-server`. Or build it yourself (see [Build](#build)).
2. User and folder:
   ```
   useradd --system --home /opt/qadrant --shell /usr/sbin/nologin qadrant
   mkdir -p /opt/qadrant/pb_data && chown -R qadrant:qadrant /opt/qadrant
   install -m 755 qadrant-server /opt/qadrant/
   ```
3. Copy `qadrant.service` to `/etc/systemd/system/` and edit two lines:
   - `QADRANT_VAPID_SUBJECT`: your own email (`mailto:you@example.com`) or an `https://` page of yours. Push services (Apple, Google, Mozilla) use it to contact the owner of a server that misbehaves.
   - `TZ`: your time zone (for example `Europe/Madrid` or `America/New_York`). All-day calendar events and recurring ones are expanded in it.
4. Start it and read the access key:
   ```
   systemctl daemon-reload && systemctl enable --now qadrant
   journalctl -u qadrant | grep "access key"
   ```
   The key is also in `/opt/qadrant/pb_data/qadrant_access_key`. To choose it yourself, set `QADRANT_ACCESS_KEY` in the service.
5. Optional, to browse the data in the PocketBase dashboard (the app does not use it):
   `sudo -u qadrant /opt/qadrant/qadrant-server superuser upsert you@example.com <password> --dir /opt/qadrant/pb_data`

The server listens only on `127.0.0.1:8090`: nothing is exposed until the tunnel or proxy publishes it.

### Option B: Docker or Podman
The `Dockerfile` in this repository builds a small image (static binary on distroless, running as a non-root user):
```
docker build -t qadrant-server .
docker run -d --name qadrant --restart unless-stopped \
  -p 127.0.0.1:8090:8090 -v qadrant-data:/data \
  -e TZ=Europe/Madrid -e QADRANT_VAPID_SUBJECT=mailto:you@example.com \
  qadrant-server
docker logs qadrant | grep "access key"
```
With a bind mount instead of a named volume, give the folder to the image's user first: `chown 65532:65532 /path/to/data`.

## Publish it over HTTPS
Use a tunnel or a reverse proxy in front of `127.0.0.1:8090`. The event stream used by sync stays open for a long time: the server sends a heartbeat every 30 s, so proxies that close idle responses after 100 s (like Cloudflare's) keep it open.

### Example: Cloudflare Tunnel
1. Install `cloudflared` (Cloudflare's package for your distribution) and log in: `cloudflared tunnel login`.
2. Create the tunnel and its DNS record:
   ```
   cloudflared tunnel create qadrant
   cloudflared tunnel route dns qadrant qadrant.example.com
   ```
3. `/etc/cloudflared/config.yml`:
   ```
   tunnel: qadrant
   credentials-file: /root/.cloudflared/<tunnel-id>.json
   ingress:
     - hostname: qadrant.example.com
       service: http://127.0.0.1:8090
     - service: http_status:404
   ```
4. `cloudflared service install && systemctl enable --now cloudflared`.
5. Check from outside: `curl -H "Authorization: Bearer <key>" https://qadrant.example.com/api/qadrant/ping` answers `{"ok":true,"version":2,…}`.

The PocketBase dashboard (`/_/`) is reachable through the tunnel too. If you do not want it public, protect `/_/*` with Cloudflare Access (or block it in your proxy).

### Other proxies
Any proxy with a valid certificate works (Caddy, nginx, Traefik). Turn off response buffering for `/api/qadrant/sync/events` (nginx: `proxy_buffering off;`); the server already sends `X-Accel-Buffering: no`.

## Connect the app
In Qadrant, Settings > Own server: paste the address and the access key and tap Connect. Then:
- **Notices:** allow them when asked (on iPhone, open the app from the Home Screen first).
- **Sync:** tick "Sync my tasks". On a new device, choose "I already use Qadrant on another device" on the welcome screen instead of creating a goal.
- **Calendar:** paste the secret iCal address of your calendar (Google, iCloud and Outlook offer one).

## Update
Stop the service, replace the binary and start it again; `pb_data/` stays as it is. New collections are created on start.
```
systemctl stop qadrant
install -m 755 qadrant-server /opt/qadrant/
systemctl start qadrant
```

## Back up and empty
- **Backups:** `pb_data/` is all the state. Copy it with the service stopped, or use the scheduled backups of the PocketBase dashboard.
- **Empty the synced data:** `systemctl stop qadrant`, then `sudo -u qadrant /opt/qadrant/qadrant-server sync-reset --dir /opt/qadrant/pb_data`, then start it again. Devices keep their own copy and upload it again on their next sync. Reminders, subscriptions and the calendar are not touched.
- **Leave sync on one device:** untick "Sync my tasks" or remove the server in Settings. Nothing is deleted from the server or the other devices.

## API
All routes need `Authorization: Bearer <key>`.

| Method and route | What it does |
| --- | --- |
| `GET /api/qadrant/ping` | Checks the address and the key. Returns the API version (`2`: with sync) and the `syncId` of the synced data |
| `GET /api/qadrant/vapid` | Public VAPID key to subscribe |
| `POST /api/qadrant/subscriptions` | Saves a subscription (`PushSubscription.toJSON()`) |
| `DELETE /api/qadrant/subscriptions` | Deletes a subscription (`{ "endpoint": … }`) |
| `PUT /api/qadrant/reminders` | Replaces the whole list of reminders. With `?version=N` (syncing devices), lists from an older sync version than the last one received are ignored |
| `DELETE /api/qadrant/reminders/{taskId}` | Deletes the reminders of a task |
| `GET /api/qadrant/calendar` | Calendar state and its events, already expanded (last 7 days and next 30) |
| `PUT /api/qadrant/calendar` | Saves the secret iCal address (`{ "url": … }`, `https://` or `webcal://`) after checking it can be read |
| `DELETE /api/qadrant/calendar` | Removes the calendar |
| `POST /api/qadrant/sync/push` | List of `{ collection, id, baseVersion, content }`. Accepts each record whose version is still `baseVersion` and gives it the next one; the rest come back with the current version and content |
| `GET /api/qadrant/sync/pull?since=N&limit=200` | Records with a version above `N`, in order, with `more` when there are more pages and the highest version |
| `GET /api/qadrant/sync/events` | SSE: a `version` event on connecting and one per change; heartbeat every 30 s |

Every minute a job sends the due reminders to every subscription and marks them as sent. The calendar is downloaded every 15 minutes. `content` is stored as is, never parsed.

## Build
Go 1.27 or later:
```
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags timetzdata -o qadrant-server .
```
`scripts/build-release.sh <version>` builds the release binaries for Linux amd64 and arm64 into `dist/` with their `SHA256SUMS`. Without Go installed:
```
podman run --rm -v "$PWD:/src:Z" -w /src docker.io/library/golang:1.27 scripts/build-release.sh 1.0.0
```

## License
MIT (`LICENSE`). Its dependencies (PocketBase, webpush-go, gocal) have their own licenses, MIT as well.
