# SPEC v1 — Temas de Telegram: 📚 Entregas y 📣 Novedades

Rama `feat/temas` desde `main` @ `104c56d` (alertas + novedades desplegadas). Fecha 2026-09-30.

## Problema

El PO quiere "pestañas" en el chat con el bot: separar las entregas (tareas, cuestionarios,
exámenes) de los anuncios (avisos del foro, notas, material). Hoy todo llega mezclado en el
mismo chat.

## Medido (2026-09-30, bot @scraperPedcobot, chat del PO)

| hecho | evidencia |
|---|---|
| Telegram permite temas en el chat privado con un bot | Bot API 9.3 (`message_thread_id` en privado) y 9.4 (`createForumTopic` en privado); cita: "create a topic in a forum supergroup chat or a private chat with a user" |
| El modo está prendido para este bot | `getMe` → `has_topics_enabled:true`, `allows_users_to_create_topics:false` (el PO lo prendió en la Mini App de BotFather → Bot Settings → Threads Settings) |
| `createForumTopic(chat_id, name)` funciona en privado | `ok:true`, `message_thread_id:184703`, `name:"🧪 prueba"` |
| `sendMessage` con `message_thread_id` entra al tema | `ok:true`, `is_topic_message:true` |
| `sendMessage` sin `message_thread_id` entra "fuera de tema" | `ok:true`, sin `message_thread_id`; el cliente lo muestra en "Todos" |
| `deleteForumTopic` funciona y borra sus mensajes | `ok:true` |
| Mandar a un tema borrado falla con 400 | `error_code:400`, `"Bad Request: message thread not found"` |
| Cómo lo muestra el cliente de escritorio | columna a la izquierda con "Todos" + un ícono por tema. "Todos" mezcla todo con separadores por tema; los mensajes viejos quedan ahí; la caja de texto dice "Mensaje fuera de tema" (captura del PO) |
| telebot v3.3.8 lo soporta | `Bot.CreateTopic(chat, *Topic)` llama a `createForumTopic` con `chat_id` (sirve para privado aunque el comentario diga supergrupo); `SendOptions.ThreadID` → `message_thread_id` (`options.go:200`) |
| telebot NO contesta solo en el tema del mensaje entrante | `context.go` no usa `ThreadID`; hay que pasar `Message().ThreadID` a mano |

**No medido:** cómo lo muestra el celular.

## Decisiones (medias, por mérito)

- **Dos temas por usuario:** `📚 Entregas` y `📣 Novedades`, con el emoji en el nombre (la
  prueba mostró el emoji como ícono).
- **El servicio elige el CANAL; el adaptador lo traduce a un tema.** `service` dice
  "esto va a Entregas" (un concepto del dominio). Que eso sea un `message_thread_id` de
  Telegram es un detalle del adaptador. Así el núcleo no se entera de Telegram, igual que hoy.
- **Creación perezosa:** el tema se crea la primera vez que hace falta mandarle algo a ese
  usuario por ese canal, y su id se guarda.
- **Nunca se pierde un mensaje por culpa de un tema:**
  - si crear el tema falla → el mensaje sale fuera de tema (log), y se reintenta crear en el
    próximo envío;
  - si el envío da `message thread not found` (el tema se borró) → se olvida el id, se crea
    el tema UNA vez más y se reenvía; si eso falla → fuera de tema.
  - Esto va ANTES de la clasificación de errores de novedades: hoy un 400 es "permanente" y
    la novedad se perdería.
- **Respuestas a comandos (`/tps`, `/login`, ayuda):** van al tema donde escribió el usuario
  (`Message().ThreadID`; 0 = fuera de tema). Es lo que espera alguien que escribe en un chat.
  La ronda automática es la que va a los temas.
- **Persistencia aparte de `users`:** tabla nueva `topics(chat_id INTEGER, channel TEXT,
  thread_id INTEGER, PRIMARY KEY(chat_id, channel))`. NO una columna en `users`: `SaveUser`
  hace `INSERT OR REPLACE` y la borraría en cada `/login`, lo que crearía temas duplicados.
  `DeleteUser` (`/borrar`) borra también sus filas.
- **Qué va a cada canal:** alertas de la ronda → Entregas. Mensajes A, B y C de novedades →
  Novedades. El aviso 🔑 de credenciales rechazadas → fuera de tema (es sobre la cuenta, no
  sobre una entrega, y tiene que verse sí o sí).

## Diseño

### Puerto

```go
type Channel string
const (
    ChannelDeliveries Channel = "entregas"
    ChannelNews       Channel = "novedades"
    ChannelGeneral    Channel = ""          // fuera de tema
)
// MessageSender: Send y SendPlain ganan el canal.
Send(chatID int64, channel Channel, message string) error
SendPlain(chatID int64, channel Channel, message string) error

type TopicRepository interface {
    TopicID(chatID int64, channel Channel) (threadID int, found bool, err error)
    SaveTopic(chatID int64, channel Channel, threadID int) error
    ForgetTopic(chatID int64, channel Channel) error
}
```

`TopicRepository` lo usa SOLO el adaptador de Telegram (`telegramSender` en `main.go`), no
el servicio.

### `telegramSender`

1. `ChannelGeneral` → envío sin `ThreadID`, como hoy.
2. Otro canal → `TopicID`. Si no hay → `CreateTopic`. Si eso falla, se loguea y se manda
   fuera de tema. Si funciona → `SaveTopic`.
3. Envío con `ThreadID`. Si falla con `message thread not found` → `ForgetTopic` → vuelta al
   paso 2 UNA sola vez → si vuelve a fallar, fuera de tema.
4. El resto de los errores se clasifican como hoy (401, 429, permanente, transitorio), y el
   saneo del token sigue.

Los nombres de los temas son constantes del adaptador.

## Criterios de aceptación

- **T1** Ronda: la alerta va con el `ThreadID` de Entregas; A, B y C, con el de Novedades.
- **T2** Primer envío a un canal sin tema → se crea UNO con el nombre correcto, se guarda y
  se usa. El segundo envío no crea otro.
- **T3** Falla crear el tema → el mensaje sale fuera de tema; el próximo envío vuelve a
  intentar crearlo.
- **T4** `message thread not found` → se olvida el id, se crea uno nuevo y el mensaje sale en
  el tema nuevo. Si también falla lo segundo → sale fuera de tema. En ningún caso se pierde.
- **T5** Una novedad que recibe `message thread not found` NO se marca como "permanente" ni
  se descarta (test del camino completo con novedades).
- **T6** `/tps` escrito dentro del tema Entregas → la respuesta lleva ese `ThreadID`; escrito
  fuera de tema → sin `ThreadID`. Lo mismo para `/login` y la ayuda.
- **T7** 🔑 credenciales rechazadas → fuera de tema.
- **T8** `/login` de nuevo NO borra los temas (no se duplican); `/borrar` sí.
- **T9** Una base vieja arranca y crea la tabla `topics`.
- **T10** Las suites de alertas y de novedades siguen verdes; solo cambian las costuras de
  compilación de las firmas nuevas.
- **T11** Ningún token en logs ni en `err.Error()` (el saneo sigue cubierto).
- **T12** Tests sin red: fake del bot de Telegram con `httptest` para `createForumTopic` y
  `sendMessage`; SQLite en `t.TempDir()`.

## Fuera de alcance (con gatillo)

- Mover los mensajes viejos a los temas: la API no mueve mensajes.
- Un tema por materia. *Gatillo:* que el PO lo pida.
- Deploy: lo hace el PO.
