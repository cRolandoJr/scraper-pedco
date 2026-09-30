package service

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

// --- fakes de novedades ---

// newsSource es la mitad de novedades del fakeSource. Los mapas van por courseID;
// un curso ausente de todos los mapas no tiene ítems.
type newsSource struct {
	userID        int
	courses       []domain.Course
	profileErr    error
	posts         map[int][]domain.ForumPost
	forumErrs     map[int]error
	grades        map[int][]domain.GradeItem
	gradeErrs     map[int]error
	materials     map[int][]domain.Material
	materialErrs  map[int]error
	profileTokens []string
	calls         []string // "grades:<id>", "materials:<id>"
}

func (source *newsSource) Profile(token string) (int, []domain.Course, error) {
	source.profileTokens = append(source.profileTokens, token)
	return source.userID, source.courses, source.profileErr
}

func (source *newsSource) Forums(token string, courses []domain.Course) (map[int][]domain.ForumPost, map[int]error) {
	posts, errs := map[int][]domain.ForumPost{}, map[int]error{}
	for _, course := range courses {
		if err := source.forumErrs[course.ID]; err != nil {
			errs[course.ID] = err
			continue
		}
		posts[course.ID] = source.posts[course.ID]
	}
	return posts, errs
}

func (source *newsSource) Grades(token string, courseID, userID int) ([]domain.GradeItem, error) {
	source.calls = append(source.calls, fmt.Sprintf("grades:%d", courseID))
	return source.grades[courseID], source.gradeErrs[courseID]
}

func (source *newsSource) Materials(token string, courseID int) ([]domain.Material, error) {
	source.calls = append(source.calls, fmt.Sprintf("materials:%d", courseID))
	return source.materials[courseID], source.materialErrs[courseID]
}

func (source *newsSource) called(call string) bool {
	for _, made := range source.calls {
		if made == call {
			return true
		}
	}
	return false
}

// fakeSeen es la mitad SeenRepository del fakeRepository.
type fakeSeen struct {
	seen      map[string]bool
	baselines map[string]bool
	markErr   error // si no es nil, MarkSeen falla (N10)
}

func newFakeSeen() fakeSeen {
	return fakeSeen{seen: map[string]bool{}, baselines: map[string]bool{}}
}

func seenKey(chatID int64, kind, key string) string {
	return fmt.Sprintf("%d/%s/%s", chatID, kind, key)
}

func baseKey(chatID int64, kind string, courseID int) string {
	return fmt.Sprintf("%d/%s/%d", chatID, kind, courseID)
}

func (seen *fakeSeen) HasBaseline(chatID int64, kind string, courseID int) (bool, error) {
	return seen.baselines[baseKey(chatID, kind, courseID)], nil
}

func (seen *fakeSeen) SetBaseline(chatID int64, kind string, courseID int) error {
	seen.baselines[baseKey(chatID, kind, courseID)] = true
	return nil
}

func (seen *fakeSeen) IsSeen(chatID int64, kind, key string) (bool, error) {
	return seen.seen[seenKey(chatID, kind, key)], nil
}

func (seen *fakeSeen) MarkSeen(chatID int64, kind, key string) error {
	if seen.markErr != nil {
		return seen.markErr
	}
	seen.seen[seenKey(chatID, kind, key)] = true
	return nil
}

func (sender *fakeSender) SendPlain(chatID int64, message string) error {
	return sender.record(sentMessage{chatID: chatID, message: message, kind: "plain"})
}

func (sender *fakeSender) record(message sentMessage) error {
	err := sender.err
	if sender.fail != nil {
		err = sender.fail(message)
	}
	message.err = err
	sender.sent = append(sender.sent, message)
	return err
}

// delivered son los envíos que salieron bien.
func (sender *fakeSender) delivered() []sentMessage {
	var ok []sentMessage
	for _, sent := range sender.sent {
		if sent.err == nil {
			ok = append(ok, sent)
		}
	}
	return ok
}

var (
	errPermanent    = fmt.Errorf("telegram: Bad Request: can't parse entities (400): %w", ports.ErrSendPermanent)
	errTransient    = errors.New("telegram: Too Many Requests: retry after 5 (429)")
	errUnauthorized = fmt.Errorf("telegram: Unauthorized (401): %w", ports.ErrSendUnauthorized)
)

// --- escenario ---

const pedco = "https://pedco.uncoma.edu.ar"

