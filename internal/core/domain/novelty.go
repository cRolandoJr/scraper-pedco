package domain

// Course es una materia del alumno, con el nombre sin el prefijo de año.
type Course struct {
	ID         int
	Name       string
	GradesLink string // /grade/report/user/index.php?id=<id>
}

// ForumPost es un aviso de un foro de tipo news.
type ForumPost struct {
	Discussion  int
	Subject     string
	Author      string
	MessageHTML string
	Link        string // /mod/forum/discuss.php?d=<discussion>
}

// GradeItem es una fila del reporte de notas del alumno.
type GradeItem struct {
	ID        int
	Name      string
	Type      string   // itemtype: mod, manual, course...
	Raw       *float64 // graderaw: nil = sin nota u oculta; 0 es una nota
	Formatted string   // gradeformatted, puede traer HTML
}

// Material es un módulo resource, url o folder visible para el alumno.
type Material struct {
	CMID    int
	ModName string
	Name    string
	Link    string // /mod/<modname>/view.php?id=<cmid>
}
