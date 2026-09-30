package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"scraper-pedco/internal/adapters/storage"
	"scraper-pedco/internal/core/ports"
)

// openTempStorage abre la base del bot en un directorio temporal (T12): InitDB usa ./pedcobot.db del cwd.
func openTempStorage(t *testing.T) *storage.Repository {
	t.Helper()
	t.Setenv("SECRET_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Chdir(t.TempDir())
	storage.InitDB()
	return storage.NewRepository()
}

func newTopicSender(t *testing.T, fake *fakeTelegram, topics TopicRepository) *telegramSender {
	t.Helper()
	sender, _ := newTestSender(t, fake)
	sender.topics = topics
	return sender
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buffer
}

func (fake *fakeTelegram) calls(method string) []recorded {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	var matching []recorded
	for _, request := range fake.requests {
		if request.method == method {
			matching = append(matching, request)
		}
	}
	return matching
}

func threadsOf(requests []recorded) []string {
	threads := []string{}
	for _, request := range requests {
		threads = append(threads, request.threadID)
	}
	return threads
}

func topicCreated(threadID int) string {
	return fmt.Sprintf(`{"ok":true,"result":{"message_thread_id":%d,"name":"x","icon_color":7322096}}`, threadID)
}

const threadNotFound = `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`

// countingTopics cuenta cada consulta a la tabla topics.
type countingTopics struct {
	TopicRepository
	calls int
}

func (counting *countingTopics) TopicID(chatID int64, channel ports.Channel) (int, bool, error) {
	counting.calls++
	return counting.TopicRepository.TopicID(chatID, channel)
}

func (counting *countingTopics) SaveTopic(chatID int64, channel ports.Channel, threadID int) error {
	counting.calls++
	return counting.TopicRepository.SaveTopic(chatID, channel, threadID)
}

func (counting *countingTopics) ForgetTopic(chatID int64, channel ports.Channel) error {
	counting.calls++
	return counting.TopicRepository.ForgetTopic(chatID, channel)
}

// T2: el primer envío a un canal crea UN tema con su nombre, lo guarda y lo usa; el segundo no crea otro.
func TestTopics_FirstSendCreatesOneTopicAndReusesIt(t *testing.T) {
	topics := openTempStorage(t)
	nextThread := 500
	fake := &fakeTelegram{replyTo: func(entry recorded) (int, string) {
		if entry.method == "createForumTopic" {
			nextThread++
			return 200, topicCreated(nextThread)
		}
		return 200, okMessage
	}}
	sender := newTopicSender(t, fake, topics)

	for _, err := range []error{
		sender.Send(1, ports.ChannelDeliveries, "a"),
		sender.Send(1, ports.ChannelDeliveries, "b"),
		sender.SendPlain(1, ports.ChannelNews, "c"),
		sender.Send(2, ports.ChannelDeliveries, "d"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	creates := fake.calls("createForumTopic")
	if len(creates) != 3 {
		t.Fatalf("un tema por (chat, canal): %+v", creates)
	}
	for index, want := range []struct {
		name string
		chat string
	}{{"📚 Entregas", `"chat_id":"1"`}, {"📣 Novedades", `"chat_id":"1"`}, {"📚 Entregas", `"chat_id":"2"`}} {
		if creates[index].name != want.name || !strings.Contains(creates[index].text, want.chat) {
			t.Errorf("tema %d: %q %s, quiero %q %s", index, creates[index].name, creates[index].text, want.name, want.chat)
		}
	}
	if got := threadsOf(fake.calls("sendMessage")); strings.Join(got, ",") != "501,501,502,503" {
		t.Errorf("hilos de los envíos: %v", got)
	}
	if threadID, found, err := topics.TopicID(1, ports.ChannelDeliveries); err != nil || !found || threadID != 501 {
		t.Errorf("Entregas guardado: %d %v %v", threadID, found, err)
	}
	if threadID, found, err := topics.TopicID(1, ports.ChannelNews); err != nil || !found || threadID != 502 {
		t.Errorf("Novedades guardado: %d %v %v", threadID, found, err)
	}
}

// T3 + T11: falla crear el tema → fuera de tema, sin reintentar en el proceso; un proceso nuevo sí.
// nil y ThreadID 0 cuentan como fallo. El log no lleva el token.
func TestTopics_FailedCreationGoesOutsideTopicAndIsNotRetried(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"error de Telegram", 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat is not a forum"}`},
		{"resultado nulo", 200, `{"ok":true,"result":null}`},
		{"ThreadID 0", 200, `{"ok":true,"result":{"message_thread_id":0,"name":"x"}}`},
		{"red caída (el error cita la URL con el token)", hangUp, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := captureLogs(t)
			topics := openTempStorage(t)
			fake := &fakeTelegram{replyTo: func(entry recorded) (int, string) {
				if entry.method == "createForumTopic" {
					return testCase.status, testCase.body
				}
				return 200, okMessage
			}}
			sender := newTopicSender(t, fake, topics)

			for _, err := range []error{
				sender.Send(1, ports.ChannelDeliveries, "a"),
				sender.SendPlain(1, ports.ChannelDeliveries, "b"),
				sender.Send(1, ports.ChannelDeliveries, "c"),
			} {
				if err != nil {
					t.Fatalf("el mensaje no se pierde: %v", err)
				}
			}
			if creates := fake.calls("createForumTopic"); len(creates) != 1 {
				t.Errorf("un solo intento de crear por proceso: %d", len(creates))
			}
			if got := threadsOf(fake.calls("sendMessage")); strings.Join(got, ",") != ",," {
				t.Errorf("los tres salen fuera de tema: %v", got)
			}
			if _, found, _ := topics.TopicID(1, ports.ChannelDeliveries); found {
				t.Errorf("no se guarda un tema que no se creó")
			}
			if !strings.Contains(logs.String(), "tema") {
				t.Errorf("control positivo: el fallo se loguea: %s", logs.String())
			}
			if strings.Contains(logs.String(), botToken) || strings.Contains(logs.String(), "SECRETO-del-bot") {
				t.Errorf("T11: el log filtra el token: %s", logs.String())
			}

			if err := sender.Send(1, ports.ChannelNews, "otro canal"); err != nil {
				t.Fatal(err)
			}
			if creates := fake.calls("createForumTopic"); len(creates) != 2 {
				t.Errorf("el fallo se recuerda por canal: %d intentos", len(creates))
			}

			nextProcess := newTopicSender(t, fake, topics)
			if err := nextProcess.Send(1, ports.ChannelDeliveries, "d"); err != nil {
				t.Fatal(err)
			}
			if creates := fake.calls("createForumTopic"); len(creates) != 3 {
				t.Errorf("un proceso nuevo lo intenta: %d intentos", len(creates))
			}
		})
	}
}

// Escenarios de tema borrado: el guardado es 700 y Telegram ya no lo tiene.
var threadGoneCases = []struct {
	name        string
	create      func() (int, string)
	deadThreads map[string]bool
	wantThreads string // hilos de los sendMessage, en orden
	wantSaved   int
}{
	{"el tema nuevo funciona", func() (int, string) { return 200, topicCreated(701) }, map[string]bool{"700": true}, "700,701", 701},
	{"el tema nuevo tampoco existe", func() (int, string) { return 200, topicCreated(701) }, map[string]bool{"700": true, "701": true}, "700,701,", 701},
	{"recrear falla", func() (int, string) {
		return 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat is not a forum"}`
	}, map[string]bool{"700": true}, "700,", 0},
}

func threadGoneFake(create func() (int, string), deadThreads map[string]bool) *fakeTelegram {
	return &fakeTelegram{replyTo: func(entry recorded) (int, string) {
		if entry.method == "createForumTopic" {
			return create()
		}
		if deadThreads[entry.threadID] {
			return 400, threadNotFound
		}
		return 200, okMessage
	}}
}

// T4: message thread not found → se olvida, se crea UNO nuevo y se reenvía; si falla, fuera de tema.
func TestTopics_ThreadNotFoundRecreatesOnceAndNeverLoses(t *testing.T) {
	for _, testCase := range threadGoneCases {
		t.Run(testCase.name, func(t *testing.T) {
			topics := openTempStorage(t)
			if err := topics.SaveTopic(1, ports.ChannelDeliveries, 700); err != nil {
				t.Fatal(err)
			}
			fake := threadGoneFake(testCase.create, testCase.deadThreads)
			sender := newTopicSender(t, fake, topics)

			if err := sender.Send(1, ports.ChannelDeliveries, "hola"); err != nil {
				t.Fatalf("el mensaje no se pierde: %v", err)
			}
			sends := fake.calls("sendMessage")
			if got := strings.Join(threadsOf(sends), ","); got != testCase.wantThreads {
				t.Errorf("hilos: %q, quiero %q", got, testCase.wantThreads)
			}
			if last := sends[len(sends)-1]; testCase.deadThreads[last.threadID] || !strings.Contains(last.text, "hola") {
				t.Errorf("el último envío tiene que salir: %+v", last)
			}
			if creates := fake.calls("createForumTopic"); len(creates) != 1 {
				t.Errorf("se recrea UNA vez: %d", len(creates))
			}
			threadID, found, err := topics.TopicID(1, ports.ChannelDeliveries)
			if err != nil || found != (testCase.wantSaved != 0) || threadID != testCase.wantSaved {
				t.Errorf("guardado: %d %v %v, quiero %d", threadID, found, err, testCase.wantSaved)
			}
		})
	}
}

// T5: en el adaptador, un tema inexistente nunca sale como error (ni ErrSendPermanent).
// El resto de los 4xx dentro de un tema sigue siendo permanente.
func TestTopics_ThreadNotFoundNeverReachesTheService(t *testing.T) {
	for _, testCase := range threadGoneCases {
		t.Run(testCase.name, func(t *testing.T) {
			topics := openTempStorage(t)
			fake := threadGoneFake(testCase.create, testCase.deadThreads)
			sender := newTopicSender(t, fake, topics)
			for name, send := range map[string]func() error{
				"Send":      func() error { return sender.Send(1, ports.ChannelNews, "x") },
				"SendPlain": func() error { return sender.SendPlain(1, ports.ChannelNews, "x") },
			} {
				if err := topics.SaveTopic(1, ports.ChannelNews, 700); err != nil {
					t.Fatal(err)
				}
				if err := send(); err != nil || errors.Is(err, ports.ErrSendPermanent) {
					t.Errorf("%s: quiero nil, tengo %v", name, err)
				}
				if err := topics.ForgetTopic(1, ports.ChannelNews); err != nil {
					t.Fatal(err)
				}
			}
		})
	}

	t.Run("otro 400 dentro del tema sigue siendo permanente", func(t *testing.T) {
		topics := openTempStorage(t)
		if err := topics.SaveTopic(1, ports.ChannelNews, 700); err != nil {
			t.Fatal(err)
		}
		fake := &fakeTelegram{reply: func(string) (int, string) {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Can't find end of the entity"}`
		}}
		sender := newTopicSender(t, fake, topics)
		if err := sender.Send(1, ports.ChannelNews, "*x"); !errors.Is(err, ports.ErrSendPermanent) {
			t.Errorf("quiero ErrSendPermanent: %v", err)
		}
		if len(fake.requests) != 1 {
			t.Errorf("sin recrear ni reenviar: %+v", fake.requests)
		}
	})
}

// T11: red caída con canal de tema → el error sale sin el token.
func TestTopics_NetworkErrorWithTopicIsSanitized(t *testing.T) {
	logs := captureLogs(t)
	topics := openTempStorage(t)
	fake := &fakeTelegram{reply: func(string) (int, string) { return 200, okMessage }}
	sender, server := newTestSender(t, fake)
	sender.topics = topics
	server.Close()

	err := sender.Send(1, ports.ChannelDeliveries, "x")
	if err == nil || errors.Is(err, ports.ErrSendPermanent) {
		t.Fatalf("red caída es transitoria: %v", err)
	}
	for where, text := range map[string]string{"err.Error()": err.Error(), "log": logs.String()} {
		if strings.Contains(text, botToken) || strings.Contains(text, "SECRETO-del-bot") {
			t.Errorf("%s filtra el token: %s", where, text)
		}
	}
}

// T13: ChannelGeneral no consulta topics ni crea temas.
func TestTopics_GeneralNeverTouchesTopics(t *testing.T) {
	counting := &countingTopics{TopicRepository: openTempStorage(t)}
	fake := &fakeTelegram{replyTo: func(entry recorded) (int, string) {
		if entry.method == "createForumTopic" {
			return 200, topicCreated(501)
		}
		return 200, okMessage
	}}
	sender := newTopicSender(t, fake, counting)

	if err := sender.Send(1, ports.ChannelGeneral, "a"); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendPlain(1, ports.ChannelGeneral, "b"); err != nil {
		t.Fatal(err)
	}
	if counting.calls != 0 || len(fake.calls("createForumTopic")) != 0 {
		t.Errorf("General tocó topics: consultas=%d creaciones=%d", counting.calls, len(fake.calls("createForumTopic")))
	}
	if got := threadsOf(fake.calls("sendMessage")); strings.Join(got, ",") != "," {
		t.Errorf("General va sin hilo: %v", got)
	}

	if err := sender.Send(1, ports.ChannelDeliveries, "c"); err != nil {
		t.Fatal(err)
	}
	if counting.calls == 0 {
		t.Errorf("control positivo: un canal de tema sí consulta")
	}
}
