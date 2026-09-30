package service

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

const (
	keyMessage       = "🔑 PEDCO rechazó tu usuario o contraseña guardados (¿la cambiaste?). Mandá /login para actualizarlos. Hasta entonces no te llegan avisos."
	noCredsMessage   = "❌ No tienes credenciales válidas. Usa /login."
	pedcoDownMessage = "⚠️ No pude consultar tus entregas ahora (PEDCO puede estar caído). Probá en un rato."
	secretPassword   = "clave-secreta-xyz"
	secretToken      = "token-secreto-abc"
)

var errTimeout = errors.New("Post \"https://pedco/login/token.php\": context deadline exceeded")

// --- fakes ---

type fakeRepository struct {
	users         map[int64]ports.UserCredentials
	order         []int64
	getUserErr    error
	saveUserErr   error
	savedSessions map[int64]string
	cleared       []int64
	marked        []int64
	savedUsers    []int64
}

func newFakeRepository(users ...ports.UserCredentials) *fakeRepository {
	repository := &fakeRepository{users: map[int64]ports.UserCredentials{}, savedSessions: map[int64]string{}}
	for _, user := range users {
		repository.users[user.ChatID] = user
		repository.order = append(repository.order, user.ChatID)
	}
	return repository
}

func (repository *fakeRepository) GetUser(chatID int64) (ports.UserCredentials, error) {
	if repository.getUserErr != nil {
		return ports.UserCredentials{}, repository.getUserErr
	}
	user, found := repository.users[chatID]
	if !found {
		return ports.UserCredentials{}, ports.ErrNoCredentials
	}
	return user, nil
}

func (repository *fakeRepository) GetAllUsers() ([]ports.UserCredentials, error) {
	var all []ports.UserCredentials
	for _, chatID := range repository.order {
		all = append(all, repository.users[chatID])
	}
	return all, nil
}

func (repository *fakeRepository) SaveUser(chatID int64, username, password string) error {
	if repository.saveUserErr != nil {
		return repository.saveUserErr
	}
	repository.savedUsers = append(repository.savedUsers, chatID)
	repository.users[chatID] = ports.UserCredentials{ChatID: chatID, User: username, Pass: password}
	return nil
}

func (repository *fakeRepository) SaveSession(chatID int64, token string) error {
	repository.savedSessions[chatID] = token
	return nil
}

func (repository *fakeRepository) ClearSession(chatID int64) error {
	repository.cleared = append(repository.cleared, chatID)
	return nil
}

func (repository *fakeRepository) MarkCredentialsRejected(chatID int64) error {
	repository.marked = append(repository.marked, chatID)
	return nil
}

type fakeSource struct {
	loginResults map[string]loginResult // por username
	fetchResults map[string]fetchResult // por token
	loginCalls   []string
	fetchCalls   []string
}

type loginResult struct {
	token string
	err   error
}

type fetchResult struct {
	items []domain.Item
	err   error
}

func (source *fakeSource) Login(username, password string) (string, error) {
	source.loginCalls = append(source.loginCalls, username)
	result, found := source.loginResults[username]
	if !found {
		return "", errors.New("login no esperado")
	}
	return result.token, result.err
}

func (source *fakeSource) FetchItems(token string, now time.Time) ([]domain.Item, error) {
	source.fetchCalls = append(source.fetchCalls, token)
	result, found := source.fetchResults[token]
	if !found {
		return nil, errors.New("fetch no esperado")
	}
	return result.items, result.err
}

type sentMessage struct {
	chatID  int64
	message string
}

type fakeSender struct {
	sent []sentMessage
	err  error
}

func (sender *fakeSender) Send(chatID int64, message string) error {
	sender.sent = append(sender.sent, sentMessage{chatID, message})
	return sender.err
}

// --- helpers ---

var testNow = time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC) // 12:00 en AR

func newTestNotifier(t *testing.T, repository *fakeRepository, source *fakeSource, sender *fakeSender) *Notifier {
	t.Helper()
	location, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("zona horaria: %v", err)
	}
	notifier := NewNotifier(repository, source, sender, location)
	notifier.delayBetween = 0
	notifier.now = func() time.Time { return testNow }
	return notifier
}

// captureLog redirige el log estándar a un buffer durante el test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	log.SetOutput(buffer)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buffer
}

func pendingItem(title string) domain.Item {
	return domain.Item{Kind: domain.Assignment, Title: title, Course: "Materia", Due: testNow.Add(48 * time.Hour), Status: domain.Pending, Link: "https://pedco/x"}
}

func doneItem(title string) domain.Item {
	item := pendingItem(title)
	item.Status = domain.Done
	return item
}

func messagesTo(sender *fakeSender, chatID int64) []string {
	var messages []string
	for _, sent := range sender.sent {
		if sent.chatID == chatID {
			messages = append(messages, sent.message)
		}
	}
	return messages
}

