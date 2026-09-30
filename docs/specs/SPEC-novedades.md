# SPEC v3 — Novedades: avisos del foro, notas nuevas y material nuevo

Rama `feat/novedades` desde `feat/api-moodle`, una vez que esa rama esté cerrada (spec
alertas v4). Usa su adaptador `internal/adapters/moodle`. Fecha 2026-09-29.

## Cambios v2 → v3 (decisión del PO, 2026-09-30)

El material nuevo avisa con el nombre y el link, y NO descarga ni reenvía el archivo. El PO:
"dejaría de ser liviano; con solo lectura sirve". Salen `Download`, `SendDocument`, el tope
de 50 MB y la descarga por POST. Las mediciones de la descarga quedan en la tabla como
historia. Se queda el saneo del token de TELEGRAM en los errores de `telegramSender`: es un
defecto demostrado (un test en rojo) del bot ya desplegado, y no depende de la descarga.

## Cambios v1 → v2 (gate del 2026-09-30: FAIL, 6 P1)

- La línea de base pasa a ser POR CURSO. Una materia que falla en la primera pasada (IPOO
  da `notingroup` en notas, medido) o una materia nueva traía hasta 55 mensajes de golpe.
- Material filtra `uservisible` ANTES de marcar visto. Un módulo con restricción aparece en
  la API sin archivo (3 parciales de BD, medido): se avisaba sin poder abrirlo y nunca más
  cuando se habilitaba.
- El token va por POST en la descarga (medido: anda). `net/url` imprime la query en los
  errores, y el token se filtraba al log (el gate lo ejecutó).
- El fallo permanente de Telegram deja de reintentarse para siempre: cae a texto plano o al
  link, y se marca visto.
- Se completa el puerto (usuario, cursos, `SendDocument`, `SeenRepository`) y se fija el
  orden alertas → novedades.
- Foro sin `perpage`: con `perpage=N` se perdían avisos (paginación medida en el foro 61009).
- La clave de notas pasa a ser `id` + valor. `gradedategraded` cambia con cualquier
  escritura de la fila (fuente de Moodle, `grade_grade.php:424-433`), no solo con una
  corrección.
- La marca de visto es por ítem, y se acepta explícitamente que un aviso pueda llegar dos
  veces.

## Problema

Hoy el bot avisa solo de entregas y parciales. Hay tres cosas que llegan tarde o no llegan:

- Los avisos de las cátedras. Ejemplo real: "Acceso al 1er Parcial" (BD, Corujo), publicado
  el mismo día del parcial en el foro Avisos.
- Las notas.
- El material que suben.

El PO pidió A (foro), B (notas) y C (material), las tres de solo lectura. Entregar desde
Telegram quedó descartado.

## Medido (2026-09-29 23:40, token de Rolando, curso BD 10325)

