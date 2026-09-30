# SPEC v3 — Alertas con estado, vía la API de Moodle

Rama `feat/api-moodle` desde `main` @ `bd2c07b`. Fecha 2026-09-29.

## Cambios v2 → v3 (pedido del PO, 2026-09-29)

El PO quiere que, cuando el token vence y el bot no lo puede renovar solo, se lo pida por
Telegram. Hoy eso no pasa: si el re-login falla en la ronda automática el error solo va al
log (`notifier.go:52`) y el usuario deja de recibir avisos sin enterarse. Además `/tps`
responde "No tienes credenciales válidas" ante CUALQUIER error (`main.go:217`), incluso con
PEDCO caído, y `/login` guarda la contraseña sin probarla. Se agregan la sección
"Credenciales rechazadas" y los criterios C17–C20; sale de "Fuera de alcance" la validación
en `/login`, porque este pedido dispara su gatillo.

## Cambios v1 → v2 (lo que tumbó el gate del 2026-09-29)

- Token vencido devuelve `accessexception`, no `invalidtoken` (webservice/lib.php:1176 de
  Moodle 4.1): ahora los dos son `ErrSessionExpired`.
- El escape de Markdown legacy no vale dentro de `*…*`: el título sale del negrito.
- Sin colly se perdía el timeout de 15 s y el oneshot corre con `TimeoutStartUSec=infinity`.
- La ventana se corta a fin de día y su justificación pasa a ser la medida.
- Estados de quiz `overdue` y `abandoned`, `teamsubmission`, prefijo `2026-` sin espacios,
  `GetUser` con token ilegible, y se saca `time/tzdata` (el Go de nixpkgs ya trae zoneinfo).

## Problema (observado)

1. La alerta no dice si algo ya está hecho. Captura del 29/09 20:01: "Trabajo Práctico Nº 2
   · Administración de Servicios · Vence hoy 23:55" aparece igual antes y después de
   entregarlo.
2. El bot lee el HTML de `/calendar/view.php?view=upcoming` con colly (`internal/adapters/pedco/scraper.go`).
   El HTML no trae el estado de la entrega; la API sí. Además esa vista corta en **10 eventos**
   (`CALENDAR_DEFAULT_UPCOMING_MAXEVENTS`, el usuario no tiene preferencia propia) y 4 de los 10
   son "Clase Sincrónica", que el parser descarta: hoy se pierden BD TP4 (13/10), AyS TP3 (15/10)
   y SI P2 (18/10) por conteo, no por fecha.
