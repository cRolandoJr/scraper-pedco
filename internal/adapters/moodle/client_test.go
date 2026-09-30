package moodle

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

const validToken = "tok-secreto-123"

func argentina(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("zona horaria: %v", err)
	}
	return location
}

// fakeMoodle simula server.php. Exige POST sin query string (todo va en el body).
type fakeMoodle struct {
	t            *testing.T
	location     *time.Location
	mutex        sync.Mutex
	statusCalls  []string
	overrideJSON string // si no es vacío, toda llamada a server.php responde esto
}

func (fake *fakeMoodle) at(month time.Month, day, hour, minute int) int64 {
	return time.Date(2026, month, day, hour, minute, 0, 0, fake.location).Unix()
}

func (fake *fakeMoodle) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.URL.Path != "/webservice/rest/server.php" {
		fake.t.Errorf("request inesperado: %s %s", request.Method, request.URL.String())
		http.Error(writer, "bad", http.StatusBadRequest)
		return
	}
	if err := request.ParseForm(); err != nil {
		fake.t.Errorf("form: %v", err)
	}
	form := request.PostForm
	if form.Get("moodlewsrestformat") != "json" {
		fake.t.Errorf("falta moodlewsrestformat=json en %s", form.Get("wsfunction"))
	}
	if fake.overrideJSON != "" {
		fmt.Fprint(writer, fake.overrideJSON)
		return
	}
	if form.Get("wstoken") != validToken {
		fmt.Fprint(writer, `{"exception":"moodle_exception","errorcode":"invalidtoken","message":"Invalid token - token not found"}`)
		return
	}
	var body any
	switch form.Get("wsfunction") {
	case "core_webservice_get_site_info":
		body = map[string]any{"userid": 7}
	case "core_enrol_get_users_courses":
		if form.Get("userid") != "7" {
			fake.t.Errorf("userid = %q", form.Get("userid"))
		}
		body = []map[string]any{
			{"id": 10, "fullname": "2026 - Administración de Servicios"},
			{"id": 20, "fullname": "2026-Administracion de Sistemas"},
			{"id": 30, "fullname": "Conceptos de Bases de Datos"},
		}
	case "mod_assign_get_assignments":
		fake.requireCourseIDs(form.Get("courseids[0]"), form.Get("courseids[1]"), form.Get("courseids[2]"))
		assignment := func(id, cmid int, name string, due, opens int64) map[string]any {
			return map[string]any{"id": id, "cmid": cmid, "name": name, "duedate": due, "allowsubmissionsfromdate": opens, "teamsubmission": 0}
		}
		body = map[string]any{"courses": []map[string]any{
			{"id": 10, "fullname": "2026 - Administración de Servicios", "assignments": []map[string]any{
				assignment(101, 1001, "TP2 entregado", fake.at(9, 29, 23, 55), fake.at(9, 1, 0, 0)),
				assignment(102, 1002, "TP3 nuevo", fake.at(10, 13, 23, 55), fake.at(9, 20, 0, 0)),
				assignment(104, 1004, "Vencida", fake.at(9, 28, 23, 55), 0),
				assignment(105, 1005, "Lejana", fake.at(10, 14, 0, 0), 0),
				assignment(107, 1007, "Borrador", fake.at(10, 5, 23, 55), 0),
				assignment(108, 1008, "Grupal", fake.at(10, 6, 23, 55), 0),
				assignment(109, 1009, "Falla estado", fake.at(10, 7, 23, 55), 0),
			}},
			{"id": 20, "fullname": "2026-Administracion de Sistemas", "assignments": []map[string]any{
				assignment(103, 1003, "Practico 3 cerrado", fake.at(10, 12, 23, 55), fake.at(10, 1, 21, 35)),
			}},
		}}
	case "mod_quiz_get_quizzes_by_courses":
		fake.requireCourseIDs(form.Get("courseids[0]"), form.Get("courseids[1]"), form.Get("courseids[2]"))
		quiz := func(id, cm, course int, name string, open, close int64) map[string]any {
			return map[string]any{"id": id, "coursemodule": cm, "course": course, "name": name, "timeopen": open, "timeclose": close}
		}
		body = map[string]any{"quizzes": []map[string]any{
			quiz(201, 2001, 30, "Parcial 1", fake.at(9, 28, 18, 0), fake.at(9, 30, 20, 0)),
			quiz(202, 2002, 30, "Abierto sin intentos", fake.at(9, 28, 8, 0), fake.at(10, 2, 23, 59)),
			quiz(203, 2003, 30, "Primer parcial", fake.at(10, 5, 10, 0), fake.at(10, 5, 22, 0)),
			quiz(204, 2004, 30, "Coloquio", fake.at(10, 7, 18, 38), 0),
			quiz(205, 2005, 30, "Overdue", fake.at(9, 28, 8, 0), fake.at(10, 3, 23, 59)),
			quiz(206, 2006, 30, "Abandonado", fake.at(9, 28, 8, 0), fake.at(10, 3, 23, 59)),
			quiz(208, 2008, 30, "Quiz vencido", fake.at(9, 20, 8, 0), fake.at(9, 28, 23, 59)),
		}}
	case "mod_assign_get_submission_status":
		assignID := form.Get("assignid")
		fake.recordStatusCall("assign:" + assignID)
		if form.Has("userid") {
			fake.t.Errorf("get_submission_status no debe mandar userid")
		}
		switch assignID {
		case "101":
			body = map[string]any{"lastattempt": map[string]any{"submission": map[string]any{"status": "submitted"}}}
		case "102", "103":
			body = map[string]any{"lastattempt": map[string]any{"submission": map[string]any{"status": "new"}}}
		case "107":
			body = map[string]any{"lastattempt": map[string]any{"submission": map[string]any{"status": "draft"}}}
		case "108":
			body = map[string]any{"lastattempt": map[string]any{
				"submission":     map[string]any{"status": "new"},
				"teamsubmission": map[string]any{"status": "submitted"},
			}}
		case "109":
			fmt.Fprint(writer, `{"exception":"required_capability_exception","errorcode":"nopermissions","message":"Sin permiso"}`)
			return
		default:
			fake.t.Errorf("status pedido para assign %s", assignID)
		}
	case "mod_quiz_get_user_attempts":
		quizID := form.Get("quizid")
		fake.recordStatusCall("quiz:" + quizID)
		if form.Get("status") != "all" {
			fake.t.Errorf("get_user_attempts sin status=all")
		}
		states := map[string][]string{
			"201": {"finished"},
			"202": {},
			"203": {},
			"204": {},
			"205": {"overdue"},
			"206": {"abandoned"},
		}[quizID]
		attempts := []map[string]any{}
		for _, state := range states {
			attempts = append(attempts, map[string]any{"state": state})
		}
		body = map[string]any{"attempts": attempts}
	default:
		fake.t.Errorf("wsfunction inesperada %q", form.Get("wsfunction"))
		return
	}
	_ = json.NewEncoder(writer).Encode(body)
}