| hecho | evidencia |
|---|---|
| Las 6 funciones están en `core_webservice_get_site_info.functions` | `mod_forum_get_forums_by_courses`, `mod_forum_get_forum_discussions`, `gradereport_user_get_grade_items`, `gradereport_overview_get_course_grades`, `core_course_get_updates_since`, `core_course_check_updates` |
| `mod_forum_get_forums_by_courses(courseids[])` trae `id`, `type`, `name` | BD: `60925 news "Avisos"` (4 discusiones), `60926 general "Foro de Consultas"` |
| `mod_forum_get_forum_discussions(forumid, sortorder=1, page, perpage)` trae `discussion`, `subject`, `message` (HTML), `created`, `userfullname`, `attachment` | 4 avisos, el más nuevo "Acceso al 1er Parcial", 1075 caracteres |
| `gradereport_user_get_grade_items(courseid, userid)` trae `id`, `itemname`, `itemtype`, `graderaw`, `gradeformatted`, `gradedategraded`, `cmid` | "Entrega TP1 - 25/08", `graderaw 2`, `gradedategraded` 1788381797 |
| `gradeformatted` viene con HTML | `<i class="icon fa fa-check …" title="Aprobado" …></i>Satisfactorio` |
| `core_course_get_contents` trae módulos con `id` (cmid), `modname`, `name`, `contents[]` | BD: resource=27, url=14, quiz=5, assign=5, forum=2, label=1 |
| Las fechas de los archivos no alcanzan para detectar lo nuevo | `timecreated` de los 3 primeros PDF de BD = 1771467735 (feb-2026, la copia del aula); `timemodified` sí varía (17 posteriores al 1-sep) |
| Tamaño del material | 377 módulos resource/url/folder en las 9 materias; el archivo más grande mide 14 952 375 bytes (gate) |
| La descarga acepta el token por POST | `curl -d token=… <fileurl>` → HTTP 200, `application/pdf`, 114 495 bytes = `filesize` (Presentacion.pdf) |
| Sin token, la descarga responde HTTP **200** con JSON de error | `{"error":"…(token) faltaba","errorcode":"missingparam"}`, 141 bytes |
| Un módulo con restricción aparece, con `uservisible:false` y sin `contents` | 3 quizzes de BD; fuente `course/externallib.php:231,308-320` (gate) |
| `mod_forum_get_forum_discussions` pagina, con los fijados primero | foro 61009, 9 avisos: `perpage=4` → 4/4/1; sin `perpage` → 9 (gate) |
| Las notas de IPOO fallan siempre para este usuario | `gradereport_user_get_grade_items(10327)` → `notingroup` (gate) |
| `core_course_get_updates_since(since = hace 30 días)` NO sirve para material | 9 módulos; solo 1 `resource` ("Soluciones TP2", `contentfiles`); el resto son notas, entregas y config |

**No medido:** qué códigos de error devuelve Telegram ante un documento o un Markdown
rechazado (se toman de la documentación de la Bot API: 400 y 413).

## Decisiones (medias, tomadas por mérito)

- **Un solo spec para las tres:** comparten la pieza que recuerda qué ya se avisó.
- **Detectar comparando contra lo ya visto, no por fechas.** La ronda puede saltearse con
  la laptop apagada, y el `timecreated` del material es el de la copia del aula. Un
  conjunto de "ya visto" por usuario no depende del reloj.
- **Primera pasada = línea de base, POR CURSO.** La primera vez que se trae con éxito un
  tipo de un curso para un usuario, se marca todo como visto, se registra la base de ese
  curso y NO se avisa. Si no, llegarían los 377 materiales de golpe. La base se registra
  aunque no haya ítems; si no, el primer ítem real se tragaría como base. Si la traída del
  curso FALLA, no se registra nada: ese curso hace su base el día que responda.
- **A:** solo foros `type == "news"` (Avisos). El foro de consultas es charla entre
  alumnos. La clave es el `discussion`. Las respuestas y ediciones no avisan. Se piden
  TODAS las discusiones (sin `perpage`); el máximo medido fue 9 por foro.
- **B:** solo ítems con `itemtype` distinto de `course` (el total cambia con cada nota y
  sería ruido) y con `graderaw` no nulo. `graderaw` es un puntero, así que un 0 es una
  nota. La clave es `id` + `graderaw`: una nota que cambia de valor vuelve a avisar, y una
  escritura que no cambia el valor no avisa. Una nota oculta llega con `graderaw` nulo, y
  cuando el docente la muestra, avisa: para el alumno es nueva.
- **C:** solo módulos `resource`, `url` y `folder` con `uservisible == true`. Lo restringido
  no entra ni a la base ni a lo visto: cuando se habilita, avisa. La clave es el cmid.
  - Los tres tipos avisan con el nombre y el link al módulo (v3). No se descarga nada.
- **Mismas rondas que las alertas (8 y 20 h), y mensajes APARTE del de alertas.** Así, si
  la novedad falla, la alerta de entregas no se pierde. Si hay novedades de varios tipos,
  va un mensaje por tipo.
