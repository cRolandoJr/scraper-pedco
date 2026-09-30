package moodle

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

// fakeNovedades sirve server.php con las respuestas reales recortadas de testdata/.
type fakeNovedades struct {
	t              *testing.T
	baseURL        string
	mutex          sync.Mutex
	discussionForm []map[string][]string // forms de cada mod_forum_get_forum_discussions
	contentsFile   map[string]string     // courseid → fixture de core_course_get_contents
	discussions    map[string]string     // forumid → JSON (si falta, se usa el fixture)
	forumsJSON     string                // si no es vacío, reemplaza a forums_10325.json
	tenDiscussions bool                  // el foro 60925 trae 10 avisos, paginados si llega perpage
}

func (fake *fakeNovedades) fixture(name string) string {
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		fake.t.Fatalf("fixture %s: %v", name, err)
	}
	return strings.ReplaceAll(string(data), DefaultBaseURL, fake.baseURL)
}

func (fake *fakeNovedades) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.URL.Path != "/webservice/rest/server.php" {
		fake.t.Errorf("request inesperado: %s %s", request.Method, request.URL.String())
		http.Error(writer, "bad", http.StatusBadRequest)
		return
	}
	if err := request.ParseForm(); err != nil {
		fake.t.Errorf("form: %v", err)
	}
	form := request.PostForm
	if form.Get("wstoken") != validToken {
		fmt.Fprint(writer, `{"exception":"moodle_exception","errorcode":"invalidtoken","message":"Invalid token - token not found"}`)
		return
	}
	switch form.Get("wsfunction") {
	case "core_webservice_get_site_info":
		fmt.Fprint(writer, `{"sitename":"Plataforma","userid":7}`)
	case "core_enrol_get_users_courses":
		if form.Get("userid") != "7" {
			fake.t.Errorf("userid = %q", form.Get("userid"))
		}
		fmt.Fprint(writer, fake.fixture("courses.json"))
	case "mod_forum_get_forums_by_courses":
		if form.Get("courseids[0]") == "" {
			fake.t.Errorf("forums_by_courses sin courseids")
		}
		if fake.forumsJSON != "" {
			fmt.Fprint(writer, fake.forumsJSON)
			return
		}
		fmt.Fprint(writer, fake.fixture("forums_10325.json"))
	case "mod_forum_get_forum_discussions":
		fake.mutex.Lock()
		fake.discussionForm = append(fake.discussionForm, form)
		fake.mutex.Unlock()
		forumID := form.Get("forumid")
		if body, found := fake.discussions[forumID]; found {
			fmt.Fprint(writer, body)
			return
		}
		if forumID != "60925" {
			fake.t.Errorf("se pidieron discusiones del foro %s, que no es news", forumID)
			fmt.Fprint(writer, `{"discussions":[],"warnings":[]}`)
			return
		}
		if fake.tenDiscussions {
			fmt.Fprint(writer, fake.paginatedTen(form.Get("perpage"), form.Get("page")))
			return
		}
		fmt.Fprint(writer, fake.fixture("discussions_60925.json"))
	case "gradereport_user_get_grade_items":
		if form.Get("userid") != "7" {
			fake.t.Errorf("grades userid = %q", form.Get("userid"))
		}
		switch form.Get("courseid") {
		case "10325":
			fmt.Fprint(writer, fake.fixture("grades_10325.json"))
		default:
			fmt.Fprint(writer, fake.fixture("grades_notingroup.json"))
		}
	case "core_course_get_contents":
		name, found := fake.contentsFile[form.Get("courseid")]
		if !found {
			fake.t.Errorf("contents de un curso inesperado: %s", form.Get("courseid"))
			fmt.Fprint(writer, `[]`)
			return
		}
		fmt.Fprint(writer, fake.fixture(name))
	default:
		fake.t.Errorf("wsfunction inesperada: %s", form.Get("wsfunction"))
		fmt.Fprint(writer, `{}`)
	}
}