func (fake *fakeMoodle) requireCourseIDs(ids ...string) {
	if strings.Join(ids, ",") != "10,20,30" {
		fake.t.Errorf("courseids = %v", ids)
	}
}

func (fake *fakeMoodle) recordStatusCall(call string) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.statusCalls = append(fake.statusCalls, call)
}

func newFake(t *testing.T) (*fakeMoodle, *Client) {
	location := argentina(t)
	fake := &fakeMoodle{t: t, location: location}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, NewClient(server.URL, location)
}

func TestFetchItems_StatusWindowAndFields(t *testing.T) {
	fake, client := newFake(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, fake.location)

	items, err := client.FetchItems(validToken, now)
	if err != nil {
		t.Fatalf("FetchItems: %v", err)
	}
	byTitle := map[string]domain.Item{}
	count := map[string]int{}
	for _, item := range items {
		byTitle[item.Title] = item
		count[item.Title]++
	}

	type want struct {
		kind    domain.Kind
		status  domain.Status
		course  string
		due     int64
		opensAt int64 // 0 = cero
		link    string
	}
	base := client.baseURL
	cases := map[string]want{
		// C1
		"TP2 entregado": {domain.Assignment, domain.Done, "Administración de Servicios", fake.at(9, 29, 23, 55), fake.at(9, 1, 0, 0), base + "/mod/assign/view.php?id=1001"},
		// C2 (y borde de ventana: 13/10 23:55 entra)
		"TP3 nuevo": {domain.Assignment, domain.Pending, "Administración de Servicios", fake.at(10, 13, 23, 55), fake.at(9, 20, 0, 0), base + "/mod/assign/view.php?id=1002"},
		// C3 + C16
		"Practico 3 cerrado": {domain.Assignment, domain.NotOpen, "Administracion de Sistemas", fake.at(10, 12, 23, 55), fake.at(10, 1, 21, 35), base + "/mod/assign/view.php?id=1003"},
		"Borrador":           {domain.Assignment, domain.Draft, "Administración de Servicios", fake.at(10, 5, 23, 55), 0, base + "/mod/assign/view.php?id=1007"},
		"Grupal":             {domain.Assignment, domain.Done, "Administración de Servicios", fake.at(10, 6, 23, 55), 0, base + "/mod/assign/view.php?id=1008"},
		// C12
		"Falla estado": {domain.Assignment, domain.Unknown, "Administración de Servicios", fake.at(10, 7, 23, 55), 0, base + "/mod/assign/view.php?id=1009"},
		// C4
		"Parcial 1":            {domain.Quiz, domain.Done, "Conceptos de Bases de Datos", fake.at(9, 30, 20, 0), fake.at(9, 28, 18, 0), base + "/mod/quiz/view.php?id=2001"},
		"Abierto sin intentos": {domain.Quiz, domain.Pending, "Conceptos de Bases de Datos", fake.at(10, 2, 23, 59), fake.at(9, 28, 8, 0), base + "/mod/quiz/view.php?id=2002"},
		"Primer parcial":       {domain.Quiz, domain.NotOpen, "Conceptos de Bases de Datos", fake.at(10, 5, 22, 0), fake.at(10, 5, 10, 0), base + "/mod/quiz/view.php?id=2003"},
		"Coloquio":             {domain.Quiz, domain.NotOpen, "Conceptos de Bases de Datos", fake.at(10, 7, 18, 38), fake.at(10, 7, 18, 38), base + "/mod/quiz/view.php?id=2004"},
		// C15
		"Overdue":    {domain.Quiz, domain.InProgress, "Conceptos de Bases de Datos", fake.at(10, 3, 23, 59), fake.at(9, 28, 8, 0), base + "/mod/quiz/view.php?id=2005"},
		"Abandonado": {domain.Quiz, domain.Unknown, "Conceptos de Bases de Datos", fake.at(10, 3, 23, 59), fake.at(9, 28, 8, 0), base + "/mod/quiz/view.php?id=2006"},
	}
	for title, expected := range cases {
		item, found := byTitle[title]
		if !found {
			t.Errorf("%q no apareció", title)
			continue
		}
		// C5: un quiz, una entrada
		if count[title] != 1 {
			t.Errorf("%q aparece %d veces", title, count[title])
		}
		if item.Kind != expected.kind || item.Status != expected.status || item.Course != expected.course || item.Link != expected.link {
			t.Errorf("%q = {kind %v status %v course %q link %q}, quiero {kind %v status %v course %q link %q}",
				title, item.Kind, item.Status, item.Course, item.Link, expected.kind, expected.status, expected.course, expected.link)
		}
		if item.Due.Unix() != expected.due {
			t.Errorf("%q Due = %v, quiero %v", title, item.Due, time.Unix(expected.due, 0))
		}
		if expected.opensAt == 0 {
			if !item.OpensAt.IsZero() {
				t.Errorf("%q OpensAt = %v, quiero cero", title, item.OpensAt)
			}
		} else if item.OpensAt.Unix() != expected.opensAt {
			t.Errorf("%q OpensAt = %v, quiero %v", title, item.OpensAt, time.Unix(expected.opensAt, 0))
		}
	}
	// C8: vencido o más allá del fin del día de now+14 → fuera
	for _, excluded := range []string{"Vencida", "Lejana", "Quiz vencido"} {
		if _, found := byTitle[excluded]; found {
			t.Errorf("%q no debía entrar a la ventana", excluded)
		}
	}
	if len(items) != len(cases) {
		t.Errorf("salieron %d ítems, quiero %d", len(items), len(cases))
	}
	// El estado se pide solo para lo que quedó en la ventana.
	for _, call := range fake.statusCalls {
		if call == "assign:104" || call == "assign:105" || call == "quiz:208" {
			t.Errorf("se pidió estado fuera de ventana: %s", call)
		}
	}
}