func contains(list []int64, value int64) bool {
	for _, element := range list {
		if element == value {
			return true
		}
	}
	return false
}

// --- ronda automática ---

// C6: token inválido/vencido → re-login → token nuevo guardado → sigue; si el re-login falla, al siguiente.
func TestNotifyAll_ExpiredTokenRelogins(t *testing.T) {
	repository := newFakeRepository(
		ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p1", Session: "viejo"},
		ports.UserCredentials{ChatID: 2, User: "beto", Pass: "p2", Session: "cookie-vieja"},
		ports.UserCredentials{ChatID: 3, User: "caro", Pass: "p3"},
	)
	source := &fakeSource{
		loginResults: map[string]loginResult{
			"ana":  {token: "nuevo"},
			"beto": {err: errTimeout},
			"caro": {token: "tok-caro"},
		},
		fetchResults: map[string]fetchResult{
			"viejo":        {err: ports.ErrSessionExpired},
			"cookie-vieja": {err: ports.ErrSessionExpired},
			"nuevo":        {items: []domain.Item{pendingItem("TP ana")}},
			"tok-caro":     {items: []domain.Item{pendingItem("TP caro")}},
		},
	}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if repository.savedSessions[1] != "nuevo" {
		t.Errorf("token nuevo de ana no guardado: %q", repository.savedSessions[1])
	}
	if !contains(repository.cleared, 1) {
		t.Errorf("no se limpió la sesión vencida de ana")
	}
	if messages := messagesTo(sender, 1); len(messages) != 1 || !strings.Contains(messages[0], "TP ana") {
		t.Errorf("ana: mensajes %q", messages)
	}
	if messages := messagesTo(sender, 2); len(messages) != 0 {
		t.Errorf("beto no debía recibir nada: %q", messages)
	}
	loginsBeto := 0
	for _, username := range source.loginCalls {
		if username == "beto" {
			loginsBeto++
		}
	}
	if loginsBeto != 1 {
		t.Errorf("re-login de beto = %d veces, quiero 1", loginsBeto)
	}
	if messages := messagesTo(sender, 3); len(messages) != 1 {
		t.Errorf("caro (siguiente usuario) no recibió su aviso: %q", messages)
	}
}

