# Servidor de Qadrant

[Read in English](README.md)

Servidor opcional para [Qadrant](https://github.com/iisra-dev), una agenda basada en la matriz de Eisenhower que funciona en tu dispositivo. Qadrant no ofrece servidor ni cuentas: si quieres **avisos**, tu **calendario** en la agenda o **las mismas tareas en todos tus dispositivos** (sincronización), montas tú este servidor. Sin él, la app funciona igual.

Cada servidor es de una persona y sirve a todos sus dispositivos con una sola clave de acceso. Es [PocketBase](https://pocketbase.io) usado como framework Go, con `webpush-go` para los avisos: un único binario sin dependencias.

## Qué guarda y quién puede leerlo
- **Recordatorios:** id de la tarea, momento del aviso, tipo (`due` o `follow-up`) y título. Sin sincronización, nada más de tus tareas.
- **Sincronización (solo si la activas en la app):** una copia sin cifrar de tus tareas, objetivos, personas, correcciones y ajustes compartidos (urgencia, horario, días laborables y festivos). Cada registro se guarda como contenido opaco con un número de versión; el servidor no lo lee y la app fusiona los cambios. El modelo de IA, lo que aprende y los ajustes del dispositivo (tema, idioma) no salen de los dispositivos.
- **Suscripciones push** de tus navegadores.
- **Calendario (opcional):** la dirección secreta iCal y los eventos de la ventana actual.
- En `pb_data/`: la base de datos, la clave de acceso (`qadrant_access_key`) y las claves VAPID (`qadrant_vapid.json`). No borres las VAPID: dejarían de funcionar todas las suscripciones.

**Quién puede leerlo:** quien administre la máquina y, si lo publicas con un túnel como Cloudflare Tunnel, el servicio del túnel (descifra el HTTPS en su red). Todavía no hay cifrado de extremo a extremo; el protocolo ya trata los registros como opacos para poder añadirlo más adelante.

## Instalar
Necesitas una máquina Linux siempre encendida (una VM pequeña, un contenedor LXC, una Raspberry Pi), 512 MB de RAM y un dominio para el HTTPS. La app solo habla con direcciones `https://`.

### Opción A: binario con systemd (cualquier Linux)
1. Descarga `qadrant-server-<versión>-linux-amd64` (o `arm64`) y `SHA256SUMS` de las versiones publicadas, compruébalos (`sha256sum -c SHA256SUMS --ignore-missing`) y renombra el binario a `qadrant-server`. O compílalo tú (ver [Compilar](#compilar)).
2. Usuario y carpeta:
   ```
   useradd --system --home /opt/qadrant --shell /usr/sbin/nologin qadrant
   mkdir -p /opt/qadrant/pb_data && chown -R qadrant:qadrant /opt/qadrant
   install -m 755 qadrant-server /opt/qadrant/
   ```
3. Copia `qadrant.service` a `/etc/systemd/system/` y cambia dos líneas:
   - `QADRANT_VAPID_SUBJECT`: tu correo (`mailto:tu@example.com`) o una página `https://` tuya. Los servicios de push (Apple, Google, Mozilla) lo usan para contactar con quien administra un servidor que se porta mal.
   - `TZ`: tu zona horaria (por ejemplo `Europe/Madrid` o `America/Mexico_City`). En ella se expanden los eventos de día completo y los que se repiten.
4. Arranca y lee la clave de acceso:
   ```
   systemctl daemon-reload && systemctl enable --now qadrant
   journalctl -u qadrant | grep "access key"
   ```
   También está en `/opt/qadrant/pb_data/qadrant_access_key`. Si prefieres fijarla tú, define `QADRANT_ACCESS_KEY` en el servicio.
5. Opcional, para mirar los datos en el panel de PocketBase (la app no lo usa):
   `sudo -u qadrant /opt/qadrant/qadrant-server superuser upsert tu@example.com <contraseña> --dir /opt/qadrant/pb_data`

El servidor escucha solo en `127.0.0.1:8090`: nada queda expuesto hasta que lo publique el túnel o el proxy.

### Opción B: Docker o Podman
El `Dockerfile` del repositorio crea una imagen pequeña (binario estático sobre distroless, con un usuario sin privilegios):
```
docker build -t qadrant-server .
docker run -d --name qadrant --restart unless-stopped \
  -p 127.0.0.1:8090:8090 -v qadrant-data:/data \
  -e TZ=Europe/Madrid -e QADRANT_VAPID_SUBJECT=mailto:tu@example.com \
  qadrant-server
docker logs qadrant | grep "access key"
```
Si montas una carpeta en lugar de un volumen con nombre, dásela antes al usuario de la imagen: `chown 65532:65532 /ruta/a/los/datos`.

## Publicarlo con HTTPS
Pon un túnel o un proxy inverso delante de `127.0.0.1:8090`. El aviso de cambios de la sincronización deja una respuesta abierta mucho tiempo: el servidor envía un latido cada 30 s para que los proxies que cortan las respuestas inactivas a los 100 s (como el de Cloudflare) no la cierren.

### Ejemplo: Cloudflare Tunnel
1. Instala `cloudflared` (el paquete de Cloudflare para tu distribución) y entra: `cloudflared tunnel login`.
2. Crea el túnel y su registro DNS:
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
5. Comprueba desde fuera: `curl -H "Authorization: Bearer <clave>" https://qadrant.tudominio.es/api/qadrant/ping` responde `{"ok":true,"version":2,…}`.

El panel de PocketBase (`/_/`) también queda accesible por el túnel. Si no lo quieres público, protege `/_/*` con Cloudflare Access (o bloquéalo en tu proxy).

### Otros proxies
Sirve cualquiera con un certificado válido (Caddy, nginx, Traefik). Desactiva el búfer de respuestas en `/api/qadrant/sync/events` (nginx: `proxy_buffering off;`); el servidor ya envía `X-Accel-Buffering: no`.

## Conectar la app
En Qadrant, Ajustes > Servidor propio: pega la dirección y la clave de acceso y pulsa Conectar. Después:
- **Avisos:** permítelos cuando los pida (en iPhone, abre antes la app desde la pantalla de inicio).
- **Sincronización:** marca «Sincronizar mis tareas». En un dispositivo nuevo, elige en la bienvenida «Ya uso Qadrant en otro dispositivo» en lugar de crear un objetivo.
- **Calendario:** pega la dirección secreta iCal de tu calendario (Google, iCloud y Outlook la ofrecen).

## Actualizar
Para el servicio, sustituye el binario y vuelve a arrancarlo; `pb_data/` se queda como está. Las colecciones nuevas se crean al arrancar.
```
systemctl stop qadrant
install -m 755 qadrant-server /opt/qadrant/
systemctl start qadrant
```

## Copias de seguridad y vaciado
- **Copias:** `pb_data/` es todo el estado. Cópialo con el servicio parado, o usa las copias programadas del panel de PocketBase.
- **Vaciar los datos sincronizados:** `systemctl stop qadrant`, después `sudo -u qadrant /opt/qadrant/qadrant-server sync-reset --dir /opt/qadrant/pb_data` y vuelve a arrancarlo. Los dispositivos conservan su copia y la vuelven a subir en su próxima sincronización. No toca recordatorios, suscripciones ni calendario.
- **Sacar un dispositivo de la sincronización:** desmarca «Sincronizar mis tareas» o quita el servidor en Ajustes. No se borra nada del servidor ni de los demás dispositivos.

## API
Todas las rutas llevan `Authorization: Bearer <clave>`. La tabla completa, con cada ruta, está en el [README en inglés](README.md#api).

## Compilar
Go 1.27 o posterior:
```
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags timetzdata -o qadrant-server .
```
`scripts/build-release.sh <versión>` compila los binarios de Linux amd64 y arm64 en `dist/` con su `SHA256SUMS`. Sin Go instalado:
```
podman run --rm -v "$PWD:/src:Z" -w /src docker.io/library/golang:1.27 scripts/build-release.sh 1.0.0
```

## Licencia
MIT (`LICENSE`). Sus dependencias (PocketBase, webpush-go, gocal) tienen sus propias licencias, también MIT.