// C6 (lado adaptador): invalidtoken y accessexception → ErrSessionExpired; otro errorcode no.
func TestFetchItems_ErrorCodes(t *testing.T) {
	cases := []struct {
		name        string
		response    string
		wantExpired bool
		wantInError string
	}{
		{"invalidtoken", `{"exception":"moodle_exception","errorcode":"invalidtoken","message":"Invalid token - token not found"}`, true, ""},
		{"accessexception", `{"exception":"webservice_access_exception","errorcode":"accessexception","message":"Access control exception"}`, true, ""},
		{"otro", `{"exception":"moodle_exception","errorcode":"wsaccessusersuspended","message":"Usuario suspendido"}`, false, "Usuario suspendido"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake, client := newFake(t)
			fake.overrideJSON = testCase.response
			_, err := client.FetchItems(validToken, time.Date(2026, 9, 29, 12, 0, 0, 0, fake.location))
			if err == nil {
				t.Fatal("quiero error")
			}
			if errors.Is(err, ports.ErrSessionExpired) != testCase.wantExpired {
				t.Errorf("errors.Is(ErrSessionExpired) = %v, quiero %v (err: %v)", !testCase.wantExpired, testCase.wantExpired, err)
			}
			if testCase.wantInError != "" && !strings.Contains(err.Error(), testCase.wantInError) {
				t.Errorf("el error %q no trae el message de Moodle %q", err, testCase.wantInError)
			}
		})
	}
}

