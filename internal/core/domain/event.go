package domain

// Event representa una tarea, foro o parcial de la facultad
type Event struct {
	ID      string
	Title   string
	Course  string
	Type    string
	DueDate string
	Link    string
}