// paginatedTen clona el primer aviso real 10 veces y pagina como Moodle si llega perpage.
func (fake *fakeNovedades) paginatedTen(perpageValue, pageValue string) string {
	var response struct {
		Discussions []map[string]any `json:"discussions"`
	}
	if err := json.Unmarshal([]byte(fake.fixture("discussions_60925.json")), &response); err != nil {
		fake.t.Fatal(err)
	}
	var all []map[string]any
	for index := 1; index <= 10; index++ {
		clone := map[string]any{}
		for key, value := range response.Discussions[0] {
			clone[key] = value
		}
		clone["discussion"] = 900 + index
		all = append(all, clone)
	}
	perpage, _ := strconv.Atoi(perpageValue)
	page, _ := strconv.Atoi(pageValue)
	if perpage > 0 {
		start := page * perpage
		if start < 0 || start > len(all) {
			start = len(all)
		}
		end := start + perpage
		if end > len(all) {
			end = len(all)
		}
		all = all[start:end]
	}
	data, _ := json.Marshal(map[string]any{"discussions": all, "warnings": []any{}})
	return string(data)
}

func newFakeNovedades(t *testing.T) (*fakeNovedades, *Client) {
	t.Helper()
	fake := &fakeNovedades{t: t, contentsFile: map[string]string{}, discussions: map[string]string{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.baseURL = server.URL
	return fake, NewClient(server.URL, argentina(t))
}

var bdCourse = domain.Course{ID: 10325, Name: "Conceptos de Bases de Datos"}
var ipooCourse = domain.Course{ID: 10327, Name: "Introduc. a la Programación Orientada a Objetos"}

func TestProfile(t *testing.T) {
	fake, client := newFakeNovedades(t)

	userID, courses, err := client.Profile(validToken)
	if err != nil {
		t.Fatal(err)
	}
	if userID != 7 {
		t.Errorf("userID = %d", userID)
	}
	want := []domain.Course{
		{ID: 10327, Name: "Introduc. a la Programación Orientada a Objetos", GradesLink: fake.baseURL + "/grade/report/user/index.php?id=10327"},
		{ID: 10325, Name: "Conceptos de Bases de Datos", GradesLink: fake.baseURL + "/grade/report/user/index.php?id=10325"},
	}
	if fmt.Sprint(courses) != fmt.Sprint(want) {
		t.Errorf("cursos:\n got %+v\nwant %+v", courses, want)
	}

	if _, _, err := client.Profile("vencido"); !errors.Is(err, ports.ErrSessionExpired) {
		t.Errorf("token vencido: %v, quiero ErrSessionExpired", err)
	}
}

// A / N4: solo foros news, sin perpage; clave discussion; link al aviso.
func TestForums_OnlyNewsAllDiscussions(t *testing.T) {
	fake, client := newFakeNovedades(t)

	posts, errs := client.Forums(validToken, []domain.Course{bdCourse, ipooCourse})
	if len(errs) != 0 {
		t.Fatalf("errores: %v", errs)
	}
	bd := posts[10325]
	if len(bd) != 2 {
		t.Fatalf("avisos de BD = %d, quiero los 2 del fixture: %+v", len(bd), bd)
	}
	first := bd[0]
	if first.Discussion != 563356 || first.Subject != "Acceso al 1er Parcial" || first.Author != "Enrique Corujo" ||
		first.Link != fake.baseURL+"/mod/forum/discuss.php?d=563356" || !strings.HasPrefix(first.MessageHTML, "<p dir=\"ltr\"") {
		t.Errorf("aviso: %+v", first)
	}
	if !first.Published.Equal(time.Unix(1790635435, 0)) {
		t.Errorf("Published = %v, quiero el created del fixture (1790635435)", first.Published)
	}
	if len(posts[10327]) != 0 {
		t.Errorf("IPOO no tiene foros en el fixture: %+v", posts[10327])
	}
	if len(fake.discussionForm) != 1 {
		t.Fatalf("pedidos de discusiones = %d, quiero 1 (solo el foro news)", len(fake.discussionForm))
	}
	for _, key := range []string{"perpage", "page"} {
		if _, sent := fake.discussionForm[0][key]; sent {
			t.Errorf("el adaptador mandó %s: pagina y pierde avisos", key)
		}
	}
}

// N4: un foro con 10 avisos → llegan los 10 (el fake pagina si recibe perpage).
func TestForums_TenPostsArrive(t *testing.T) {
	fake, client := newFakeNovedades(t)
	fake.tenDiscussions = true

	posts, errs := client.Forums(validToken, []domain.Course{bdCourse})
	if len(errs) != 0 {
		t.Fatalf("errores: %v", errs)
	}
	if len(posts[10325]) != 10 {
		t.Errorf("avisos = %d, quiero 10", len(posts[10325]))
	}
}

// N11 en el adaptador: si falla un foro, su curso va a errores y no trae avisos; el resto sigue.
func TestForums_PerCourseErrors(t *testing.T) {
	fake, client := newFakeNovedades(t)
	fake.forumsJSON = `[{"id":60925,"course":10325,"type":"news","name":"Avisos"},{"id":60927,"course":10327,"type":"news","name":"Avisos"}]`
	fake.discussions["60927"] = `{"exception":"moodle_exception","errorcode":"nopermissions","message":"x"}`

	posts, errs := client.Forums(validToken, []domain.Course{bdCourse, ipooCourse})
	if errs[10327] == nil || !strings.Contains(errs[10327].Error(), "nopermissions") {
		t.Errorf("error de IPOO = %v", errs[10327])
	}
	if _, present := posts[10327]; present {
		t.Errorf("un curso con error no debe traer avisos: %+v", posts[10327])
	}
	if errs[10325] != nil || len(posts[10325]) != 2 {
		t.Errorf("BD debía seguir: err=%v avisos=%d", errs[10325], len(posts[10325]))
	}

	fake.forumsJSON = `{"exception":"webservice_access_exception","errorcode":"accessexception","message":"x"}`
	posts, errs = client.Forums(validToken, []domain.Course{bdCourse, ipooCourse})
	if len(posts) != 0 || !errors.Is(errs[10325], ports.ErrSessionExpired) || !errors.Is(errs[10327], ports.ErrSessionExpired) {
		t.Errorf("si falla forums_by_courses, todos los cursos llevan el error: posts=%v errs=%v", posts, errs)
	}
}

// B: forma real del reporte; graderaw nulo → nil; notingroup → error.
func TestGrades(t *testing.T) {
	_, client := newFakeNovedades(t)

	items, err := client.Grades(validToken, 10325, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("ítems = %d: %+v", len(items), items)
	}
	first := items[0]
	if first.ID != 144101 || first.Name != "Entrega TP1 - 25/08" || first.Type != "mod" || first.Raw == nil || *first.Raw != 2 ||
		!strings.HasSuffix(first.Formatted, "</i>Satisfactorio") {
		t.Errorf("TP1: %+v", first)
	}
	if items[1].Raw != nil {
		t.Errorf("graderaw null debe dar nil: %v", *items[1].Raw)
	}
	if items[3].Type != "course" {
		t.Errorf("ítem course: %+v", items[3])
	}

	if _, err := client.Grades(validToken, 10327, 7); err == nil || !strings.Contains(err.Error(), "notingroup") {
		t.Errorf("IPOO: %v, quiero el notingroup", err)
	}
}

// C / N7 / N8: solo resource, url y folder con uservisible, y ninguna descarga: las
// fileurl de los fixtures apuntan al fake, que falla ante todo lo que no sea server.php.
func TestMaterials(t *testing.T) {
	fake, client := newFakeNovedades(t)
	fake.contentsFile["10325"] = "contents_10325.json"
	fake.contentsFile["1"] = "contents_folder.json"
	fake.contentsFile["3"] = "contents_restricted.json"

	materials, err := client.Materials(validToken, 10325)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Material{
		{CMID: 910436, ModName: "resource", Name: "Presentación de la Materia", Link: fake.baseURL + "/mod/resource/view.php?id=910436"},
		{CMID: 910438, ModName: "url", Name: "Instructivo de uso de programa DIA", Link: fake.baseURL + "/mod/url/view.php?id=910438"},
	}
	if describe(materials) != describe(want) {
		t.Errorf("materiales:\n got %s\nwant %s", describe(materials), describe(want))
	}

	folder, err := client.Materials(validToken, 1)
	if err != nil || len(folder) != 1 || folder[0].ModName != "folder" {
		t.Errorf("folder: %s, %v", describe(folder), err)
	}
	restricted, err := client.Materials(validToken, 3)
	if err != nil || len(restricted) != 0 {
		t.Errorf("resource con uservisible:false debe quedar afuera: %s, %v", describe(restricted), err)
	}
}

func describe(materials []domain.Material) string {
	var parts []string
	for _, material := range materials {
		parts = append(parts, fmt.Sprintf("{%d %s %q %s}", material.CMID, material.ModName, material.Name, material.Link))
	}
	return strings.Join(parts, " ")
}