// C7 (ronda): todo hecho → no envía.
func TestNotifyAll_AllDoneSendsNothing(t *testing.T) {
	repository := newFakeRepository(ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok"})
	source := &fakeSource{fetchResults: map[string]fetchResult{"tok": {items: []domain.Item{doneItem("TP hecho")}}}}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()
	if len(sender.sent) != 0 {
		t.Errorf("no debía enviar: %+v", sender.sent)
	}
}

// C18: sin token + ErrBadCredentials → flag + UN 🔑 → sigue. Timeout → nada.
func TestNotifyAll_BadCredentialsWithoutToken(t *testing.T) {
	repository := newFakeRepository(
		ports.UserCredentials{ChatID: 1, User: "ana", Pass: "mala"},
		ports.UserCredentials{ChatID: 2, User: "beto", Pass: "p2"},
		ports.UserCredentials{ChatID: 3, User: "caro", Pass: "p3"},
	)
	source := &fakeSource{
		loginResults: map[string]loginResult{
			"ana":  {err: ports.ErrBadCredentials},
			"beto": {err: errTimeout},
			"caro": {token: "tok-caro"},
		},
		fetchResults: map[string]fetchResult{"tok-caro": {items: []domain.Item{pendingItem("TP caro")}}},
	}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if !contains(repository.marked, 1) {
		t.Errorf("flag de ana no marcado")
	}
	if messages := messagesTo(sender, 1); len(messages) != 1 || messages[0] != keyMessage {
		t.Errorf("ana: quiero UN 🔑, tengo %q", messages)
	}
	if contains(repository.marked, 2) || len(messagesTo(sender, 2)) != 0 {
		t.Errorf("beto (timeout): marked=%v mensajes=%q", repository.marked, messagesTo(sender, 2))
	}
	if len(messagesTo(sender, 3)) != 1 {
		t.Errorf("la ronda no siguió con caro")
	}
}

// C19: token vencido → re-login → ErrBadCredentials → flag + 🔑.
func TestNotifyAll_BadCredentialsOnRelogin(t *testing.T) {
	repository := newFakeRepository(ports.UserCredentials{ChatID: 1, User: "ana", Pass: "mala", Session: "vencido"})
	source := &fakeSource{
		loginResults: map[string]loginResult{"ana": {err: ports.ErrBadCredentials}},
		fetchResults: map[string]fetchResult{"vencido": {err: ports.ErrSessionExpired}},
	}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if !contains(repository.marked, 1) {
		t.Errorf("flag no marcado")
	}
	if messages := messagesTo(sender, 1); len(messages) != 1 || messages[0] != keyMessage {
		t.Errorf("quiero UN 🔑, tengo %q", messages)
	}
}

// C20: flag puesto → no se llama a Login y no hay mensaje.
func TestNotifyAll_FlaggedUserIsSkipped(t *testing.T) {
	repository := newFakeRepository(ports.UserCredentials{ChatID: 1, User: "ana", Pass: "mala", CredsRejected: true})
	source := &fakeSource{loginResults: map[string]loginResult{"ana": {err: ports.ErrBadCredentials}}}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if len(source.loginCalls) != 0 || len(source.fetchCalls) != 0 {
		t.Errorf("no debía llamar a PEDCO: login=%v fetch=%v", source.loginCalls, source.fetchCalls)
	}
	if len(sender.sent) != 0 {
		t.Errorf("no debía enviar: %+v", sender.sent)
	}
}

// C21: falla el envío del 🔑 → se loguea, flag puesto, la ronda sigue.
func TestNotifyAll_KeyMessageSendFails(t *testing.T) {
	logs := captureLog(t)
	repository := newFakeRepository(
		ports.UserCredentials{ChatID: 1, User: "ana", Pass: "mala"},
		ports.UserCredentials{ChatID: 2, User: "beto", Pass: "p2", Session: "tok-beto"},
	)
	source := &fakeSource{
		loginResults: map[string]loginResult{"ana": {err: ports.ErrBadCredentials}},
		fetchResults: map[string]fetchResult{"tok-beto": {items: []domain.Item{pendingItem("TP beto")}}},
	}
	sender := &fakeSender{err: errors.New("telegram caído")}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if !contains(repository.marked, 1) {
		t.Errorf("flag no marcado")
	}
	if !strings.Contains(logs.String(), "telegram caído") {
		t.Errorf("el fallo de envío no quedó en el log:\n%s", logs)
	}
	if len(messagesTo(sender, 2)) != 1 {
		t.Errorf("la ronda no siguió con beto")
	}
}

// --- /tps ---

// C22 y C7 (/tps).
func TestNotifyOne(t *testing.T) {
	cases := []struct {
		name       string
		user       *ports.UserCredentials
		getUserErr error
		login      loginResult
		fetch      map[string]fetchResult
		want       string // mensaje exacto, o fragmento si wantPart
		wantPart   bool
		wantMark   bool
		wantLogins int
	}{
		{name: "sin fila", want: noCredsMessage},
		{name: "error de descifrado", getUserErr: errors.New("descifrado falló (¿cambió SECRET_KEY?)"), want: noCredsMessage},
		{name: "flag puesto", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok", CredsRejected: true},
			fetch: map[string]fetchResult{"tok": {items: []domain.Item{pendingItem("TP")}}}, want: noCredsMessage},
		{name: "credenciales malas", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p"},
			login: loginResult{err: ports.ErrBadCredentials}, want: noCredsMessage, wantMark: true, wantLogins: 1},
		{name: "PEDCO caído", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p"},
			login: loginResult{err: errTimeout}, want: pedcoDownMessage, wantLogins: 1},
		{name: "fetch con otro error", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok"},
			fetch: map[string]fetchResult{"tok": {err: errors.New("wsaccessusersuspended")}}, want: pedcoDownMessage},
		{name: "ventana vacía", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok"},
			fetch: map[string]fetchResult{"tok": {}}, want: "✅ ¡No tienes entregas pendientes! Relájate."},
		{name: "todo hecho envía la lista", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok"},
			fetch: map[string]fetchResult{"tok": {items: []domain.Item{doneItem("TP entregado")}}}, want: "✅ TP entregado · Materia", wantPart: true},
		{name: "usa el token guardado sin login", user: &ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "tok"},
			fetch: map[string]fetchResult{"tok": {items: []domain.Item{pendingItem("TP pendiente")}}}, want: "📝 Tarea: TP pendiente", wantPart: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newFakeRepository()
			if testCase.user != nil {
				repository = newFakeRepository(*testCase.user)
			}
			repository.getUserErr = testCase.getUserErr
			source := &fakeSource{loginResults: map[string]loginResult{"ana": testCase.login}, fetchResults: testCase.fetch}
			got := newTestNotifier(t, repository, source, &fakeSender{}).NotifyOne(1)

			if testCase.wantPart {
				if !strings.Contains(got, testCase.want) {
					t.Errorf("respuesta sin %q:\n%s", testCase.want, got)
				}
			} else if got != testCase.want {
				t.Errorf("respuesta = %q, quiero %q", got, testCase.want)
			}
			if contains(repository.marked, 1) != testCase.wantMark {
				t.Errorf("marked = %v, quiero marcado=%v", repository.marked, testCase.wantMark)
			}
			if len(source.loginCalls) != testCase.wantLogins {
				t.Errorf("logins = %v, quiero %d", source.loginCalls, testCase.wantLogins)
			}
		})
	}
}

