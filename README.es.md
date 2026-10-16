# kunveno

[English](README.md) · [Русский](README.ru.md) · Español

Videoconferencias en tu propio servidor para la red de una empresa: voz, cámaras y pantalla
compartida en el navegador, con un pequeño servidor en Go que ejecutas tú mismo. En esperanto,
«kunveno» significa «reunión».

Pensado para una oficina o una VPN donde se quieren llamadas sin servicios de terceros: sin
cuentas, sin servidores STUN ni TURN, sin CDN y sin analítica. Una sala es un nombre y un PIN;
todo corre en un solo equipo detrás de nginx.

Licencia GNU Affero General Public License v3.0 only; hay disponible una licencia comercial
(ver [Licencia](#licencia)).

![Una sala con seis participantes](docs/images/room.png)

## Funciones

- Salas con PIN, definidas en la configuración o creadas desde la página de inicio (se guardan en
  el servidor). Hasta 50 personas por sala (15 por defecto).
- Audio de todos, cámaras (640x360, 15 fps), una pantalla compartida a la vez.
- Comprobación antes de entrar: elección de micrófono y cámara, medidor de nivel, y entrar con el
  micrófono o la cámara apagados. Funciona en equipos sin cámara.
- Fondos virtuales, procesados en el navegador: desenfoque, bloques de vidrio, imágenes que el
  administrador pone en `backgrounds/`, o una imagen propia que no sale del navegador. Supresión
  de ruido del micrófono (teclado, sonidos de fondo), también en el navegador.
- Enlaces a una sala (`https://<servidor>/join/<sala>#pin=…`), copiados desde la página de inicio
  o desde dentro de una sala: quien lo recibe llega a la comprobación previa con la sala y el PIN ya
  rellenados. El PIN va en el fragmento de la dirección, que el navegador no envía a ningún sitio:
  nunca llega al servidor ni a sus registros.
- Un indicador de calidad de conexión en cada participante (pérdidas, tiempo de ida y vuelta y
  variación del retardo, medidos en el servidor).
- Una prueba de eco por sala, salas que reproducen un archivo de vídeo en bucle y salas que
  muestran una cámara RTSP en directo.
- Notas de la reunión con el complemento comercial `kunveno-notes`: el habla de cada participante se
  reconoce en el servidor y el texto de la última reunión de la sala se descarga en Markdown (véase
  [Notas de la reunión](#notas-de-la-reunión)).
- Grabación de la reunión con el complemento comercial `kunveno-recorder`: la pantalla compartida y
  el audio de todos en un único vídeo MP4, que se descarga desde la sala o desde la página de inicio
  (véase [Grabación de la reunión](#grabación-de-la-reunión)).
- Funciona en redes difíciles: si a un participante le falla UDP, el navegador pasa a ICE sobre
  TCP en el mismo puerto.
- Interfaz en ruso (por defecto), inglés y español; el título de la instalación es configurable.
- Protección contra la adivinación del PIN, métricas de Prometheus con un panel de Grafana,
  registros en el diario del sistema.

![La página de inicio: salas y comprobación antes de entrar](docs/images/lobby.png)

## Instalación

Necesitas un servidor Linux con Docker al que tus usuarios lleguen por la red.

**1. Descarga y descomprime** el paquete:

```bash
curl -fsSL https://github.com/ssubbotin/kunveno/releases/latest/download/kunveno.tgz | tar xz
cd kunveno
sudo chown -R 10001:10001 data && sudo chgrp 10001 config.yaml && chmod 640 config.yaml
```

Los servicios se ejecutan como el usuario 10001, nunca como root: la última línea les da `data/` y
les deja leer `config.yaml` (lo sigue editando usted).

El directorio es la instalación completa:

```
docker-compose.yml
.env            ajustes: rellena dos líneas, el resto es opcional
config.yaml     salas, PIN y título; los valores por defecto funcionan tal cual
data/           salas creadas desde la página de inicio, caché del reproductor
certs/          certificado TLS: autofirmado en el primer arranque, o el tuyo
backgrounds/    imágenes que se ofrecen como fondos virtuales
media/          archivos de vídeo para salas con reproductor
grafana/        fuente de datos y panel de Grafana
```

**2. Rellena dos líneas en `.env`:**

```bash
PUBLIC_HOST=10.0.0.5            # la dirección o el nombre del servidor, tal como lo escriben los usuarios
GRAFANA_ADMIN_PASSWORD=...      # cualquier contraseña fuerte; genera una con: openssl rand -base64 24
```

**3. Arranca:**

```bash
docker compose pull && docker compose up -d
```

**4. Abre `https://<PUBLIC_HOST>/`** y entra en una sala con su PIN de `config.yaml`. El
certificado es autofirmado, así que cada navegador pedirá aceptarlo una vez.

**5. Abre el cortafuegos** para tus usuarios: TCP 443 (web) y el puerto 7000 por UDP y TCP (audio y
vídeo).

### Tareas habituales

| Tarea | Cómo |
|---|---|
| Aplicar cambios de `config.yaml` | `docker compose restart server player` |
| Leer los registros | `journalctl -t kunveno-server -f` |
| Supervisión | `https://<PUBLIC_HOST>/grafana/`, usuario `admin`, la contraseña de `.env`. En un servidor expuesto a internet, defina `GRAFANA_WALL_PASSWORD` en `.env`: el navegador la pedirá (usuario `GRAFANA_WALL_USER`, `admin` por defecto) antes del inicio de sesión de Grafana |
| Usar tu propio certificado | sustituye `certs/fullchain.pem` y `certs/privkey.pem`, luego `docker compose restart web` |
| Copia de seguridad | copia el directorio (`data/` pertenece al usuario 10001, así que con `sudo`) |

**Actualizar** a una nueva versión, dentro del directorio:

```bash
docker compose pull && docker compose up -d
```

La pila usa las imágenes `latest`, así que esto descarga la nueva versión y reinicia solo los
contenedores cuya imagen cambió; `.env`, `config.yaml` y los datos se conservan. Para quedarte en
una versión, pon por ejemplo `KUNVENO_VERSION=0.2.4` en `.env`. Si viene de una versión anterior a
0.4.5 (cuando los servicios corrían como root), ejecute una vez antes de arrancar: `sudo chown -R 10001:10001 data && sudo chgrp 10001 config.yaml && chmod 640 config.yaml`.

### Si algo no funciona

- **Los usuarios entran pero no oyen ni ven nada:** el servidor eligió una dirección equivocada
  para el audio y el vídeo. Añade `KUNVENO_ADVERTISE_IP=<la dirección a la que llegan los usuarios,
  por ejemplo la de la VPN>` a `.env` y ejecuta `docker compose up -d`.
- **Los puertos 80 o 443 están ocupados:** define `HTTP_PORT` y `HTTPS_PORT` en `.env`.
- **El servidor se detiene con «is not writable by the server» o «permission denied» en
  `config.yaml`:** ejecute `sudo chown -R 10001:10001 data && sudo chgrp 10001 config.yaml && chmod 640 config.yaml` en el directorio y arranque de nuevo.
- **El servidor se detiene con «config.yaml is a directory»:** el archivo desapareció y Docker creó
  un directorio en su lugar; borra el directorio, restaura `config.yaml` desde el paquete y arranca
  de nuevo.

Todos los ajustes de `.env` están descritos en el propio `.env`; los de `config.yaml`, en
[Configuración](#configuración). Certificados, cortafuegos, resolución de problemas y paquetes Debian:
[docs/deploy.md](docs/deploy.md) (en inglés).

## Para administradores

**Por dónde va el tráfico.** kunveno es una unidad de reenvío selectivo (SFU): cada participante
envía al servidor un flujo por tipo (voz, cámara, pantalla) y el servidor envía copias a los demás.
Nada viaja directamente entre participantes, así que la subida de cada participante es pequeña y la
carga recae en el enlace de red del servidor.

**Cifrado.** El audio y el vídeo van cifrados con DTLS-SRTP entre cada navegador y el servidor, de
modo que por la red todo viaja cifrado. El servidor descifra los flujos para reenviarlos; no hay
cifrado de extremo a extremo entre participantes. El sitio y la señalización van por HTTPS.

**Ancho de banda.** Por participante, aproximadamente: voz unos 40 kbit/s, cámara unos 350 kbit/s,
pantalla compartida hasta 2,5 Mbit/s (`media.share_max_kbps`). El servidor recibe un flujo de cada
tipo por participante y envía (N − 1) copias de cada uno. Para 15 personas con cámara y una
pantalla compartida son unos 120 Mbit/s de salida del servidor en el peor caso; una reunión del
mismo tamaño solo con voz necesita menos de 10 Mbit/s.

**Puertos.** Todo corre en el mismo equipo:

| Puerto | Qué lleva |
|---|---|
| TCP 443 | el sitio (HTTPS) y la señalización (WebSocket a través de nginx) |
| TCP 80 | solo una redirección a 443 |
| UDP 7000 | el canal de audio y vídeo: voz y vídeo de todos los participantes en un solo puerto; no se abren puertos por llamada |
| TCP 7000 | el canal de reserva para un participante cuyo UDP no pasa (normalmente un NAT o un cortafuegos en una pasarela que corta los flujos UDP largos); el navegador cambia a él por sí solo y lo recuerda una semana, y «probar el rápido» junto al estado vuelve a UDP |

No hace falta STUN, TURN ni ningún servicio externo. Los puertos internos del servidor (API,
métricas, Prometheus, Grafana, node-exporter) escuchan solo en 127.0.0.1; a Grafana se llega por el mismo 443 en
`/grafana/`.
La fila «Заметки» del panel (plegada) pertenece al complemento opcional kunveno-notes, que añade su
destino de Prometheus mediante `/etc/prometheus/targets/*.json`; sin él no hay destino y la fila queda vacía.
La fila «Запись» (plegada) funciona igual para el complemento kunveno-recorder.

**Regla del cortafuegos.** Permitir desde las redes de los usuarios hacia el servidor: TCP 443 (y
80), UDP 7000 y TCP 7000. Si un segmento de red solo puede usar TCP 7000, dejar pasar su UDP 7000
da a esos usuarios el camino rápido.

**Compartir pantalla** es una función propia del navegador (`getDisplayMedia`); no se instala nada
en los equipos de los usuarios. Funciona en Chrome y Firefox en Linux y Windows.

## Notas de la reunión

Un participante puede activar las notas en una sala (Más → Tomar notas). El bot de notas entra
entonces en la sala como un participante llamado «Notas», escucha el micrófono de cada persona por
separado y envía las frases reconocidas al servidor, que guarda el texto: quién dijo qué y cuándo.
Todos ven «Notas» con un icono de hoja en la cabecera de la sala. Cuando se desactivan las notas o
sale la última persona, quienes siguen en la sala reciben la barra «Listo tras la reunión:» con un
botón «Notas» que las descarga; en la página de inicio el mismo archivo está disponible en «Última
reunión», con el PIN de la sala. El archivo es Markdown: la sala, la fecha y la hora, quién habló y
las frases en orden, con las pausas marcadas.

- El bot es el complemento comercial `kunveno-notes` (reconocimiento de voz en el procesador del
  servidor, sin servicios externos y sin guardar el audio). No forma parte de este repositorio; para
  una licencia escribe a ssubbotin@gmail.com. La guía de instalación acompaña a la licencia.
- Sin el complemento deja `notes.enabled: false` (por defecto): las salas y la página de inicio no
  muestran nada de las notas.
- Solo se guardan las notas de la última reunión de cada sala, durante `notes.keep` desde que las
  notas terminan (una desactivación o el final de la reunión; un día por defecto); la primera frase
  de la reunión siguiente las sustituye. Eliminar una sala elimina sus notas. Descargarlas exige el
  PIN de la sala, y los PIN erróneos cuentan para el mismo bloqueo que al entrar.
- Las notas se pueden eliminar antes desde la página de inicio: «Última reunión» → «Eliminar…» con
  el PIN de la sala, y luego «Notas» (o «Todo») en la confirmación. Mientras se toman notas no se pueden
  eliminar: primero hay que desactivarlas.
- `notes.live: true` muestra las frases en la sala a medida que se reconocen, en el recuadro del
  participante «Notas», para comprobar el reconocimiento.

## Grabación de la reunión

Un participante puede activar la grabación en una sala (Más → Grabación). El bot de
grabación entra entonces en la sala como un participante llamado «Grabación» y graba la pantalla
compartida y el audio de todos en un único vídeo (1920x1080, 15 fotogramas por segundo, H.264 y
AAC). Mientras nadie comparte pantalla, el vídeo muestra la cámara de quien está hablando o, si la
tiene apagada, una pantalla de espera con el nombre de la sala, la fecha y su nombre. Las demás
cámaras no se graban. Todos ven «● Grabando» en la
cabecera de la sala; el punto rojo siempre se refiere a este vídeo. Cuando se desactiva la grabación
o sale la última persona, el bot sube el archivo al servidor y quienes siguen en la sala reciben un
botón «Vídeo» en la barra «Listo tras la reunión:» (junto a «Notas» si la reunión también tuvo
notas, con un solo botón de cierre para ambos); en la página de inicio el mismo archivo está
disponible en «Última reunión», con el PIN de la sala.

La página de inicio muestra «Última reunión» bajo una sala que guarda un vídeo, notas o ambos, con
lo que hay y el vencimiento más próximo («Vídeo y notas, se guardan hasta 16/10, 14:43»). Su
ventana pide el PIN de la sala una vez y muestra cada elemento con su propio plazo y un botón
«Descargar». Encima del texto y en la ventana se indica cuándo fue esa reunión, cuánto duró y
cuántas personas participaron («15/10, 11:00–11:27 · 27 min · 7 participantes»; sin nombres),
mientras se guardan sus notas o su grabación.

- El bot es el complemento comercial `kunveno-recorder`. No forma parte de este repositorio; para
  una licencia escribe a ssubbotin@gmail.com. La guía de instalación acompaña a la licencia.
- Sin el complemento deja `recording.enabled: false` (por defecto): las salas y la página de inicio
  no muestran nada de la grabación.
- Solo se guarda la grabación de la última reunión de cada sala, durante `recording.keep` desde que
  termina (un día por defecto); la grabación siguiente la sustituye. Volver a activar la grabación en la misma reunión también
  sustituye lo grabado antes: una reunión tiene una sola grabación y gana la posterior. Eliminar una
  sala elimina su grabación. Descargarla exige el PIN de la sala, y los PIN erróneos cuentan para el
  mismo bloqueo que al entrar. Una descarga interrumpida se puede reanudar mientras el enlace sea
  válido (5 minutos); después hay que empezar la descarga de nuevo desde la sala o la página de inicio.
- La grabación se puede eliminar antes desde la página de inicio: «Última reunión» → «Eliminar…»
  con el PIN de la sala, y luego «Vídeo» (o «Todo») en la confirmación. Una grabación en curso no se puede eliminar:
  primero hay que desactivarla.
- Un archivo mayor que `recording.max_size` (8 GB por defecto) lo rechaza el servidor y el archivo se queda
  en el volumen del complemento;
  conviene fijar el límite según la reunión más larga y dejar espacio libre en el disco.

## Cómo funciona

![Cómo encaja kunveno](docs/images/architecture.svg)

- `kunveno-server` (Go, [pion/webrtc](https://github.com/pion/webrtc)) es una unidad de reenvío
  selectivo: cada participante envía un flujo por tipo (audio, pantalla, cámara) y el servidor lo
  reenvía a todos los demás. ICE funciona en modo lite con un único candidato
  (`media.advertise_ip`), un puerto UDP para todo el audio y vídeo y el mismo puerto por TCP como
  reserva.
- Cada navegador mantiene una `RTCPeerConnection` con tres ranuras de envío fijas (audio, pantalla,
  cámara); encender o apagar la cámara o la pantalla sustituye la pista sin renegociar. El servidor
  es el único que hace ofertas SDP.
- El cliente web (Svelte 5, Vite, TypeScript) es un paquete estático servido por nginx, que además
  termina TLS y redirige `/ws` y `/api/` al servidor por loopback.
- `kunveno-echo` y `kunveno-player` son ayudantes que entran en las salas como participantes por
  loopback: el eco reproduce el sonido de la sala con retardo cuando alguien lo pide; el
  reproductor muestra un archivo de vídeo en bucle o una cámara RTSP en directo en su propia
  casilla de cámara (el directo va marcado). Usa ffmpeg: un archivo se transcodifica una vez a VP8
  y Opus y se guarda en caché; un directo se transcodifica al vuelo y se reconecta cada 5 s si se
  corta.

Certificados, cortafuegos, resolución de problemas y los ayudantes en detalle: [docs/deploy.md](docs/deploy.md)
(en inglés).

## Estructura del repositorio

| Ruta | Qué hay |
|---|---|
| `cmd/kunveno-server` | el proceso del servidor: configuración, HTTP, señalización, audio y vídeo |
| `cmd/kunveno-echo`, `cmd/kunveno-player` | el eco y el reproductor de archivos de vídeo y cámaras RTSP |
| `internal/room`, `internal/conference`, `internal/signal`, `internal/sfu` | estado de las salas, reuniones, señalización por WebSocket, la SFU |
| `internal/config`, `internal/httpapi`, `internal/metrics` | configuración, API HTTP, métricas de Prometheus |
| `internal/quality`, `internal/ratelimit`, `internal/roomstore` | calidad de conexión, límite de intentos de PIN, salas guardadas |
| `internal/echo`, `internal/player`, `pkg/botclient` | interior de los ayudantes |
| `web/` | el cliente para el navegador |
| `deploy/` | imágenes Docker y directorio compose, nginx, unidad systemd, paquetes deb, configuración de ejemplo |
| `tools/loadgen` | participantes sintéticos para pruebas de carga y de extremo a extremo |
| `docs/` | guía de operación, comprobaciones manuales antes de cada versión, el diagrama y las capturas |

## Desarrollo

Requisitos: Go 1.26, Node 22 y ffmpeg para las pruebas del reproductor (se omiten sin él).

```bash
make test        # go vet + go test -race, y las pruebas del cliente web
make build       # bin/kunveno-server y web/dist
make fmt         # gofmt + go mod tidy

go test ./internal/sfu -run TestCameraFlowsBetweenTwoPeers -v    # una prueba de Go
cd web && npx vitest run src/lib/pins.test.ts                    # un archivo de pruebas web
cd web && npm run check                                          # svelte-check
```

Ejecución local: copia `deploy/kunveno/config.yaml`, pon en `media.advertise_ip` la dirección de la
máquina en la red local (los navegadores no recogen candidatos de loopback, así que con `127.0.0.1`
no hay audio ni vídeo), arranca `bin/kunveno-server -config <archivo>` y luego
`cd web && npm run dev` (Vite redirige `/ws` y `/api/` a `127.0.0.1:8080`).
`go run ./tools/loadgen -n 14 -duration 30s` añade participantes sintéticos (`-camera <imagen o
directorio>` les da cámaras con una imagen fija, `-names a,b,c` les pone nombre; requiere ffmpeg). Antes de una versión,
repasa las comprobaciones manuales de [docs/test-matrix.md](docs/test-matrix.md).

## Configuración

`config.yaml` (se lee al arrancar; el servidor rechaza claves desconocidas):

| Clave | Significado |
|---|---|
| `http.listen` | dirección a la que redirige nginx, en loopback (por defecto `KUNVENO_UPSTREAM` de `.env`, si no `127.0.0.1:8080`) |
| `media.advertise_ip` | la dirección a la que los navegadores envían audio y vídeo (por defecto `KUNVENO_ADVERTISE_IP` de `.env`, si no la dirección IPv4 principal del equipo) |
| `media.udp_port`, `media.tcp_port` | puertos de audio y vídeo (7000 por defecto; `tcp_port: -1` desactiva la reserva por TCP) |
| `media.share_max_kbps` | tope de bitrate de la pantalla compartida |
| `media.reconnect_grace` | segundos que el cliente espera a que se recupere una conexión de medios caída antes de volver a entrar (3..120; 15 por defecto). Los cortes más breves congelan el audio y el vídeo (cámaras, pantalla compartida) |
| `ui.title` | título que se muestra en la página de inicio y en la pestaña del navegador |
| `ui.reactions` | emojis que los participantes pueden enviar como reacciones (1..16; diez por defecto) |
| `ui.noise_suppression` | `true` activa la supresión de ruido (GTCRN) del cliente para quien no haya elegido (por defecto `false`) |
| `rooms[]` | `slug`, `name`, `pin`, `max_participants` y, opcionalmente, `player: {file, name}` para un vídeo en bucle del directorio media o `player: {url, name}` para una cámara en directo (`rtsp://` o `rtsps://`; las credenciales de la URL se ocultan en los registros) |
| `rooms_file` | dónde se guardan las salas creadas desde la página de inicio (`/var/lib/kunveno/rooms.json`) |
| `backgrounds_dir` | carpeta con las imágenes de fondo virtual (`/var/lib/kunveno/backgrounds`; el stack monta ahí `backgrounds/`) |
| `notes.enabled` | `true` ofrece notas de la reunión en las salas; necesita el complemento `kunveno-notes` (por defecto `false`, que oculta todo lo relativo a las notas) |
| `notes.keep` | cuánto se guardan las notas tras terminar: un número entero y `m`, `h` o `d` (`90m`, `12h`, `1d`, `2d`; por defecto `1d`) |
| `notes.live` | `true` muestra las frases en la sala a medida que se reconocen, para comprobar el reconocimiento (por defecto `false`) |
| `notes.dir` | dónde se guardan las notas (`/var/lib/kunveno/notes`, que en el stack es `data/notes`) |
| `recording.enabled` | `true` ofrece la grabación de la reunión en las salas; necesita el complemento `kunveno-recorder` (por defecto `false`, que oculta todo lo relativo a la grabación) |
| `recording.keep` | cuánto se guarda una grabación tras terminar: un número entero y `m`, `h` o `d` (`90m`, `12h`, `1d`, `2d`; por defecto `1d`) |
| `recording.dir` | dónde se guardan las grabaciones (`/var/lib/kunveno/recordings`, que en el stack es `data/recordings`) |
| `recording.max_size` | la grabación más grande que acepta el servidor: un número entero y `MB` o `GB` (`500MB`, `8GB`; por defecto `8GB`) |

`.env`, junto al archivo compose, fija la versión (`KUNVENO_VERSION`), los puertos, el nombre
público del equipo para Grafana y su contraseña de administrador.

## Operación

- Registros: `journalctl -t kunveno-server --since "7 days ago" -o cat` (también `kunveno-web`,
  `kunveno-echo`, `kunveno-player`, `kunveno-prometheus`, `kunveno-grafana`, `kunveno-node-exporter`). Cada conexión
  registra su ruta de audio y vídeo (UDP o TCP), los cambios de calidad y los PIN erróneos.
- Métricas: `/metrics` en el servidor (solo loopback); Grafana en `https://<servidor>/grafana/`
  con el panel «kunveno» ya provisto.
- Datos que conservar en el servidor, todos junto a `docker-compose.yml`: `config.yaml`, `.env`,
  `data/` (salas creadas desde la página de inicio, caché del reproductor), `certs/` y `media/`.
- Una cámara RTSP debe ser alcanzable desde el servidor; el reproductor la recoge por TCP. Mientras
  el directo esté caído, el reproductor sigue en la sala sin vídeo.

## Versiones

Las versiones son etiquetas git `vX.Y.Z` en los commits de fusión en `main`; la versión aparece
en el pie de la página de inicio y en la métrica `kunveno_build_info`. Una etiqueta publica las
imágenes `ghcr.io/ssubbotin/kunveno-{server,web,echo,player}` como `X.Y.Z` y `latest`, y una
versión en GitHub con `kunveno.tgz`, el directorio de instalación con `docker-compose.yml` fijado a
esa versión. `main` publica `main`, y un pull request de este repositorio publica una vista previa
`pr-<número>` (`KUNVENO_VERSION=pr-<número>` en `.env` para probarla).

## Licencia

Copyright (C) 2026 Sergey Subbotin <ssubbotin@gmail.com>

kunveno es software libre: puedes redistribuirlo y modificarlo bajo los términos de la
[GNU Affero General Public License, versión 3](LICENSE) (`AGPL-3.0-only`). En resumen: puedes
ejecutarlo, estudiarlo, modificarlo y compartirlo; si ejecutas una versión modificada como servicio
de red, debes ofrecer a sus usuarios el código fuente de esa versión.

Hay una licencia comercial para organizaciones que no pueden aceptar los términos de la AGPL, por
ejemplo para incluir kunveno en un producto propietario, mantener privados sus cambios u ofrecer
kunveno como servicio alojado o revendido sin publicar sus cambios. Los términos comerciales
incluyen soporte. Escribe a ssubbotin@gmail.com.

El nombre «kunveno» identifica el proyecto original; los forks deben usar otro nombre.
