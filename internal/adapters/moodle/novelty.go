package moodle

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"scraper-pedco/internal/core/domain"
)

// Profile devuelve el userid y las materias del alumno (los mismos cursos que la ronda de alertas).
func (client *Client) Profile(token string) (int, []domain.Course, error) {
	var siteInfo struct {
		UserID int `json:"userid"`
	}
	if err := client.call(token, "core_webservice_get_site_info", url.Values{}, &siteInfo); err != nil {
		return 0, nil, err
	}
	var enrolled []struct {
		ID       int    `json:"id"`
		FullName string `json:"fullname"`
	}
	if err := client.call(token, "core_enrol_get_users_courses", url.Values{"userid": {strconv.Itoa(siteInfo.UserID)}}, &enrolled); err != nil {
		return 0, nil, err
	}
	courses := make([]domain.Course, 0, len(enrolled))
	for _, course := range enrolled {
		courses = append(courses, domain.Course{
			ID:         course.ID,
			Name:       yearPrefix.ReplaceAllString(course.FullName, ""),
			GradesLink: fmt.Sprintf("%s/grade/report/user/index.php?id=%d", client.baseURL, course.ID),
		})
	}
	return siteInfo.UserID, courses, nil
}

// Forums trae TODAS las discusiones (sin perpage: con perpage Moodle pagina y se pierden avisos)
// de los foros news. Un curso con algún foro fallido queda en errores y sin avisos.
func (client *Client) Forums(token string, courses []domain.Course) (map[int][]domain.ForumPost, map[int]error) {
	posts := map[int][]domain.ForumPost{}
	errs := map[int]error{}
	if len(courses) == 0 {
		return posts, errs
	}
	courseIDs := url.Values{}
	for index, course := range courses {
		courseIDs.Set(fmt.Sprintf("courseids[%d]", index), strconv.Itoa(course.ID))
	}
	var forums []struct {
		ID     int    `json:"id"`
		Course int    `json:"course"`
		Type   string `json:"type"`
	}
	if err := client.call(token, "mod_forum_get_forums_by_courses", courseIDs, &forums); err != nil {
		for _, course := range courses {
			errs[course.ID] = err
		}
		return map[int][]domain.ForumPost{}, errs
	}
	for _, forum := range forums {
		if forum.Type != "news" || errs[forum.Course] != nil {
			continue
		}
		var response struct {
			Discussions []struct {
				Discussion   int    `json:"discussion"`
				Subject      string `json:"subject"`
				Message      string `json:"message"`
				UserFullName string `json:"userfullname"`
				Created      int64  `json:"created"`
			} `json:"discussions"`
		}
		if err := client.call(token, "mod_forum_get_forum_discussions", url.Values{"forumid": {strconv.Itoa(forum.ID)}}, &response); err != nil {
			errs[forum.Course] = err
			delete(posts, forum.Course)
			continue
		}
		for _, discussion := range response.Discussions {
			posts[forum.Course] = append(posts[forum.Course], domain.ForumPost{
				Discussion:  discussion.Discussion,
				Subject:     discussion.Subject,
				Author:      discussion.UserFullName,
				MessageHTML: discussion.Message,
				Link:        fmt.Sprintf("%s/mod/forum/discuss.php?d=%d", client.baseURL, discussion.Discussion),
				Published:   time.Unix(discussion.Created, 0),
			})
		}
	}
	return posts, errs
}

func (client *Client) Grades(token string, courseID, userID int) ([]domain.GradeItem, error) {
	var response struct {
		UserGrades []struct {
			GradeItems []struct {
				ID             int      `json:"id"`
				ItemName       string   `json:"itemname"`
				ItemType       string   `json:"itemtype"`
				GradeRaw       *float64 `json:"graderaw"`
				GradeFormatted string   `json:"gradeformatted"`
			} `json:"gradeitems"`
		} `json:"usergrades"`
	}
	params := url.Values{"courseid": {strconv.Itoa(courseID)}, "userid": {strconv.Itoa(userID)}}
	if err := client.call(token, "gradereport_user_get_grade_items", params, &response); err != nil {
		return nil, err
	}
	var items []domain.GradeItem
	for _, userGrades := range response.UserGrades {
		for _, item := range userGrades.GradeItems {
			items = append(items, domain.GradeItem{
				ID: item.ID, Name: item.ItemName, Type: item.ItemType, Raw: item.GradeRaw, Formatted: item.GradeFormatted,
			})
		}
	}
	return items, nil
}

var materialModules = map[string]bool{"resource": true, "url": true, "folder": true}

// Materials filtra uservisible: un módulo restringido no debe entrar a lo visto,
// así avisa el día que se habilite.
func (client *Client) Materials(token string, courseID int) ([]domain.Material, error) {
	var sections []struct {
		Modules []struct {
			ID          int    `json:"id"`
			ModName     string `json:"modname"`
			Name        string `json:"name"`
			UserVisible bool   `json:"uservisible"`
		} `json:"modules"`
	}
	if err := client.call(token, "core_course_get_contents", url.Values{"courseid": {strconv.Itoa(courseID)}}, &sections); err != nil {
		return nil, err
	}
	var materials []domain.Material
	for _, section := range sections {
		for _, module := range section.Modules {
			if !materialModules[module.ModName] || !module.UserVisible {
				continue
			}
			materials = append(materials, domain.Material{
				CMID:    module.ID,
				ModName: module.ModName,
				Name:    module.Name,
				Link:    fmt.Sprintf("%s/mod/%s/view.php?id=%d", client.baseURL, module.ModName, module.ID),
			})
		}
	}
	return materials, nil
}