var (
	courseBD   = domain.Course{ID: 10325, Name: "Conceptos de Bases de Datos", GradesLink: pedco + "/grade/report/user/index.php?id=10325"}
	courseIPOO = domain.Course{ID: 10327, Name: "Introduc. a la Programación Orientada a Objetos", GradesLink: pedco + "/grade/report/user/index.php?id=10327"}
)

func post(discussion int, subject string) domain.ForumPost {
	return domain.ForumPost{Discussion: discussion, Subject: subject, Author: "Enrique Corujo", MessageHTML: "<p>texto</p>",
		Link: fmt.Sprintf("%s/mod/forum/discuss.php?d=%d", pedco, discussion)}
}

func grade(id int, name string, raw *float64, formatted string) domain.GradeItem {
	return domain.GradeItem{ID: id, Name: name, Type: "mod", Raw: raw, Formatted: formatted}
}

func score(value float64) *float64 { return &value }

func link(cmid int, modname, name string) domain.Material {
	return domain.Material{CMID: cmid, ModName: modname, Name: name, Link: fmt.Sprintf("%s/mod/%s/view.php?id=%d", pedco, modname, cmid)}
}

const satisfactorio = `<i class="icon fa fa-check text-success fa-fw inline"  title="Aprobado" role="img" aria-label="Aprobado"></i>Satisfactorio`

// newsScenario: un usuario con token válido, sin entregas pendientes, y la fuente de novedades vacía.
func newsScenario(t *testing.T) (*fakeRepository, *fakeSource, *fakeSender, *Notifier) {
	t.Helper()
	repository := newFakeRepository(ports.UserCredentials{ChatID: 1, User: "ana", Pass: secretPassword, Session: secretToken})
	source := &fakeSource{fetchResults: map[string]fetchResult{secretToken: {}}}
	source.userID = 7
	source.courses = []domain.Course{courseBD, courseIPOO}
	source.posts = map[int][]domain.ForumPost{}
	source.forumErrs = map[int]error{}
	source.grades = map[int][]domain.GradeItem{}
	source.gradeErrs = map[int]error{}
	source.materials = map[int][]domain.Material{}
	source.materialErrs = map[int]error{}
	sender := &fakeSender{}
	return repository, source, sender, newTestNotifier(t, repository, source, sender)
}

func texts(messages []sentMessage) []string {
	var all []string
	for _, message := range messages {
		all = append(all, message.kind+": "+message.message)
	}
	return all
}

// --- N13: formato ---