- **Mismos cursos que la ronda de alertas.**
- **Un usuario pausado (`creds_rejected`) no recibe novedades**, igual que las alertas.
- **Orden y token:** por usuario, primero alertas y después novedades, con el mismo token
  (el que dejó la fase de alertas, re-login incluido). Si alertas terminó en error, ese
  usuario no hace novedades en esa ronda. Si novedades recibe `ErrSessionExpired`, corta
  sus novedades de esa ronda sin re-loguear (la ronda siguiente lo resuelve).
- **Entrega "al menos una vez":** se marca visto cada ítem inmediatamente DESPUÉS de su
  envío, con `INSERT OR IGNORE`. Si el envío sale y la marca falla, el aviso se repite en la
  ronda siguiente. Se acepta: es mejor que perder un aviso.
- **Fallo de Telegram:**
  - Transitorio (red, 429, 5xx): no se marca visto y se reintenta en la ronda siguiente.
  - Permanente (400, 413, otros 4xx):
    - Si era un mensaje con Markdown, se reenvía una vez en texto plano sin escapes.
    - Si esa segunda vez también falla con un error permanente, se marca visto y se loguea.
- **Un curso que falla siempre** (IPOO en notas) se loguea una línea por ronda y por
  curso, sin avisarle al usuario.

## Diseño

### Puerto

```go
// En el Source (adaptador moodle). Si alertas ya trae userid y cursos, se reusa esa llamada.
Profile(token string) (userID int, courses []Course, err error)
Forums(token string, courses []Course) (map[int][]ForumPost, map[int]error) // por curso
Grades(token string, courseID, userID int) ([]GradeItem, error)
Materials(token string, courseID int) ([]Material, error) // ya filtrado por uservisible
// Los errores de envío se distinguen con errors.Is(err, ErrSendPermanent).

// Nuevo:
type SeenRepository interface {
    HasBaseline(chatID int64, kind string, courseID int) (bool, error)
    SetBaseline(chatID int64, kind string, courseID int) error
    IsSeen(chatID int64, kind, key string) (bool, error)
    MarkSeen(chatID int64, kind, key string) error // INSERT OR IGNORE
}
```

**Saneo del token de Telegram:** los errores de `telegramSender` no incluyen el token del
bot (telebot lo pone en la URL de la API y `*url.Error` la imprime).

### Persistencia

- Tabla nueva `seen(chat_id INTEGER, kind TEXT, key TEXT, PRIMARY KEY(chat_id, kind, key))`.
  `kind` es `forum`, `grade` o `material`.
- Tabla nueva `seen_baseline(chat_id INTEGER, kind TEXT, course_id INTEGER,
  PRIMARY KEY(chat_id, kind, course_id))`.
- Las dos se crean con `CREATE TABLE IF NOT EXISTS`.
- `DeleteUser` (`/borrar`) borra también las filas de ese chat.
- `SaveUser` (`/login`) NO borra lo visto. Cambiar la contraseña no hace nuevo lo ya
  avisado.

### Orden por tipo, en la ronda

Por cada curso:

1. Traer los ítems. Si falla, loguear y saltear el curso sin marcar nada.
2. Si no hay línea de base para (usuario, tipo, curso), marcar todos los ítems vistos y
   registrar la base. No se avisa.
3. Si hay base, tomar los no vistos, enviarlos y marcar cada uno después de su envío,
   según las reglas de "Fallo de Telegram".

### Mensajes

Markdown legacy con el mismo escape de alertas v4 (C14: fuera de negrito).

- **A:** `📣 Aviso en <materia>` + título + `— <autor>` + los primeros 400 caracteres del
  texto sin HTML + `🔗 Ver aviso` → `/mod/forum/discuss.php?d=<discussion>`.
  - Los 400 son runas del texto plano, y el corte se hace ANTES de escapar, con `…` si
    se cortó.
  - Un mensaje por aviso.
