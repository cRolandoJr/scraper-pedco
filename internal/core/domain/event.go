package domain

// Event representa una tarea, foro o parcial de la facultad
type Event struct {
	ID      string
	Title   string
	Course  string
	Type    string
	DueDate string // Aquí guardaremos "lunes, 18 mayo, 23:55"
	Link    string // Aquí guardaremos "https://pedco..."
}