func TestNoveltyFormat_Golden(t *testing.T) {
	forum := formatForumPost(courseBD, domain.ForumPost{
		Discussion: 563356, Subject: "Parcial_1 *urgente* [BD]", Author: "Enrique Corujo",
		MessageHTML: `<p dir="ltr" style="text-align:left;">Hola &amp; bienvenidos: usen <b>snake_case</b> y *negrita*</p><p>Link: [aquí]</p>`,
		Link:        pedco + "/mod/forum/discuss.php?d=563356",
	})
	grades := formatGrades(domain.Course{ID: 10325, Name: "[AyS] Redes_2", GradesLink: pedco + "/grade/report/user/index.php?id=10325"},
		[]domain.GradeItem{grade(1, "TP_1 *final*", score(2), satisfactorio), grade(2, "<p>Parcial &amp; recup</p>", score(8), "8,00")})
	material := formatMaterials(courseBD, []domain.Material{link(910436, "resource", "Guía_1 <b>[v2]</b> &amp; *extra*")})

	got := strings.Join([]string{forum.markdown, forum.plain, grades.markdown, grades.plain, material.markdown, material.plain}, "\n=====\n")
	golden, err := os.ReadFile("testdata/novelty.golden")
	if err != nil {
		t.Fatalf("golden: %v", err)
	}
	if want := strings.TrimSuffix(string(golden), "\n"); got != want {
		t.Errorf("distinto del golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// N13: el corte de 400 runas se hace ANTES de escapar: nunca deja una barra colgando.
func TestNoveltyFormat_CutBeforeEscape(t *testing.T) {
	text := strings.Repeat("a", 399) + "_bbb"
	forum := formatForumPost(courseBD, domain.ForumPost{Subject: "s", Author: "x", MessageHTML: text, Link: pedco})
	if !strings.Contains(forum.markdown, "\n"+strings.Repeat("a", 399)+`\_…`+"\n") {
		t.Errorf("corte markdown mal:\n%s", forum.markdown)
	}
	if !strings.Contains(forum.plain, "\n"+strings.Repeat("a", 399)+"_…\n") {
		t.Errorf("corte plano mal:\n%s", forum.plain)
	}

	runes := formatForumPost(courseBD, domain.ForumPost{Subject: "s", Author: "x", MessageHTML: strings.Repeat("ñ", 450), Link: pedco})
	if !strings.Contains(runes.plain, "\n"+strings.Repeat("ñ", 400)+"…\n") {
		t.Errorf("el corte es por runas, no por bytes:\n%s", runes.plain)
	}
	short := formatForumPost(courseBD, domain.ForumPost{Subject: "s", Author: "x", MessageHTML: strings.Repeat("a", 400), Link: pedco})
	if strings.Contains(short.plain, "…") {
		t.Errorf("400 runas justas no llevan …")
	}
}

// --- N1 / N2 / N3: línea de base por curso ---

func TestNovelties_FirstRoundIsBaseline(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	source.posts[10325] = []domain.ForumPost{post(563356, "Acceso al 1er Parcial")}
	source.grades[10325] = []domain.GradeItem{grade(144101, "Entrega TP1 - 25/08", score(2), satisfactorio)}
	source.gradeErrs[10327] = errors.New("gradereport_user_get_grade_items: error/notingroup (notingroup)")
	source.materials[10325] = []domain.Material{link(910436, "resource", "Presentación de la Materia"), link(910438, "url", "Instructivo")}

	notifier.NotifyAll()

	if len(sender.sent) != 0 {
		t.Fatalf("N1: la primera ronda no avisa novedades: %q", texts(sender.sent))
	}
	for _, key := range []string{"1/forum/563356", "1/grade/144101:2", "1/material/910436", "1/material/910438"} {
		if !repository.seen[key] {
			t.Errorf("N1: %s debía quedar visto; seen=%v", key, repository.seen)
		}
	}
	for _, key := range []string{"1/forum/10325", "1/forum/10327", "1/grade/10325", "1/material/10325", "1/material/10327"} {
		if !repository.baselines[key] {
			t.Errorf("N1: falta la base %s; bases=%v", key, repository.baselines)
		}
	}
	if repository.baselines["1/grade/10327"] {
		t.Errorf("N3: el curso que falló no registra base")
	}
}

func TestNovelties_EmptyCourseBaselineThenFirstItemNotifies(t *testing.T) {
	_, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()
	if len(sender.sent) != 0 {
		t.Fatalf("control: la base vacía no avisa: %q", texts(sender.sent))
	}

	source.posts[10325] = []domain.ForumPost{post(563356, "Acceso al 1er Parcial")}
	notifier.NotifyAll()

	if got := sender.delivered(); len(got) != 1 || !strings.Contains(got[0].message, "Acceso al 1er Parcial") {
		t.Errorf("N2: el primer ítem tras una base vacía debe avisar: %q", texts(sender.sent))
	}
}

func TestNovelties_FailedCourseBaselinesWhenItAnswers(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	source.gradeErrs[10327] = errors.New("notingroup")
	notifier.NotifyAll()

	source.gradeErrs[10327] = nil
	source.grades[10327] = []domain.GradeItem{grade(9, "TP IPOO", score(7), "7,00")}
	source.grades[10325] = []domain.GradeItem{grade(144101, "Entrega TP1 - 25/08", score(2), satisfactorio)}
	notifier.NotifyAll()

	got := sender.delivered()
	if len(got) != 1 || !strings.Contains(got[0].message, "Nota nueva en Conceptos de Bases de Datos") {
		t.Errorf("N3: en la 2 solo avisa BD; IPOO hace su base: %q", texts(sender.sent))
	}
	if !repository.baselines["1/grade/10327"] || !repository.seen["1/grade/9:7"] {
		t.Errorf("N3: IPOO debía hacer su base en la 2: bases=%v seen=%v", repository.baselines, repository.seen)
	}
}

// --- N4 / N5 / N6: foro y notas ---

func TestNovelties_ForumPostsOneMessageEach(t *testing.T) {
	_, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	for index := 1; index <= 10; index++ {
		source.posts[10325] = append(source.posts[10325], post(900+index, fmt.Sprintf("Aviso %d", index)))
	}
	notifier.NotifyAll()

	got := sender.delivered()
	if len(got) != 10 {
		t.Fatalf("N4: llegan los 10 avisos: %q", texts(got))
	}
	if !strings.HasPrefix(got[0].message, "📣 Aviso en Conceptos de Bases de Datos\nAviso 1\n— Enrique Corujo") || got[0].kind != "markdown" {
		t.Errorf("mensaje A: %q", got[0].message)
	}
}

func TestNovelties_Grades(t *testing.T) {
	_, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	source.grades[10325] = []domain.GradeItem{
		grade(144101, "Entrega TP1 - 25/08", score(2), satisfactorio),
		grade(144103, "Entrega TP3 - 18/09", nil, "-"),
		{ID: 144100, Type: "course", Raw: score(2), Formatted: "2,00"},
		grade(144120, "Autoevaluación", score(0), "0,00"),
	}
	notifier.NotifyAll()

	got := sender.delivered()
	if len(got) != 1 {
		t.Fatalf("N5: un mensaje por materia: %q", texts(got))
	}
	want := "📝 Nota nueva en Conceptos de Bases de Datos\nEntrega TP1 - 25/08: Satisfactorio\nAutoevaluación: 0,00\n\n🔗 [Ver notas](" + pedco + "/grade/report/user/index.php?id=10325)"
	if got[0].message != want {
		t.Errorf("N5:\n got %q\nwant %q", got[0].message, want)
	}

	// N6: mismo ítem con otro graderaw → avisa; mismo graderaw (con otra fecha de escritura) → nada.
	source.grades[10325][0] = grade(144101, "Entrega TP1 - 25/08", score(3), "Supera lo esperado")
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 2 || !strings.Contains(got[1].message, "Entrega TP1 - 25/08: Supera lo esperado") || strings.Contains(got[1].message, "Autoevaluación") {
		t.Errorf("N6: el cambio de valor avisa solo ese ítem: %q", texts(got))
	}
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 2 {
		t.Errorf("N6: sin cambio de valor no avisa: %q", texts(got))
	}
}

// --- N7 / N8: material (N7: sin descarga; el Source ya no tiene cómo pedir un archivo) ---

func TestNovelties_Material(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	source.materials[10325] = []domain.Material{
		link(1, "resource", "Presentación"),
		link(4, "url", "Video"),
		link(5, "folder", "Carpeta"),
	}
	notifier.NotifyAll()

	got := sender.delivered()
	if len(got) != 1 {
		t.Fatalf("N7: un mensaje por materia con todo su material nuevo: %q", texts(got))
	}
	want := "📎 Material nuevo en Conceptos de Bases de Datos\n" +
		"Presentación · 🔗 [Abrir](" + pedco + "/mod/resource/view.php?id=1)\n" +
		"Video · 🔗 [Abrir](" + pedco + "/mod/url/view.php?id=4)\n" +
		"Carpeta · 🔗 [Abrir](" + pedco + "/mod/folder/view.php?id=5)"
	if got[0].kind != "markdown" || got[0].message != want {
		t.Errorf("N7 mensaje C:\n got %q\nwant %q", got[0].message, want)
	}
	for _, key := range []string{"1/material/1", "1/material/4", "1/material/5"} {
		if !repository.seen[key] {
			t.Errorf("N7: si sale, se marcan todos: falta %s", key)
		}
	}
	notifier.NotifyAll()
	if len(sender.sent) != 1 {
		t.Errorf("N7: marcados, no se repiten: %q", texts(sender.sent))
	}
}

// N8: lo restringido llega filtrado (el adaptador saca uservisible:false): no queda visto y avisa al habilitarse.
func TestNovelties_RestrictedMaterialNotifiesWhenEnabled(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll() // base sin el recurso restringido
	notifier.NotifyAll() // sigue restringido
	if repository.seen["1/material/910471"] || len(sender.sent) != 0 {
		t.Fatalf("N8: restringido no queda visto ni avisa")
	}

	source.materials[10325] = []domain.Material{link(910471, "resource", "Recuperatorio")}
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 1 || !strings.Contains(got[0].message, "Recuperatorio") {
		t.Errorf("N8: al habilitarse avisa: %q", texts(sender.sent))
	}
}

// --- N9 / N10: fallos de Telegram y de la marca ---

func TestNovelties_TelegramFailures(t *testing.T) {
	logs := captureLog(t)
	repository, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	// Transitorio: no se marca, UN solo envío (sin reenvío plano) y la ronda siguiente lo manda.
	source.posts[10325] = []domain.ForumPost{post(1, "Transitorio")}
	sender.err = errTransient
	notifier.NotifyAll()
	if repository.seen["1/forum/1"] {
		t.Fatalf("N9: transitorio no marca")
	}
	if len(sender.sent) != 1 || sender.sent[0].kind != "markdown" {
		t.Fatalf("N9: transitorio = un solo envío, sin reenvío plano: %q", texts(sender.sent))
	}
	sender.sent = nil
	sender.err = nil
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 1 || !strings.Contains(got[0].message, "Transitorio") || !repository.seen["1/forum/1"] {
		t.Fatalf("N9: la ronda siguiente lo manda y marca: %q", texts(sender.sent))
	}

	// Permanente en Markdown → texto plano y se marca.
	sender.sent = nil
	source.posts[10325] = append(source.posts[10325], post(2, "Markdown_roto"))
	sender.fail = func(message sentMessage) error {
		if message.kind == "markdown" {
			return errPermanent
		}
		return nil
	}
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 1 || got[0].kind != "plain" || !strings.Contains(got[0].message, "Markdown_roto") || !repository.seen["1/forum/2"] {
		t.Errorf("N9: Markdown rechazado → plano y marca: %q", texts(sender.sent))
	}

	// Los dos permanentes → se marca y se loguea.
	sender.sent = nil
	logs.Reset()
	source.posts[10325] = append(source.posts[10325], post(3, "Imposible"))
	sender.fail = func(sentMessage) error { return errPermanent }
	notifier.NotifyAll()
	if len(sender.sent) != 2 || sender.sent[0].kind != "markdown" || sender.sent[1].kind != "plain" {
		t.Errorf("N9: un intento Markdown y uno plano: %q", texts(sender.sent))
	}
	if !repository.seen["1/forum/3"] {
		t.Errorf("N9: dos permanentes → se marca igual")
	}
	if !strings.Contains(logs.String(), "descartado") {
		t.Errorf("N9: dos permanentes → se loguea: %s", logs.String())
	}
	notifier.NotifyAll()
	if len(sender.sent) != 2 {
		t.Errorf("N9: marcado, no se reintenta: %q", texts(sender.sent))
	}
}

// N9: 401 corta las novedades del usuario en esa ronda: un solo envío, nada marcado, sin plano.
func TestNovelties_UnauthorizedCutsWithoutMarking(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	source.posts[10325] = []domain.ForumPost{post(1, "Uno"), post(2, "Dos")}
	source.grades[10325] = []domain.GradeItem{grade(144101, "Entrega TP1 - 25/08", score(2), satisfactorio)}
	source.materials[10327] = []domain.Material{link(3, "url", "IPOO")}
	sender.err = errUnauthorized
	notifier.NotifyAll()

	if len(sender.sent) != 1 || sender.sent[0].kind != "markdown" {
		t.Errorf("N9: 401 → un solo envío y corte, sin plano: %q", texts(sender.sent))
	}
	if len(repository.seen) != 0 {
		// la base de la ronda 1 no dejó ítems vistos: todo lo visto sería de esta ronda
		t.Errorf("N9: 401 no marca nada: %v", repository.seen)
	}

	sender.err = nil
	sender.sent = nil
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 4 {
		t.Errorf("N9: tras el 401, la ronda siguiente manda todo: %q", texts(got))
	}
}

func TestNovelties_MarkSeenFailsRepeatsNextRound(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	repository.users[2] = ports.UserCredentials{ChatID: 2, User: "beto", Pass: "p", Session: secretToken}
	repository.order = append(repository.order, 2)
	notifier.NotifyAll()

	source.posts[10325] = []domain.ForumPost{post(1, "Uno"), post(2, "Dos")}
	repository.markErr = errors.New("database is locked")
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 4 {
		t.Fatalf("N10: la marca fallida no corta la ronda (2 avisos × 2 usuarios): %q", texts(got))
	}

	repository.markErr = nil
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 8 {
		t.Errorf("N10: la ronda siguiente los repite: %q", texts(got))
	}
	notifier.NotifyAll()
	if got := sender.delivered(); len(got) != 8 {
		t.Errorf("N10: ya marcados, no se repiten más: %d", len(got))
	}
}

// --- N11 / N12: cursos que fallan, usuarios que no corren ---

func TestNovelties_FailedCourseDoesNotStopOthers(t *testing.T) {
	logs := captureLog(t)
	repository, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	source.materialErrs[10325] = errors.New("core_course_get_contents: HTTP 503")
	source.materials[10325] = []domain.Material{link(1, "url", "BD")}
	source.materials[10327] = []domain.Material{link(2, "url", "IPOO")}
	source.forumErrs[10325] = errors.New("mod_forum_get_forum_discussions: nopermissions")
	notifier.NotifyAll()

	if got := sender.delivered(); len(got) != 1 || !strings.Contains(got[0].message, "IPOO") {
		t.Errorf("N11: los demás cursos siguen: %q", texts(got))
	}
	if repository.seen["1/material/1"] {
		t.Errorf("N11: nada del curso fallido se marca")
	}
	for _, line := range []string{"material de Conceptos de Bases de Datos", "forum de Conceptos de Bases de Datos"} {
		if strings.Count(logs.String(), line) != 1 {
			t.Errorf("N11: una línea de log por curso y tipo (%q):\n%s", line, logs.String())
		}
	}
}

func TestNovelties_SkippedUsers(t *testing.T) {
	repository, source, _, notifier := newsScenario(t)
	repository.users[1] = ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: secretToken, CredsRejected: true}
	notifier.NotifyAll()
	if len(source.profileTokens) != 0 {
		t.Errorf("N12: pausado → sin novedades")
	}

	repository.users[1] = ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "roto"}
	source.fetchResults["roto"] = fetchResult{err: errors.New("mod_assign_get_assignments: HTTP 503")}
	notifier.NotifyAll()
	if len(source.profileTokens) != 0 {
		t.Errorf("N12: alertas en error → sin novedades")
	}

	// Re-login en alertas: novedades usa el token nuevo.
	repository.users[1] = ports.UserCredentials{ChatID: 1, User: "ana", Pass: "p", Session: "vencido"}
	source.fetchResults["vencido"] = fetchResult{err: ports.ErrSessionExpired}
	source.loginResults = map[string]loginResult{"ana": {token: secretToken}}
	notifier.NotifyAll()
	if len(source.profileTokens) != 1 || source.profileTokens[0] != secretToken {
		t.Errorf("novedades debe usar el token que dejó alertas: %q", source.profileTokens)
	}
}

func TestNovelties_SessionExpiredCutsWithoutRelogin(t *testing.T) {
	repository, source, sender, notifier := newsScenario(t)
	source.loginResults = map[string]loginResult{"ana": {token: "otro"}}
	notifier.NotifyAll()

	source.gradeErrs[10325] = fmt.Errorf("gradereport_user_get_grade_items: %w", ports.ErrSessionExpired)
	source.materials[10325] = []domain.Material{link(1, "url", "BD")}
	source.calls = nil
	notifier.NotifyAll()

	if source.called("grades:10327") || source.called("materials:10325") || len(sender.sent) != 0 {
		t.Errorf("N12: ErrSessionExpired corta las novedades del usuario: calls=%v sent=%q", source.calls, texts(sender.sent))
	}
	if len(source.loginCalls) != 0 || len(repository.cleared) != 0 {
		t.Errorf("N12: sin re-login ni limpieza del token: logins=%v cleared=%v", source.loginCalls, repository.cleared)
	}
}

// N14: ni el token ni la contraseña salen en logs ni mensajes de novedades.
func TestNovelties_SecretsNeverInLogsOrMessages(t *testing.T) {
	logs := captureLog(t)
	_, source, sender, notifier := newsScenario(t)
	notifier.NotifyAll()

	source.posts[10325] = []domain.ForumPost{post(1, "Aviso")}
	source.materials[10325] = []domain.Material{link(2, "resource", "Doc")}
	source.gradeErrs[10327] = errors.New("notingroup")
	sender.fail = func(message sentMessage) error {
		if message.kind == "markdown" {
			return errPermanent
		}
		return errTransient
	}
	notifier.NotifyAll()

	if logs.Len() == 0 || len(sender.sent) == 0 {
		t.Fatal("control positivo: sin logs o sin envíos")
	}
	outputs := append(texts(sender.sent), logs.String())
	for _, output := range outputs {
		if strings.Contains(output, secretToken) || strings.Contains(output, secretPassword) {
			t.Errorf("salida con un secreto:\n%s", output)
		}
	}
}
