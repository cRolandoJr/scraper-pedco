package service

import (
	"os"
	"strings"
	"testing"
	"time"

	"scraper-pedco/internal/core/domain"
)

// utc arma un instante en UTC; la hora AR es 3 h menos.
func utc(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
}

// C11 + C14 (+ C3 hora AR, C4 quiz sin cierre, C5 una entrada por quiz).
func TestFormatItems_Golden(t *testing.T) {
	const base = "https://pedco.uncoma.edu.ar"
	items := []domain.Item{
		{Kind: domain.Assignment, Title: "Entrega Trabajo Práctico Nº 3", Course: "Administración de Servicios",
			Due: utc(10, 14, 2, 55), Status: domain.Pending, Link: base + "/mod/assign/view.php?id=1002"},
		{Kind: domain.Assignment, Title: "TRABAJO PRACTICO N° 2", Course: "Introduc. a la Programación Orientada a Objetos",
			Due: utc(10, 1, 2, 55), Status: domain.Done, Link: base + "/mod/assign/view.php?id=1007"},
		{Kind: domain.Quiz, Title: "Primer parcial", Course: "Programación Estática y Laboratorio Web",
			OpensAt: utc(10, 5, 13, 0), Due: utc(10, 6, 1, 0), Status: domain.NotOpen, Link: base + "/mod/quiz/view.php?id=911187"},
		{Kind: domain.Assignment, Title: "Entrega Practico 3", Course: "Seguridad Informática",
			OpensAt: utc(10, 2, 0, 35), Due: utc(10, 13, 2, 55), Status: domain.NotOpen, Link: base + "/mod/assign/view.php?id=1005"},
		{Kind: domain.Quiz, Title: "Coloquio de cierre", Course: "Conceptos de Bases de Datos",
			OpensAt: utc(10, 7, 21, 38), Due: utc(10, 7, 21, 38), Status: domain.Unknown, Link: base + "/mod/quiz/view.php?id=2006"},
		{Kind: domain.Quiz, Title: "Parcial 1 - 29/09 18:00 hs", Course: "Conceptos de Bases de Datos",
			OpensAt: utc(9, 29, 21, 0), Due: utc(9, 29, 23, 0), Status: domain.Done, Link: base + "/mod/quiz/view.php?id=2008"},
		{Kind: domain.Assignment, Title: "TP_final *borrador*", Course: "[AyS] Redes y `SO`",
			Due: utc(9, 30, 2, 55), Status: domain.Draft, Link: base + "/mod/assign/view.php?id=1003"},
		{Kind: domain.Quiz, Title: "Cuestionario 2", Course: "Conceptos de Bases de Datos",
			OpensAt: utc(9, 28, 11, 0), Due: utc(10, 1, 2, 59), Status: domain.InProgress, Link: base + "/mod/quiz/view.php?id=2004"},
		{Kind: domain.Quiz, Title: "Autoevaluación", Course: "Materia X",
			Due: utc(10, 3, 15, 0), Status: domain.Pending, Link: base + "/mod/quiz/view.php?id=2009"},
	}
	notifier := newTestNotifier(t, newFakeRepository(), &fakeSource{}, &fakeSender{})

	got := notifier.formatItems(items, true)

	golden, err := os.ReadFile("testdata/alert.golden")
	if err != nil {
		t.Fatalf("golden: %v", err)
	}
	want := strings.TrimSuffix(string(golden), "\n")
	if got != want {
		t.Errorf("mensaje distinto del golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