- **B:** `📝 Nota nueva en <materia>` + una línea por ítem: `<itemname>: <nota>`. `<nota>` es
  `gradeformatted` sin HTML (`Satisfactorio`, `8,00`). Cierra con `🔗 Ver notas` →
  `/grade/report/user/index.php?id=<courseid>`. Un mensaje por materia; si sale, se marcan
  todos sus ítems.
- **C:** `📎 Material nuevo en <materia>: <nombre>`, más `🔗 Abrir` →
  `/mod/<modname>/view.php?id=<cmid>`. Un mensaje por materia con todos sus materiales
  nuevos; si sale, se marcan todos.

## Criterios de aceptación

- **N1** Primera ronda de un usuario → cero mensajes de novedades; `seen` y `seen_baseline`
  quedan llenas para cada (tipo, curso) que se trajo bien.
- **N2** Primera pasada de un curso sin ítems → base registrada, y el primer ítem que
  aparece después SÍ avisa.
- **N3** Un curso que falla en la pasada 1 y responde en la 2 → en la 2 hace su base SIN
  avisar. Los demás cursos avisan normalmente en la 2.
- **N4** Aviso nuevo en un foro `news` → mensaje A. Aviso nuevo en un foro `general` →
  nada. Un foro con 10 avisos nuevos → llegan los 10 (el fake no pagina y el adaptador no
  manda `perpage`).
- **N5** Nota nueva → mensaje B con la nota sin HTML (fixture con el `gradeformatted` real).
  El ítem `course` y un `graderaw` nulo → nada. `graderaw = 0` → avisa.
- **N6** Mismo ítem con otro `graderaw` → avisa de nuevo. Mismo `graderaw` con otro
  `gradedategraded` → nada.
- **N7** `resource`, `url` y `folder` nuevos → mensaje C con el nombre y el link, y NINGUNA
  descarga (el fake del Source falla si se le pide un archivo). `label`, `quiz` y `assign`
  → nada.
- **N8** Recurso con `uservisible:false` → nada y NO queda visto. Cuando pasa a
  `uservisible:true` → avisa.
- **N9** Envío con error transitorio → no se marca y la ronda siguiente lo manda. Error
  permanente en Markdown → sale
  en texto plano y se marca. Los dos intentos permanentes → se marca y se loguea.
- **N10** El envío sale y `MarkSeen` falla → la ronda siguiente lo repite (se acepta el
  duplicado) y la ronda no se corta.
- **N11** Falla traer un curso → los demás siguen; nada de ese curso se marca.
- **N12** Usuario pausado, o alertas terminó en error → sin novedades. `ErrSessionExpired`
  en novedades → corta las novedades de ese usuario sin re-login. `/borrar` → sus filas de
  `seen` y `seen_baseline` desaparecen.
- **N13** El texto de foro, el `itemname` y el nombre del material con `_`, `*`, `[` y HTML
  (`<p>`, `&amp;`) salen limpios y escapados (golden). Un corte de 400 runas no deja una
  barra colgando.
- **N14** Ni el token de Moodle, ni el de Telegram, ni la contraseña aparecen en logs,
  mensajes ni en `err.Error()`. Test: `telegramSender` contra un servidor cerrado, con un
  token conocido, y `strings.Contains(err.Error(), token)` debe dar falso.
- **N15** Tests sin red (`httptest` + fakes). SQLite en `t.TempDir()`.
## Fuera de alcance (con gatillo)

- Foros de consultas y respuestas a avisos. *Gatillo:* que el PO lo pida.
- Avisos más rápidos que 8 y 20 h: cambia el timer, que vive en nix-config. *Gatillo:* un
  aviso que llegue tarde para algo del mismo día.
- Archivos dentro de `folder`. *Gatillo:* una materia que publique todo en carpetas.
- Deploy: lo hace el PO.