// C9: ningún error del adaptador lleva el token ni la contraseña.
func TestErrorsNeverContainSecrets(t *testing.T) {
	const password = "clave-super-secreta"
	responses := map[string]func(http.ResponseWriter){
		"exception": func(writer http.ResponseWriter) {
			fmt.Fprint(writer, `{"exception":"moodle_exception","errorcode":"otro","message":"falló"}`)
		},
		"http500": func(writer http.ResponseWriter) { http.Error(writer, "boom", http.StatusInternalServerError) },
		"html":    func(writer http.ResponseWriter) { fmt.Fprint(writer, "<html>mantenimiento</html>") },
		"invalidlogin": func(writer http.ResponseWriter) {
			fmt.Fprint(writer, `{"error":"Invalid login, please try again","errorcode":"invalidlogin"}`)
		},
	}
	for name, respond := range responses {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { respond(writer) }))
			defer server.Close()
			client := NewClient(server.URL, argentina(t))

			_, fetchErr := client.FetchItems(validToken, time.Now())
			_, loginErr := client.Login("legajo", password)
			for _, err := range []error{fetchErr, loginErr} {
				if err == nil {
					t.Fatal("quiero error")
				}
				if strings.Contains(err.Error(), validToken) || strings.Contains(err.Error(), password) {
					t.Errorf("el error filtra un secreto: %q", err)
				}
			}
		})
	}
}

// C13: un servidor colgado corta en ≤ 15 s con error.
func TestFetchItems_HangingServerTimesOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client := NewClient(server.URL, argentina(t))

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		_, err := client.FetchItems(validToken, time.Now())
		done <- result{err, time.Since(start)}
	}()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("quiero error por timeout")
		}
		if got.elapsed > 15*time.Second+500*time.Millisecond {
			t.Errorf("tardó %v, quiero ≤ 15 s", got.elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no cortó en 20 s")
	}
}

// C17: solo invalidlogin es ErrBadCredentials.
func TestLogin(t *testing.T) {
	cases := []struct {
		name      string
		handler   http.HandlerFunc
		wantToken string
		wantBad   bool
	}{
		{"token", func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"token":"nuevo-token","privatetoken":null}`)
		}, "nuevo-token", false},
		{"invalidlogin", func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"error":"Invalid login, please try again","errorcode":"invalidlogin","stacktrace":null,"debuginfo":null,"reproductionlink":null}`)
		}, "", true},
		{"timeout", func(http.ResponseWriter, *http.Request) { time.Sleep(300 * time.Millisecond) }, "", false},
		{"http500", func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, `{"errorcode":"invalidlogin"}`, http.StatusInternalServerError)
		}, "", false},
		{"http503 mantenimiento", func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "<html>Mantenimiento</html>", http.StatusServiceUnavailable)
		}, "", false},
		{"http200 html", func(writer http.ResponseWriter, _ *http.Request) { fmt.Fprint(writer, "<html>invalidlogin</html>") }, "", false},
		{"json sin errorcode", func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"error":"Invalid login, please try again"}`)
		}, "", false},
		{"otro errorcode", func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"error":"No confirmado","errorcode":"usernotconfirmed"}`)
		}, "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/login/token.php" || request.URL.RawQuery != "" {
					t.Errorf("request inesperado: %s %s", request.Method, request.URL.String())
				}
				_ = request.ParseForm()
				if request.PostForm.Get("username") != "legajo" || request.PostForm.Get("password") != "clave" || request.PostForm.Get("service") != "moodle_mobile_app" {
					t.Errorf("form = %v", request.PostForm)
				}
				testCase.handler(writer, request)
			}))
			defer server.Close()
			client := NewClient(server.URL, argentina(t))
			client.httpClient.Timeout = 100 * time.Millisecond

			token, err := client.Login("legajo", "clave")
			if testCase.wantToken != "" {
				if err != nil || token != testCase.wantToken {
					t.Fatalf("Login = (%q, %v), quiero (%q, nil)", token, err, testCase.wantToken)
				}
				return
			}
			if err == nil {
				t.Fatalf("quiero error, token %q", token)
			}
			if errors.Is(err, ports.ErrBadCredentials) != testCase.wantBad {
				t.Errorf("errors.Is(ErrBadCredentials) = %v, quiero %v (err: %v)", !testCase.wantBad, testCase.wantBad, err)
			}
		})
	}
}
