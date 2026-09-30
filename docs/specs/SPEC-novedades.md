# SPEC v1 — Novedades: avisos del foro, notas nuevas y material nuevo

Rama `feat/novedades` desde `feat/api-moodle`, una vez que esa rama esté cerrada (spec
alertas v4). Usa su adaptador `internal/adapters/moodle`. Fecha 2026-09-29.

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
| `timecreated` de los archivos NO sirve para detectar lo nuevo | los 3 primeros PDF de BD tienen 1771467735 (feb-2026, la copia del aula) |
| `core_course_get_updates_since(since = hace 30 días)` NO sirve para material | 9 módulos; solo 1 `resource` ("Soluciones TP2", `contentfiles`); el resto son notas, entregas y config |

**No medido:**
- Si un módulo oculto le aparece al alumno en `core_course_get_contents`.
- El tamaño máximo real de los archivos de PEDCO.

## Decisiones (medias, tomadas por mérito)

- **Un solo spec para las tres:** comparten la pieza que recuerda qué ya se avisó.
- **Detectar comparando contra lo ya visto, no por fechas.** Las fechas de material no
  sirven (medido) y la ronda puede saltearse con la laptop apagada. Un conjunto de "ya
  visto" por usuario no depende del reloj.
- **Primera pasada = línea de base.** La primera vez que corre para un usuario y un tipo,
  marca todo como visto y NO avisa. Si no, llegarían 27 PDF de BD por 9 materias. La
  primera pasada queda registrada aunque no haya ningún ítem; si no se registrara, el
  primer ítem real se tragaría como base.
- **A:** solo foros `type == "news"` (Avisos). El foro de consultas es charla entre
  alumnos. La clave es el `discussion`. Las respuestas y ediciones no avisan.
- **B:** solo ítems con `itemtype` distinto de `course` (el total cambia con cada nota y
  sería ruido) y con `graderaw` no nulo. La clave es `id` + `gradedategraded`, así que una
  re-corrección vuelve a avisar.
- **C:** solo módulos `resource`, `url` y `folder`. La clave es el cmid.
  - `resource`: se manda el archivo si Telegram lo acepta (límite de 50 MB para un bot,
    documentado por Telegram, no medido). Si no, el link.
  - `url` y `folder`: solo el link.
- **Mismas rondas que las alertas (8 y 20 h), y mensajes APARTE del de alertas.** Así, si
  la novedad falla, la alerta de entregas no se pierde. Si hay novedades de varios tipos,
  va un mensaje por tipo.
- **Mismos cursos que la ronda de alertas.**
- **Un usuario pausado (`creds_rejected`) no recibe novedades**, igual que las alertas.

## Diseño

### Puerto

```go
Forums(token string, courseIDs []int) ([]ForumPost, error)  // solo news, discusiones
Grades(token string, courseID, userID int) ([]GradeItem, error)
Materials(token string, courseID int) ([]Material, error)   // resource|url|folder
DownloadFile(token, fileURL string) (io.ReadCloser, int64, error)
```

### Persistencia

- Tabla nueva `seen(chat_id INTEGER, kind TEXT, key TEXT, PRIMARY KEY(chat_id, kind, key))`.
  `kind` es `forum`, `grade` o `material`.
- Tabla nueva `seen_baseline(chat_id INTEGER, kind TEXT, PRIMARY KEY(chat_id, kind))`.
- Las dos se crean con `CREATE TABLE IF NOT EXISTS`.
- `DeleteUser` (`/borrar`) borra también las filas de ese chat.
- `SaveUser` (`/login`) NO borra lo visto. Cambiar la contraseña no hace nuevo lo ya
  avisado.

### Orden por tipo, en la ronda

1. Traer los ítems.
2. Si no hay línea de base, marcar todo visto y registrar la base.
3. Si hay, filtrar los no vistos, enviar, y marcar vistos SOLO si el envío salió bien. Si el
   envío falla, se reintenta en la ronda siguiente.

Si traer los ítems de un curso falla, se saltea ese curso (log) y no se marca nada de él.

### Mensajes

Markdown legacy con el mismo escape de alertas v4 (C14: fuera de negrito).

- **A:** `📣 Aviso en <materia>` + título + `— <autor>` + los primeros 400 caracteres del
  texto sin HTML + `🔗 Ver aviso` → `/mod/forum/discuss.php?d=<discussion>`.
- **B:** `📝 Nota nueva en <materia>` + una línea por ítem: `<itemname>: <nota>`. `<nota>` es
  `gradeformatted` sin HTML (`Satisfactorio`, `8,00`). Cierra con `🔗 Ver notas` →
  `/grade/report/user/index.php?id=<courseid>`.
- **C:** `📎 Material nuevo en <materia>: <nombre>`. Después va el archivo como documento o
  el link (`/mod/<modname>/view.php?id=<cmid>`).

## Criterios de aceptación

- **N1** Primera ronda de un usuario → cero mensajes de novedades, y `seen` y
  `seen_baseline` quedan llenas para los tres tipos.
- **N2** Primera ronda sin ningún ítem de un tipo → la base queda registrada, y el primer
  ítem que aparece después SÍ avisa.
- **N3** Aviso nuevo en un foro `news` → mensaje A. Aviso nuevo en un foro `general` → nada.
- **N4** Nota nueva → mensaje B con la nota sin HTML (fixture con el `gradeformatted` real
  medido). El ítem `course` y un `graderaw` nulo → nada.
- **N5** Re-corrección (mismo `id`, otro `gradedategraded`) → avisa de nuevo.
- **N6** Recurso nuevo → se descarga y se manda como documento. Si pasa de 50 MB, o la
  descarga falla, se manda el link. `url` y `folder` → link. `label`, `quiz` y `assign` →
  nada.
- **N7** Falla el envío → no se marca visto; la ronda siguiente lo manda.
- **N8** Falla traer los ítems de un curso → los demás cursos siguen y nada de ese curso se
  marca visto.
- **N9** Usuario pausado → sin novedades. `/borrar` → sus filas de `seen` desaparecen.
- **N10** Texto de foro con `_`, `*`, `[` y HTML (`<p>`, `&amp;`) → sale limpio y escapado
  (golden).
- **N11** Ni el token ni la contraseña aparecen en logs, en mensajes ni en el nombre del
  archivo enviado. El token viaja solo en la query de la descarga.
- **N12** Tests sin red (`httptest` + fakes); SQLite en `t.TempDir()`.

## Fuera de alcance (con gatillo)

- Foros de consultas y respuestas a avisos. *Gatillo:* que el PO lo pida.
- Avisos más rápidos que 8 y 20 h: cambia el timer, que vive en nix-config. *Gatillo:* un
  aviso que llegue tarde para algo del mismo día.
- Archivos dentro de `folder`. *Gatillo:* una materia que publique todo en carpetas.
- Deploy: lo hace el PO.