// --- /login ---

// C23.
func TestLinkAccount(t *testing.T) {
	cases := []struct {
		name        string
		saveUserErr error
		login       loginResult
		want        string
		wantSession string
		wantMark    bool
		wantSaved   bool
		wantLogins  int
	}{
		{name: "credenciales buenas", login: loginResult{token: "tok-nuevo"},
			want: "🔐 Listo, entré a PEDCO con tu cuenta. Usá /tps para ver tus entregas.", wantSession: "tok-nuevo", wantSaved: true, wantLogins: 1},
		{name: "credenciales malas", login: loginResult{err: ports.ErrBadCredentials},
			want: "❌ PEDCO rechazó ese usuario o contraseña. Probá /login de nuevo.", wantMark: true, wantSaved: true, wantLogins: 1},
		{name: "PEDCO caído", login: loginResult{err: errTimeout},
			want: "💾 Guardé tus datos, pero PEDCO no responde ahora; los pruebo en la próxima ronda.", wantSaved: true, wantLogins: 1},
		{name: "falla el guardado", saveUserErr: errors.New("disk I/O error"), login: loginResult{token: "tok"},
			want: "❌ Hubo un error guardando tus datos."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newFakeRepository()
			repository.saveUserErr = testCase.saveUserErr
			source := &fakeSource{loginResults: map[string]loginResult{"ana": testCase.login}}
			got := newTestNotifier(t, repository, source, &fakeSender{}).LinkAccount(1, "ana", "clave")

			if got != testCase.want {
				t.Errorf("respuesta = %q, quiero %q", got, testCase.want)
			}
			if repository.savedSessions[1] != testCase.wantSession {
				t.Errorf("sesión guardada = %q, quiero %q", repository.savedSessions[1], testCase.wantSession)
			}
			if contains(repository.marked, 1) != testCase.wantMark {
				t.Errorf("marked = %v, quiero marcado=%v", repository.marked, testCase.wantMark)
			}
			if contains(repository.savedUsers, 1) != testCase.wantSaved {
				t.Errorf("savedUsers = %v, quiero guardado=%v", repository.savedUsers, testCase.wantSaved)
			}
			if len(source.loginCalls) != testCase.wantLogins {
				t.Errorf("logins = %v, quiero %d", source.loginCalls, testCase.wantLogins)
			}
		})
	}
}

// C9 + C26: ni el token ni la contraseña salen en logs ni mensajes del servicio,
// por ningún camino (ronda, /tps, /login).
func TestSecretsNeverInLogsOrMessages(t *testing.T) {
	logs := captureLog(t)
	var outputs []string

	repository := newFakeRepository(
		ports.UserCredentials{ChatID: 1, User: "ana", Pass: secretPassword, Session: secretToken},
		ports.UserCredentials{ChatID: 2, User: "beto", Pass: secretPassword},
		ports.UserCredentials{ChatID: 3, User: "caro", Pass: secretPassword},
	)
	source := &fakeSource{
		loginResults: map[string]loginResult{
			"ana":  {err: errTimeout},
			"beto": {err: ports.ErrBadCredentials},
			"caro": {token: secretToken + "-2"},
		},
		fetchResults: map[string]fetchResult{
			secretToken:        {err: ports.ErrSessionExpired},
			secretToken + "-2": {err: errors.New("mod_assign_get_assignments: respuesta ilegible")},
		},
	}
	sender := &fakeSender{}
	notifier := newTestNotifier(t, repository, source, sender)
	notifier.NotifyAll()
	outputs = append(outputs, notifier.NotifyOne(1), notifier.NotifyOne(3))

	repository.saveUserErr = errors.New("disk I/O error")
	outputs = append(outputs, notifier.LinkAccount(4, "dani", secretPassword))
	repository.saveUserErr = nil
	source.loginResults["dani"] = loginResult{token: secretToken + "-3"}
	outputs = append(outputs, notifier.LinkAccount(4, "dani", secretPassword))
	source.loginResults["dani"] = loginResult{err: ports.ErrBadCredentials}
	outputs = append(outputs, notifier.LinkAccount(4, "dani", secretPassword))
	source.loginResults["dani"] = loginResult{err: errTimeout}
	outputs = append(outputs, notifier.LinkAccount(4, "dani", secretPassword))

	for _, sent := range sender.sent {
		outputs = append(outputs, sent.message)
	}
	outputs = append(outputs, logs.String())
	if logs.Len() == 0 {
		t.Fatal("control positivo: el log capturado está vacío")
	}
	for _, output := range outputs {
		if strings.Contains(output, secretToken) || strings.Contains(output, secretPassword) {
			t.Errorf("salida con un secreto:\n%s", output)
		}
	}
}