3. Un cuestionario aparece dos veces ("Se abre Primer parcial" y "Se cierra Primer
   parcial"), porque el calendario tiene un evento por borde.
4. Una tarea que todavía no acepta entregas se ve como cualquier otra. Caso real: SI
   "Entrega Practico 3" abre el 26/10 21:35; PEDCO rechaza la entrega hoy con
   "La fecha de vencimiento de esta tarea ya ha pasado" (mensaje engañoso).

## Decisiones del PO (2026-09-29)

- **D1. Todo a la API.** Se elimina colly y el scraping HTML. Un token de Web Services por
  usuario.
- **D2. Lo hecho se muestra con ✅, al final** del mensaje, en una línea corta.
- **D3. Mejora extra: 🔒 "Abre <fecha>"** para lo que aún no abrió.

## Medido (spikes del 2026-09-29 con el token de Rolando)

| claim | evidencia |
|---|---|
| `mod_assign_get_submission_status` → `lastattempt.submission.status` = `submitted` / `new` / `draft` | 7 entregas hechas hoy leídas como `submitted`; SI P3 (`sendforgrading=1`) antes de enviar sería `draft` (no medido: no se pudo guardar, la tarea no abrió) |
| `mod_assign_get_assignments` trae `duedate`, `allowsubmissionsfromdate`, `cmid` | SI P3: desde 26/10 21:35, vence 15/11 23:55 |
| `mod_quiz_get_quizzes_by_courses` trae `timeopen`, `timeclose`, `coursemodule` | 5 quizzes de IPOO/BD/Web |
| `mod_quiz_get_user_attempts(status=all)` → `attempts[].state` | BD "Parcial 1 - 29/09" = `['finished']` (control positivo); "Herencia…" = `[]` |
| Quiz sin cierre existe | BD "Coloquio de cierre": `timeclose = 0`, `timeopen = 17/11 18:38` |
| `login/token.php?service=moodle_mobile_app` da token con usuario y contraseña | medido en curza-sync (sep-2026), no re-medido acá |
| Parámetros por POST (`wstoken` en el body) funcionan | `tablero.py` lo hace así en cada llamada |
| Token inválido (basura, cookie vieja, vacío) → `errorcode: invalidtoken`, HTTP 200 | gate, 3 casos |
| Token vencido → `accessexception` | código de Moodle 4.1 (`webservice/lib.php:1176-1178`, `:895`); no medido en vivo |
| `mod_assign_get_submission_status` sin `userid` da la misma salida que con él | gate, byte a byte |
| 0 de 41 tareas con `duedate = 0`; 0 de 41 con `teamsubmission` | gate |
| 2 de 9 cursos usan `2026-` sin espacios | gate |
| El Go de nixpkgs resuelve zoneinfo desde el store; el closure del unit trae tzdata | gate |
| El bot tiene 2 usuarios | `select count(*) from users` sobre `pedcobot.db` (solo lectura) = 2 |

## Diseño

### Puerto (reemplaza `ports.Scraper`)

```go
type Source interface {
    Login(username, password string) (token string, err error) // login/token.php
    FetchItems(token string, now time.Time) ([]domain.Item, error)
}
```

`ErrSessionExpired` se conserva con el nuevo significado: el token dejó de valer
(respuesta con `errorcode` `invalidtoken` o `accessexception`). `ScraperFactory` desaparece: el adaptador
de la API no guarda estado entre llamadas, así que el `Notifier` recibe un `Source` en vez
de una fábrica.

### Dominio

```go
type Kind int   // Assignment | Quiz
type Status int // Pending | Draft | NotOpen | InProgress | Done | Unknown

type Item struct {
    Kind    Kind
    Title   string    // nombre de la actividad, SIN "Vencimiento de" / "Se cierra"
    Course  string    // fullname sin el prefijo "2026 - "
    Due     time.Time // tarea: duedate; quiz: timeclose, o timeopen si timeclose == 0
    OpensAt time.Time // tarea: allowsubmissionsfromdate; quiz: timeopen (cero si no hay)
    Status  Status
    Link    string    // /mod/assign/view.php?id=<cmid> o /mod/quiz/view.php?id=<cmid>
}
```

### Adaptador `internal/adapters/moodle`

Endpoint `https://pedco.uncoma.edu.ar/webservice/rest/server.php`, `moodlewsrestformat=json`,
todo por POST. La URL base es configurable en el constructor (los tests apuntan a un
`httptest.Server`). El constructor arma su propio `http.Client{Timeout: 15 * time.Second}`
(el mismo valor que `requestTimeout` del scraper): el oneshot no tiene timeout de systemd y
un PEDCO colgado lo dejaría colgado para siempre.

`FetchItems(token, now)`:

1. `core_webservice_get_site_info` → `userid`.
2. `core_enrol_get_users_courses(userid)`.
3. `mod_assign_get_assignments(courseids[])` y `mod_quiz_get_quizzes_by_courses(courseids[])`:
   una llamada cada una, con todos los cursos.
4. **Ventana:** entra el ítem con `now <= Due <= fin del día (hora AR) de now + 14 días`.
   Los 14 días son decisión (el calendario usa 21 con tope de 10 eventos, ver Problema 2).
   El corte a fin de día evita que un mismo ítem entre a las 8 y salga a las 20. Lo que ya
   venció no entra (así se comporta hoy). Un quiz sin cierre usa `Due = timeopen`, así que
   sale de la ventana en el momento en que abre (igual que el evento "Se abre" de hoy).
5. Solo para los ítems de la ventana: `mod_assign_get_submission_status(assignid)` (sin
   `userid`: toma el del token) o `mod_quiz_get_user_attempts(quizid, status=all)`.

Materia: `fullname` sin el prefijo de año, con `^\d{4}\s*-\s*` (hay `2026 - ` y `2026-`).

**Estado** (se evalúa en este orden; gana el primero que aplica):

| tipo | condición | Status |
|---|---|---|
| tarea | el estado se lee de `lastattempt.teamsubmission` si existe, si no de `lastattempt.submission` | — |
| tarea | `status == "submitted"` | Done |
| tarea | `OpensAt > now` | NotOpen |
| tarea | `status == "draft"` | Draft |
| tarea | cualquier otro (`new`, `reopened`, sin submission) | Pending |
| quiz | algún intento con `state == "finished"` | Done |
| quiz | `OpensAt > now` | NotOpen |
| quiz | algún intento `inprogress` u `overdue` | InProgress |
| quiz | algún intento `abandoned` y ninguno de los anteriores | Unknown |
| quiz | ninguno | Pending |

`abandoned` va a Unknown y no a Pending: con los intentos agotados sería un pendiente falso
para siempre. Ni `overdue` ni `abandoned` aparecieron en los datos (13 quizzes).

**Errores:** si la respuesta es un objeto con `exception`: `errorcode` `invalidtoken` o
`accessexception` → `ErrSessionExpired` (el re-login se intenta una sola vez por ronda, como
hoy en `fetchEventsFor`, así que no hay bucle); cualquier otro → error con el `message` de Moodle. Si una llamada
de estado de UN ítem falla, el ítem queda `Unknown` y se registra el error en el log (sin el
token); la ronda sigue. No `Pending`: un "pendiente" falso sobre algo ya entregado invita a
re-entregar, y un "hecho" falso es peor (fail-closed hacia la duda, no hacia una afirmación).

**`Login`:** POST a `/login/token.php` con `username`, `password`, `service=moodle_mobile_app`.
La respuesta trae `token` o `error`; `error` → error de login.

### Credenciales rechazadas (v3)

Un error nuevo en `ports`: `ErrBadCredentials`. `Login` lo devuelve SOLO cuando
`token.php` responde con `errorcode` `invalidlogin`. Cualquier otro error (timeout, HTTP
distinto de 200, cuerpo no JSON, otro `errorcode`) sigue siendo un error común.

La separación es lo que decide el diseño. El 2026-09-29 PEDCO estuvo caído durante toda
la sesión (timeout en `token.php` y en `server.php`, con Google respondiendo). Si "no pude
entrar" disparara el aviso, esa caída les habría dicho a todos los usuarios que su
contraseña no funciona.

- **Ronda automática:** si el login da `ErrBadCredentials`, se le manda al usuario:
  `🔑 PEDCO rechazó tu usuario o contraseña guardados (¿la cambiaste?). Mandá /login para
  actualizarlos. Hasta entonces no te llegan avisos.` El mensaje se repite en cada ronda
  (8 y 20 h) mientras siga fallando. No se guarda estado nuevo para deduplicar: dos
  recordatorios por día hasta que lo arregle es lo que se busca. Los demás errores siguen
  yendo solo al log.
- **`/tps`:** `ErrBadCredentials` o usuario sin credenciales → `❌ No tienes credenciales
  válidas. Usa /login.` Cualquier otro error → `⚠️ No pude consultar PEDCO ahora (puede
  estar caído). Probá en un rato.`
- **`/login`, después de recibir la contraseña:** primero se guarda, como hoy, y después
  se llama a `Login`. Con token → se guarda con `SaveSession` → `🔐 Listo, entré a PEDCO
  con tu cuenta. Usá /tps para ver tus entregas.` Con `ErrBadCredentials` → `❌ PEDCO
  rechazó ese usuario o contraseña. Probá /login de nuevo.` Con otro error →
  `💾 Guardé tus datos, pero PEDCO no responde ahora; los pruebo en la próxima ronda.`
  Guardar antes de probar es a propósito: si PEDCO está caído, igual quedan los datos.

**No medido:** que PEDCO responda `invalidlogin` ante credenciales malas. Está en el
código de Moodle 4.1 (`login/token.php`), pero no pude medirlo el 2026-09-29 porque PEDCO
no respondía. Se mide en el E2E con un usuario inexistente, así no suma un intento fallido
en la cuenta de nadie. Si el código es otro, se ajusta la constante y se anota acá.

### Persistencia

La columna `session_blob` pasa a guardar el **token**, cifrado como hoy. Sin migración: la
cookie vieja que quedó guardada llega como token inválido, la API responde `invalidtoken` y
entra el camino de re-login (criterio C6). Los métodos `SaveSession`/`ClearSession` se
quedan como están.

`NotifyOne` (/tps) pasa a usar el token guardado igual que `NotifyAll`: hoy fuerza un
login completo solo porque `GetUser` no devolvía la sesión. `GetUser` pasa a devolver
también el token guardado; si el token no se puede descifrar o es NULL, devuelve token vacío
(no error) y el flujo cae a login, igual que ya hace `GetAllUsers`.

### Mensaje

Hora de Argentina: `time.LoadLocation("America/Argentina/Buenos_Aires")`. Sin
`time/tzdata`: el Go de nixpkgs ya resuelve zoneinfo desde el store (medido por el gate).
Los timestamps de Moodle son epoch UTC. Fechas: `lun 05/10 22:00`; "Hoy 23:55" y "Mañana 23:59" cuando corresponde.

Orden: primero lo que no está hecho (Pending, Draft, InProgress, NotOpen, Unknown), ordenado por
`Due`; después lo hecho, ordenado por `Due`.

```
⏰ *Alerta Automática de Entregas:*

🔥 Examen: Primer parcial
📘 Programación Estática y Laboratorio Web
⏰ Abre lun 05/10 10:00 · cierra lun 05/10 22:00
🔒 Todavía no abrió
🔗 [Ir a Pedco](https://pedco.uncoma.edu.ar/mod/quiz/view.php?id=911187)

📝 Tarea: Entrega Trabajo Práctico Nº 3
📘 Administración de Servicios
⏰ Vence mar 13/10 23:55
⏳ Pendiente
🔗 [Ir a Pedco](…)

✅ *Hecho*
✅ TRABAJO PRACTICO N° 2 · Introduc. a la Programación Orientada a Objetos
✅ Parcial 1 - 29/09 18:00 hs · Conceptos de Bases de Datos

---
👨‍💻 *PedcoBot* | [Rolando Cobis](…)
```

Línea de estado por Status: Pending `⏳ Pendiente` · Draft `⏳ Borrador sin enviar` ·
InProgress `⏳ Intento sin terminar` · Unknown `❔ No pude leer el estado` · NotOpen `🔒 Abre <fecha>` (para el quiz, la línea
⏰ ya muestra la apertura, así que la de estado dice `🔒 Todavía no abrió`).
La línea ⏰ de un quiz con apertura y cierre muestra los dos: una sola entrada por
cuestionario (problema 3).

**Envío:** la ronda automática manda mensaje solo si hay al menos un ítem no hecho; si todo
está hecho, no manda nada (dos mensajes por día de ✅ son ruido). `/tps` responde siempre:
con la lista completa, o con "✅ ¡No tienes entregas pendientes!" si la ventana está vacía.

**Escape (Markdown legacy, `tele.ModeMarkdown`, `main.go:80`).** Telegram no admite escape
dentro de una entidad. Por eso el título NO va en negrito: `📝 Tarea: <título>`. Todo texto
que viene de la cátedra (título, materia) se escapa con `\` delante de `_`, `*`, `` ` `` y `[`,
y siempre queda fuera de entidades. Solo el texto fijo del bot usa `*…*`. Hoy ningún título
tiene esos caracteres (gate, con control positivo); el escape no está medido contra Telegram.

## Criterios de aceptación

- **C1** Tarea `submitted` en la ventana → aparece en "Hecho" con ✅, no en la lista pendiente.
- **C2** Tarea `new` con `allowsubmissionsfromdate` en el pasado → `⏳ Pendiente`.
- **C3** Tarea con `allowsubmissionsfromdate` futuro → `🔒 Abre <fecha en hora AR>`.
- **C4** Quiz con intento `finished` → ✅; quiz sin intentos y abierto → `⏳ Pendiente`;
  quiz con `timeopen` futuro → `🔒`; quiz con `timeclose == 0` usa `timeopen` como fecha.
- **C5** Un quiz produce UNA entrada, no dos.
- **C6** Token guardado inválido (incluye la cookie vieja) o vencido (`accessexception`) →
  `ErrSessionExpired` → re-login → token nuevo guardado → la ronda sigue con ese usuario.
  Si el re-login también falla, se pasa al usuario siguiente (sin reintento).
- **C7** Ronda automática con todo hecho → no envía. `/tps` con todo hecho → envía la lista.
- **C8** Fuera de la ventana (vencido, o más de 14 días) → no aparece.
- **C9** Ningún log ni mensaje de error contiene el token ni la contraseña.
- **C10** `go.mod` ya no requiere `colly`; `nix build .#pedco-bot` pasa con el
  `vendorHash` actualizado. El build que cuenta es el de nix-config (otro nixpkgs, por
  `follows`): lo corre el PO en el rebuild.
- **C11** Formato del mensaje fijado por un test golden sobre un `[]Item` con los seis
  Status.
- **C12** Falla la llamada de estado de un ítem → ese ítem sale `❔`, los demás salen bien.
- **C13** Un servidor que no responde corta en ≤ 15 s con error (test con `httptest` colgado).
- **C14** Título con `_` y `*` y materia con `[` → el texto sale escapado y fuera de negrito
  (fixture en el golden de C11).
- **C15** Quiz `overdue` → `⏳ Intento sin terminar`; quiz solo `abandoned` → `❔`.
- **C16** Materia `2026-Administracion de Sistemas` → `Administracion de Sistemas`.
- **C17** `token.php` responde `{"error":…,"errorcode":"invalidlogin"}` → `Login` devuelve
  `ErrBadCredentials` (`errors.Is`). Timeout, HTTP 500 u otro `errorcode` → NO es
  `ErrBadCredentials`.
- **C18** Ronda automática: un usuario con `ErrBadCredentials` recibe el mensaje 🔑 y la
  ronda sigue con el siguiente. Un usuario con timeout NO recibe mensaje.
- **C19** `/tps`: `ErrBadCredentials` → mensaje de `/login`; otro error → mensaje de PEDCO
  caído.
- **C20** `/login` con credenciales buenas → token guardado + 🔐; con malas → ❌ y los datos
  quedan guardados; con PEDCO caído → 💾.

## Fuera de alcance (con gatillo)

- Avisar notas nuevas. *Gatillo:* que el PO lo pida.
- Eventos de curso con "parcial"/"examen"/"recuperatorio" en el título: el parser actual los
  toma (`scraper.go:148-152`) y la API de tareas y quizzes no. Hoy no hay ninguno en los
  próximos 90 días (gate). *Gatillo:* que una cátedra anuncie un parcial solo como evento.
- Mostrar actividades que la cátedra oculta al alumno (el recuperatorio de BD no aparece en
  `mod_quiz_get_quizzes_by_courses`; tampoco aparecía en el calendario de hoy).
- Deploy: push, `nix flake update pedco-bot` en nix-config y `rebuild` los hace el PO.
