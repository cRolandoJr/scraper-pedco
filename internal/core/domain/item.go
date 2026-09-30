package domain

import "time"

type Kind int

const (
	Assignment Kind = iota
	Quiz
)

type Status int

const (
	Pending Status = iota
	Draft
	NotOpen
	InProgress
	Done
	Unknown
)

// Item es una tarea o un cuestionario con su estado para el alumno.
type Item struct {
	Kind    Kind
	Title   string    // nombre de la actividad
	Course  string    // fullname sin el prefijo de año
	Due     time.Time // tarea: duedate; quiz: timeclose, o timeopen si timeclose == 0
	OpensAt time.Time // tarea: allowsubmissionsfromdate; quiz: timeopen (cero si no hay)
	Status  Status
	Link    string
}
