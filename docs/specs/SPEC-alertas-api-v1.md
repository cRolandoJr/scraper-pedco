# SPEC v1 — Alertas con estado, vía la API de Moodle

Rama `feat/api-moodle` desde `main` @ `bd2c07b`. Fecha 2026-09-29.

## Problema (observado)

1. La alerta no dice si algo ya está hecho. Captura del 29/09 20:01: "Trabajo Práctico Nº 2
   · Administración de Servicios · Vence hoy 23:55" aparece igual antes y después de
   entregarlo.
2. El bot lee el HTML de `/calendar/view.php?view=upcoming` con colly (`internal/adapters/pedco/scraper.go`).
   El HTML no trae el estado de la entrega; la API sí.
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
(respuesta con `errorcode` `invalidtoken`). `ScraperFactory` desaparece: el adaptador
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
`httptest.Server`).

`FetchItems(token, now)`:

1. `core_webservice_get_site_info` → `userid`.
2. `core_enrol_get_users_courses(userid)`.
3. `mod_assign_get_assignments(courseids[])` y `mod_quiz_get_quizzes_by_courses(courseids[])`:
   una llamada cada una, con todos los cursos.
4. **Ventana:** entra el ítem con `now <= Due <= now + 14 días`. Los 14 días son decisión,
   no medición: el calendario actual mostró TP3 de Adm. Servicios (+14 d) y no SI P2 (+19 d),
   pero tampoco mostró BD TP4 (+14 d), así que su regla real no está medida. Lo que ya
   venció no entra (así se comporta hoy).
5. Solo para los ítems de la ventana: `mod_assign_get_submission_status(assignid, userid)`
   o `mod_quiz_get_user_attempts(quizid, status=all)`.

**Estado** (se evalúa en este orden; gana el primero que aplica):

| tipo | condición | Status |
|---|---|---|
| tarea | `submission.status == "submitted"` | Done |
| tarea | `OpensAt > now` | NotOpen |
| tarea | `submission.status == "draft"` | Draft |
| tarea | cualquier otro (`new`, `reopened`, sin submission) | Pending |
| quiz | algún intento con `state == "finished"` | Done |
| quiz | `OpensAt > now` | NotOpen |
| quiz | algún intento `inprogress` | InProgress |
| quiz | ninguno | Pending |

**Errores:** si la respuesta es un objeto con `exception`: `errorcode == "invalidtoken"` →
`ErrSessionExpired`; cualquier otro → error con el `message` de Moodle. Si una llamada
de estado de UN ítem falla, el ítem queda `Unknown` y se registra el error en el log (sin el
token); la ronda sigue. No `Pending`: un "pendiente" falso sobre algo ya entregado invita a
re-entregar, y un "hecho" falso es peor (fail-closed hacia la duda, no hacia una afirmación).

**`Login`:** POST a `/login/token.php` con `username`, `password`, `service=moodle_mobile_app`.
La respuesta trae `token` o `error`; `error` → error de login.

### Persistencia

La columna `session_blob` pasa a guardar el **token**, cifrado como hoy. Sin migración: la
cookie vieja que quedó guardada llega como token inválido, la API responde `invalidtoken` y
entra el camino de re-login (criterio C6). Los métodos `SaveSession`/`ClearSession` se
quedan como están.

`NotifyOne` (/tps) pasa a usar el token guardado igual que `NotifyAll`: hoy fuerza un
login completo solo porque `GetUser` no devolvía la sesión. `GetUser` pasa a devolver
también el token guardado.

### Mensaje

Hora de Argentina: `time.LoadLocation("America/Argentina/Buenos_Aires")` con
`import _ "time/tzdata"` (el binario corre desde `/nix/store`; no depender del zoneinfo del
sistema). Fechas: `lun 05/10 22:00`; "Hoy 23:55" y "Mañana 23:59" cuando corresponde.

Orden: primero lo que no está hecho (Pending, Draft, InProgress, NotOpen, Unknown), ordenado por
`Due`; después lo hecho, ordenado por `Due`.

```
⏰ *Alerta Automática de Entregas:*

🔥 EXAMEN *Primer parcial*
📘 Programación Estática y Laboratorio Web
⏰ Abre lun 05/10 10:00 · cierra lun 05/10 22:00
🔒 Todavía no abrió
🔗 [Ir a Pedco](https://pedco.uncoma.edu.ar/mod/quiz/view.php?id=911187)

📝 Tarea *Entrega Trabajo Práctico Nº 3*
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

Los títulos y materias se escapan para Markdown (`*`, `_`, `` ` ``, `[`), porque vienen de la
cátedra y hoy un `_` en un nombre rompe el formato.

## Criterios de aceptación

- **C1** Tarea `submitted` en la ventana → aparece en "Hecho" con ✅, no en la lista pendiente.
- **C2** Tarea `new` con `allowsubmissionsfromdate` en el pasado → `⏳ Pendiente`.
- **C3** Tarea con `allowsubmissionsfromdate` futuro → `🔒 Abre <fecha en hora AR>`.
- **C4** Quiz con intento `finished` → ✅; quiz sin intentos y abierto → `⏳ Pendiente`;
  quiz con `timeopen` futuro → `🔒`; quiz con `timeclose == 0` usa `timeopen` como fecha.
- **C5** Un quiz produce UNA entrada, no dos.
- **C6** Token guardado inválido (incluye la cookie vieja) → `ErrSessionExpired` →
  re-login → token nuevo guardado → la ronda sigue con ese usuario.
- **C7** Ronda automática con todo hecho → no envía. `/tps` con todo hecho → envía la lista.
- **C8** Fuera de la ventana (vencido, o más de 14 días) → no aparece.
- **C9** Ningún log ni mensaje de error contiene el token ni la contraseña.
- **C10** `go.mod` ya no requiere `colly`; `nix build .#pedco-bot` pasa con el
  `vendorHash` actualizado.
- **C11** Formato del mensaje fijado por un test golden sobre un `[]Item` con los seis
  Status.
- **C12** Falla la llamada de estado de un ítem → ese ítem sale `❔`, los demás salen bien.

## Fuera de alcance (con gatillo)

- Validar la contraseña en `/login` pidiendo el token en ese momento. *Gatillo:* un usuario
  que guarda credenciales malas y no se entera.
- Avisar notas nuevas. *Gatillo:* que el PO lo pida.
- Mostrar actividades que la cátedra oculta al alumno (el recuperatorio de BD no aparece en
  `mod_quiz_get_quizzes_by_courses`; tampoco aparecía en el calendario de hoy).
- Deploy: push, `nix flake update pedco-bot` en nix-config y `rebuild` los hace el PO.
