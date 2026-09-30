package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tele "gopkg.in/telebot.v3"

	"scraper-pedco/internal/core/ports"
)

const botToken = "123456:SECRETO-del-bot_xyz"

// fakeTelegram responde lo que diga reply (o replyTo, si está) y guarda lo que llegó.
// Un status hangUp corta la conexión sin responder (error de red).
type fakeTelegram struct {
	mutex    sync.Mutex
	reply    func(method string) (status int, body string)
	replyTo  func(entry recorded) (status int, body string)
	requests []recorded
}

const hangUp = -1

type recorded struct {
	method    string
	parseMode string
	text      string
	threadID  string // message_thread_id; vacío = fuera de tema
	name      string // nombre del tema en createForumTopic
}

func (fake *fakeTelegram) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	requestBody, _ := io.ReadAll(request.Body)
	entry := recorded{method: method, text: string(requestBody)}
	if strings.Contains(entry.text, `"parse_mode":"Markdown"`) {
		entry.parseMode = "Markdown"
	}
	var params map[string]string
	if json.Unmarshal(requestBody, &params) == nil {
		entry.threadID, entry.name = params["message_thread_id"], params["name"]
	}
	fake.mutex.Lock()
	fake.requests = append(fake.requests, entry)
	fake.mutex.Unlock()
	var status int
	var body string
	if fake.replyTo != nil {
		status, body = fake.replyTo(entry)
	} else {
		status, body = fake.reply(method)
	}
	if status == hangUp {
		connection, _, _ := writer.(http.Hijacker).Hijack()
		connection.Close()
		return
	}
	writer.WriteHeader(status)
	fmt.Fprint(writer, body)
}

const okMessage = `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`

func newTestSender(t *testing.T, fake *fakeTelegram) (*telegramSender, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	bot, err := tele.NewBot(tele.Settings{URL: server.URL, Token: botToken, Offline: true, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return newTelegramSender(bot, nil), server
}

func TestTelegramSender_Delivers(t *testing.T) {
	fake := &fakeTelegram{reply: func(string) (int, string) { return 200, okMessage }}
	sender, _ := newTestSender(t, fake)

	if err := sender.Send(1, ports.ChannelGeneral, "*hola*"); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendPlain(1, ports.ChannelGeneral, "hola_mundo"); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests: %+v", fake.requests)
	}
	if fake.requests[0].method != "sendMessage" || fake.requests[0].parseMode != "Markdown" {
		t.Errorf("Send va en Markdown: %+v", fake.requests[0])
	}
	if fake.requests[1].method != "sendMessage" || fake.requests[1].parseMode != "" || !strings.Contains(fake.requests[1].text, "hola_mundo") {
		t.Errorf("SendPlain va sin parse_mode: %+v", fake.requests[1])
	}
}

// N9: permanente = 4xx salvo 401 y 429 (400, 413, 403); 401 = token del bot inválido;
// transitorio = red, 429, 5xx y un 4xx sin JSON.
// Cada caso pasa por el telebot real, así se cubren las tres formas de error que expone.
func TestTelegramSender_ClassifiesErrors(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		permanent    bool
		unauthorized bool
	}{
		{"markdown rechazado (descripción no mapeada: fmt.Errorf)", 400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 12"}`, true, false},
		{"413 (ErrTooLarge)", 413, `{"ok":false,"error_code":413,"description":"Request Entity Too Large"}`, true, false},
		{"bloqueado por el usuario (*tele.Error 403)", 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, true, false},
		{"flood (FloodError)", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`, false, false},
		{"429 sin retry_after", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5"}`, false, false},
		{"5xx con JSON", 502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`, false, false},
		{"5xx sin JSON", 502, `<html>502 Bad Gateway</html>`, false, false},
		{"401 token del bot inválido", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, false, true},
		{"4xx sin JSON", 400, `<html>400 Bad Request</html>`, false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &fakeTelegram{reply: func(string) (int, string) { return testCase.status, testCase.body }}
			sender, _ := newTestSender(t, fake)
			for name, send := range map[string]func() error{
				"Send":      func() error { return sender.Send(1, ports.ChannelGeneral, "x") },
				"SendPlain": func() error { return sender.SendPlain(1, ports.ChannelGeneral, "x") },
			} {
				err := send()
				if err == nil {
					t.Fatalf("%s: quiero error", name)
				}
				if errors.Is(err, ports.ErrSendPermanent) != testCase.permanent {
					t.Errorf("%s: permanente=%v, quiero %v (%v)", name, errors.Is(err, ports.ErrSendPermanent), testCase.permanent, err)
				}
				if errors.Is(err, ports.ErrSendUnauthorized) != testCase.unauthorized {
					t.Errorf("%s: 401=%v, quiero %v (%v)", name, errors.Is(err, ports.ErrSendUnauthorized), testCase.unauthorized, err)
				}
			}
		})
	}
}

// N14: red caída → transitorio, y el error no lleva el token del bot (telebot cita la URL con el token).
func TestTelegramSender_NetworkErrorIsTransientAndSanitized(t *testing.T) {
	fake := &fakeTelegram{reply: func(string) (int, string) { return 200, okMessage }}
	sender, server := newTestSender(t, fake)
	server.Close()

	for name, err := range map[string]error{
		"Send":      sender.Send(1, ports.ChannelGeneral, "x"),
		"SendPlain": sender.SendPlain(1, ports.ChannelGeneral, "x"),
	} {
		if err == nil || errors.Is(err, ports.ErrSendPermanent) {
			t.Errorf("%s: la red caída es transitoria: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), botToken) || strings.Contains(err.Error(), "SECRETO-del-bot") {
			t.Errorf("%s: el error filtra el token del bot: %v", name, err)
		}
	}
}
