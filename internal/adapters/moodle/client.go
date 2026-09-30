// Package moodle implementa ports.Source sobre la API REST de Moodle
// (webservice/rest/server.php) y login/token.php. Todo va por POST: el token
// y la contraseña viajan en el body, nunca en la URL, así que los errores de
// net/http (que citan la URL) no los filtran.
package moodle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

const (
	DefaultBaseURL = "https://pedco.uncoma.edu.ar"
	requestTimeout = 15 * time.Second
	windowDays     = 14
)

var yearPrefix = regexp.MustCompile(`^\d{4}\s*-\s*`)

type Client struct {
	baseURL    string
	httpClient *http.Client
	location   *time.Location
}

func NewClient(baseURL string, location *time.Location) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: requestTimeout},
		location:   location,
	}
}

func (client *Client) Login(username, password string) (string, error) {
	body, err := client.post("/login/token.php", url.Values{
		"username": {username},
		"password": {password},
		"service":  {"moodle_mobile_app"},
	})
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	var response struct {
		Token     string `json:"token"`
		Error     string `json:"error"`
		ErrorCode string `json:"errorcode"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("login: respuesta ilegible: %w", err)
	}
	switch {
	case response.Token != "":
		return response.Token, nil
	case response.ErrorCode == "invalidlogin":
		return "", fmt.Errorf("login: %w", ports.ErrBadCredentials)
	case response.Error != "" || response.ErrorCode != "":
		return "", fmt.Errorf("login: %s (%s)", response.Error, response.ErrorCode)
	default:
		return "", errors.New("login: respuesta sin token")
	}
}

func (client *Client) FetchItems(token string, now time.Time) ([]domain.Item, error) {
	_, courses, err := client.Profile(token)
	if err != nil {
		return nil, err
	}
	courseNames := map[int64]string{}
	courseIDs := url.Values{}
	for index, course := range courses {
		courseNames[int64(course.ID)] = course.Name
		courseIDs.Set(fmt.Sprintf("courseids[%d]", index), strconv.Itoa(course.ID))
	}

	var assignmentsResponse struct {
		Courses []struct {
			ID          int64 `json:"id"`
			Assignments []struct {
				ID                       int64  `json:"id"`
				CMID                     int64  `json:"cmid"`
				Name                     string `json:"name"`
				DueDate                  int64  `json:"duedate"`
				AllowSubmissionsFromDate int64  `json:"allowsubmissionsfromdate"`
			} `json:"assignments"`
		} `json:"courses"`
	}
	if err := client.call(token, "mod_assign_get_assignments", courseIDs, &assignmentsResponse); err != nil {
		return nil, err
	}
	var quizzesResponse struct {
		Quizzes []struct {
			ID           int64  `json:"id"`
			CourseModule int64  `json:"coursemodule"`
			Course       int64  `json:"course"`
			Name         string `json:"name"`
			TimeOpen     int64  `json:"timeopen"`
			TimeClose    int64  `json:"timeclose"`
		} `json:"quizzes"`
	}
	if err := client.call(token, "mod_quiz_get_quizzes_by_courses", courseIDs, &quizzesResponse); err != nil {
		return nil, err
	}

	windowEnd := client.windowEnd(now)
	inWindow := func(due time.Time) bool { return !due.Before(now) && due.Before(windowEnd) }

	var items []domain.Item
	for _, course := range assignmentsResponse.Courses {
		for _, assignment := range course.Assignments {
			item := domain.Item{
				Kind:    domain.Assignment,
				Title:   assignment.Name,
				Course:  courseNames[course.ID],
				Due:     epoch(assignment.DueDate),
				OpensAt: epoch(assignment.AllowSubmissionsFromDate),
				Link:    fmt.Sprintf("%s/mod/assign/view.php?id=%d", client.baseURL, assignment.CMID),
			}
			if !inWindow(item.Due) {
				continue
			}
			item.Status = client.assignmentStatus(token, assignment.ID, item.OpensAt, now)
			items = append(items, item)
		}
	}
	for _, quiz := range quizzesResponse.Quizzes {
		item := domain.Item{
			Kind:    domain.Quiz,
			Title:   quiz.Name,
			Course:  courseNames[quiz.Course],
			Due:     epoch(quiz.TimeClose),
			OpensAt: epoch(quiz.TimeOpen),
			Link:    fmt.Sprintf("%s/mod/quiz/view.php?id=%d", client.baseURL, quiz.CourseModule),
		}
		if quiz.TimeClose == 0 {
			item.Due = item.OpensAt
		}
		if !inWindow(item.Due) {
			continue
		}
		item.Status = client.quizStatus(token, quiz.ID, item.OpensAt, now)
		items = append(items, item)
	}
	return items, nil
}

// windowEnd es el primer instante (hora AR) posterior al fin del día de now + 14 días.
func (client *Client) windowEnd(now time.Time) time.Time {
	local := now.In(client.location)
	return time.Date(local.Year(), local.Month(), local.Day()+windowDays+1, 0, 0, 0, 0, client.location)
}

func (client *Client) assignmentStatus(token string, assignID int64, opensAt, now time.Time) domain.Status {
	type submission struct {
		Status string `json:"status"`
	}
	var response struct {
		LastAttempt struct {
			Submission     *submission `json:"submission"`
			TeamSubmission *submission `json:"teamsubmission"`
		} `json:"lastattempt"`
	}
	if err := client.call(token, "mod_assign_get_submission_status", url.Values{"assignid": {strconv.FormatInt(assignID, 10)}}, &response); err != nil {
		log.Printf("⚠️ Estado de la tarea %d ilegible: %v", assignID, err)
		return domain.Unknown
	}
	status := ""
	if response.LastAttempt.TeamSubmission != nil {
		status = response.LastAttempt.TeamSubmission.Status
	} else if response.LastAttempt.Submission != nil {
		status = response.LastAttempt.Submission.Status
	}
	switch {
	case status == "submitted":
		return domain.Done
	case opensAt.After(now):
		return domain.NotOpen
	case status == "draft":
		return domain.Draft
	default:
		return domain.Pending
	}
}

func (client *Client) quizStatus(token string, quizID int64, opensAt, now time.Time) domain.Status {
	var response struct {
		Attempts []struct {
			State string `json:"state"`
		} `json:"attempts"`
	}
	params := url.Values{"quizid": {strconv.FormatInt(quizID, 10)}, "status": {"all"}}
	if err := client.call(token, "mod_quiz_get_user_attempts", params, &response); err != nil {
		log.Printf("⚠️ Estado del cuestionario %d ilegible: %v", quizID, err)
		return domain.Unknown
	}
	states := map[string]bool{}
	for _, attempt := range response.Attempts {
		states[attempt.State] = true
	}
	switch {
	case states["finished"]:
		return domain.Done
	case opensAt.After(now):
		return domain.NotOpen
	case states["inprogress"] || states["overdue"]:
		return domain.InProgress
	case states["abandoned"]:
		return domain.Unknown
	default:
		return domain.Pending
	}
}

// call invoca una función de server.php y decodifica la respuesta en out.
func (client *Client) call(token, function string, params url.Values, out any) error {
	form := url.Values{}
	for key, values := range params {
		form[key] = values
	}
	form.Set("wstoken", token)
	form.Set("wsfunction", function)
	form.Set("moodlewsrestformat", "json")

	body, err := client.post("/webservice/rest/server.php", form)
	if err != nil {
		return fmt.Errorf("%s: %w", function, err)
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '{' {
		var exception struct {
			Exception string `json:"exception"`
			ErrorCode string `json:"errorcode"`
			Message   string `json:"message"`
		}
		if json.Unmarshal(trimmed, &exception) == nil && exception.Exception != "" {
			if exception.ErrorCode == "invalidtoken" || exception.ErrorCode == "accessexception" {
				return fmt.Errorf("%s: %w", function, ports.ErrSessionExpired)
			}
			return fmt.Errorf("%s: %s (%s)", function, exception.Message, exception.ErrorCode)
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: respuesta ilegible: %w", function, err)
	}
	return nil
}

func (client *Client) post(path string, form url.Values) ([]byte, error) {
	response, err := client.httpClient.PostForm(client.baseURL+path, form)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return body, nil
}

func epoch(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
