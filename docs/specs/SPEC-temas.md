# SPEC v2 — Temas de Telegram: 📚 Entregas y 📣 Novedades

Rama `feat/temas` desde `main` @ `104c56d` (alertas + novedades desplegadas). Fecha 2026-09-30.

## Cambios v1 → v2 (gate del 2026-09-30: FAIL, 2 P1)

- **Tope al crear temas:** con "Threaded Mode" apagado, cada envío intentaba crear el tema y
  fallaba (la alerta y cada novedad son envíos distintos). El fallo se recuerda por
  (chat, canal) mientras dura el proceso: una ronda intenta crear a lo sumo una vez por canal.
- **T5 pasa al adaptador:** el reintento vive en `telegramSender`, así que el servicio nunca ve
  el `thread not found`, y un test en `service` probaría otra cosa.
- `CreateTopic` de telebot puede devolver `nil` sin error (`topic.go:40`). Un resultado nulo o
  con `ThreadID` 0 cuenta como fallo.
- El log del fallo de creación se sanea: el error de `Raw` lleva la URL con el token
  (`api.go:50,55`).
- `TopicRepository` se define del lado de quien lo consume (`cmd/scraper`), no en `ports`.
- Concurrencia, medida por el gate: el daemon no crea temas, porque sus respuestas usan
  `context.Send`. Solo dos rondas superpuestas podrían crear dos veces: la segunda fila se
  ignora (`INSERT OR IGNORE`), se relee el id guardado y queda un tema vacío huérfano. Se
  acepta, porque requiere dos oneshots a la vez (el timer no los superpone).

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
| `Message().ThreadID` llega también en `OnText` (el flujo `/login`) | gate, `ProcessUpdate` sincrónico |
| telebot no tiene un error tipado para el tema inexistente | sale como texto: `telegram: Bad Request: message thread not found (400)`, así que se reconoce por texto (gate) |
| `CreateTopic(&tele.Chat{ID: id})` funciona para un chat privado | `Recipient()` = el id; `message_thread_id` se parsea como int (gate, httptest) |

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
  - si crear el tema falla (error, resultado nulo o `ThreadID` 0) → el mensaje sale fuera de
    tema, con un log saneado. El fallo se recuerda por (chat, canal) hasta que termina el
    proceso: en esa ronda ese canal va fuera de tema sin volver a intentarlo, y la próxima
    ronda (otro proceso) lo intenta de nuevo;
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

// En cmd/scraper (del lado de quien lo consume), NO en ports:
type TopicRepository interface {
    TopicID(chatID int64, channel Channel) (threadID int, found bool, err error)
    SaveTopic(chatID int64, channel Channel, threadID int) error // INSERT OR IGNORE; después se relee
    ForgetTopic(chatID int64, channel Channel) error
}
```

`TopicRepository` lo usa SOLO el adaptador de Telegram (`telegramSender` en `main.go`), no
el servicio.

### `telegramSender`

1. `ChannelGeneral` → envío sin `ThreadID`, como hoy.
2. Otro canal → si el fallo está recordado → fuera de tema. Si no, `TopicID`. Si no hay →
   `CreateTopic`. Si falla (error, `nil` o `ThreadID` 0) → recordar el fallo, log saneado,
   fuera de tema. Si funciona → `SaveTopic` y releer el id guardado.
3. Envío con `ThreadID`. Si falla con `message thread not found` → `ForgetTopic` → vuelta al
   paso 2 UNA sola vez → si vuelve a fallar, fuera de tema.
4. El resto de los errores se clasifican como hoy (401, 429, permanente, transitorio), y el
   saneo del token sigue.

Los nombres de los temas son constantes del adaptador.

## Criterios de aceptación

- **T1** Ronda: la alerta va con el `ThreadID` de Entregas; A, B y C, con el de Novedades.
- **T2** Primer envío a un canal sin tema → se crea UNO con el nombre correcto, se guarda y
  se usa. El segundo envío no crea otro.
- **T3** Falla crear el tema → el mensaje sale fuera de tema. Más envíos al mismo canal en el
  mismo proceso NO vuelven a llamar a `createForumTopic` (se cuentan las llamadas). Un
  proceso nuevo sí lo intenta. `nil` y `ThreadID` 0 cuentan como fallo.
- **T4** `message thread not found` → se olvida el id, se crea uno nuevo y el mensaje sale en
  el tema nuevo. Si también falla lo segundo → sale fuera de tema. En ningún caso se pierde.
- **T5** A nivel adaptador (httptest): un 400 `message thread not found` → se recrea el tema,
  se reenvía, y `Send` devuelve nil. Si se agota el reintento y sale fuera de tema, también
  devuelve nil. En ningún caso el error envuelve `ErrSendPermanent` por un tema inexistente.
- **T6** `/tps` escrito dentro del tema Entregas → la respuesta lleva ese `ThreadID`; escrito
  fuera de tema → sin `ThreadID`. Lo mismo para `/login` y la ayuda.
- **T7** 🔑 credenciales rechazadas → fuera de tema.
- **T8** `/login` de nuevo NO borra los temas (no se duplican); `/borrar` sí.
- **T9** Una base vieja arranca y crea la tabla `topics`.
- **T10** Las suites de alertas y de novedades siguen verdes; solo cambian las costuras de
  compilación de las firmas nuevas.
- **T11** Ningún token en logs ni en `err.Error()`, incluido el log del fallo de
  `createForumTopic`.
- **T13** `ChannelGeneral` no consulta la tabla `topics` ni crea temas.
- **T12** Tests sin red: fake del bot de Telegram con `httptest` para `createForumTopic` y
  `sendMessage`; SQLite en `t.TempDir()`.

## Fuera de alcance (con gatillo)

- Mover los mensajes viejos a los temas: la API no mueve mensajes.
- Un tema por materia. *Gatillo:* que el PO lo pida.
- Deploy: lo hace el PO.
