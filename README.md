# Servidor propio de Qadrant

Opcional. Qadrant no ofrece servidor: quien quiera avisos, calendario o las mismas tareas en todos sus dispositivos (sincronización, fase 4) monta el suyo con estas instrucciones. Sin él la app funciona igual. Cada servidor es de una persona y sirve a todos sus dispositivos con la misma clave.
Es PocketBase usado como framework Go, con `webpush-go` para los avisos. Un único binario sin dependencias.

## Qué guarda
- **Recordatorios:** id de la tarea, momento del aviso, tipo (`due` o `follow-up`) y título. Sin sincronización, nada más de tus tareas.
- **Sincronización (fase 4, solo si la activas en la app):** una copia sin cifrar de tus tareas, objetivos, personas, correcciones y ajustes compartidos. Puede leerla quien administre el servidor y, si lo publicas con un túnel, el servicio del túnel.
- **Suscripciones push** de tus navegadores.
- **Calendario (opcional):** la dirección secreta iCal y los eventos de la ventana actual.
- En `pb_data/`: la base de datos, la clave de acceso (`qadrant_access_key`) y las claves VAPID (`qadrant_vapid.json`). No borres las VAPID: invalidarían todas las suscripciones.

## API (todas con `Authorization: Bearer <clave>`)
| Método y ruta | Qué hace |
| --- | --- |
| `GET /api/qadrant/ping` | Comprueba la URL y la clave |
| `GET /api/qadrant/vapid` | Clave pública VAPID para suscribirse |
| `POST /api/qadrant/subscriptions` | Guarda una suscripción (`PushSubscription.toJSON()`) |
| `DELETE /api/qadrant/subscriptions` | Borra una suscripción (`{ "endpoint": … }`) |
| `PUT /api/qadrant/reminders` | Sustituye la lista completa de recordatorios |
| `DELETE /api/qadrant/reminders/{taskId}` | Borra los recordatorios de una tarea |
| `GET /api/qadrant/calendar` | Estado del calendario y sus eventos ya expandidos (últimos 7 días y próximos 30) |
| `PUT /api/qadrant/calendar` | Guarda la dirección secreta iCal (`{ "url": … }`, `https://` o `webcal://`) tras comprobar que se puede leer |
| `DELETE /api/qadrant/calendar` | Quita el calendario |

El calendario se descarga cada 15 minutos; se expanden las repeticiones (`RRULE`, `EXDATE`) y las zonas horarias con la del servidor (`TZ=Europe/Madrid` en `qadrant.service`). La dirección solo se guarda aquí y nunca vuelve a la app.

Cada minuto, un cron envía los recordatorios vencidos a todas las suscripciones y los marca como enviados.

## Compilar
Necesitas Go 1.27 o posterior (en tu equipo o en el contenedor).
```
cd server
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o qadrant-server .
```

## Instalar en un LXC de Proxmox
1. Crea un contenedor Debian 13 sin privilegios (512 MB de RAM y 2 GB de disco bastan) y entra en él.
2. Usuario y carpeta:
   ```
   useradd --system --home /opt/qadrant --shell /usr/sbin/nologin qadrant
   mkdir -p /opt/qadrant/pb_data && chown -R qadrant:qadrant /opt/qadrant
   ```
3. Copia el binario: `scp qadrant-server root@<ip-del-lxc>:/opt/qadrant/` y `chmod 755 /opt/qadrant/qadrant-server`.
4. Copia `qadrant.service` a `/etc/systemd/system/`, cambia el correo de `QADRANT_VAPID_SUBJECT` y arranca:
   ```
   systemctl daemon-reload && systemctl enable --now qadrant
   journalctl -u qadrant | grep "Clave de acceso"
   ```
   Esa clave es la que se pega en Ajustes de la app. También está en `/opt/qadrant/pb_data/qadrant_access_key`. Si prefieres fijarla tú, define `QADRANT_ACCESS_KEY` en el servicio.
5. Crea el superusuario de PocketBase (solo para mirar los datos; la app no lo usa):
   `sudo -u qadrant /opt/qadrant/qadrant-server superuser upsert tu-correo@example.com <contraseña> --dir /opt/qadrant/pb_data`.

El servidor escucha solo en `127.0.0.1:8090`: nada queda expuesto hasta que lo publique el túnel.

## Publicar con Cloudflare Tunnel
1. En el LXC instala `cloudflared` (paquete de Cloudflare para Debian) y entra: `cloudflared tunnel login`.
2. Crea el túnel y la ruta DNS:
   ```
   cloudflared tunnel create qadrant
   cloudflared tunnel route dns qadrant qadrant.tudominio.es
   ```
3. `/etc/cloudflared/config.yml`:
   ```
   tunnel: qadrant
   credentials-file: /root/.cloudflared/<id-del-tunel>.json
   ingress:
     - hostname: qadrant.tudominio.es
       service: http://127.0.0.1:8090
     - service: http_status:404
   ```
4. `cloudflared service install && systemctl enable --now cloudflared`.
5. Comprueba desde fuera: `curl -H "Authorization: Bearer <clave>" https://qadrant.tudominio.es/api/qadrant/ping` debe responder `{"ok":true,"version":1}`.

El panel de PocketBase (`/_/`) queda accesible a través del túnel con tu superusuario. Si no lo quieres expuesto, añade en Cloudflare Access una regla para `/_/*`.

## Copias de seguridad
`pb_data/` es todo el estado. Con el servicio parado, cópialo; o usa las copias programadas del panel de PocketBase.

## Licencia
MIT (`LICENSE`). Cubre solo este servidor; sus dependencias (PocketBase, webpush-go, gocal) tienen sus propias licencias, también MIT.
