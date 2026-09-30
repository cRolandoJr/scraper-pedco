package storage

import (
	"path/filepath"
	"testing"

	"scraper-pedco/internal/core/ports"
)

var _ ports.SeenRepository = (*Repository)(nil)

// Persistencia de seen: MarkSeen idempotente (INSERT OR IGNORE), claves por (chat, tipo).
func TestSeenMarksAreIdempotentAndScoped(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if seen, err := IsSeen(1, "forum", "563356"); err != nil || seen {
		t.Fatalf("antes de marcar: seen=%v err=%v", seen, err)
	}
	if err := MarkSeen(1, "forum", "563356"); err != nil {
		t.Fatal(err)
	}
	if err := MarkSeen(1, "forum", "563356"); err != nil {
		t.Fatalf("la segunda marca debe ser INSERT OR IGNORE: %v", err)
	}
	if seen, err := IsSeen(1, "forum", "563356"); err != nil || !seen {
		t.Errorf("tras marcar: seen=%v err=%v", seen, err)
	}
	if seen, _ := IsSeen(1, "grade", "563356"); seen {
		t.Errorf("la marca de forum no debe valer para grade")
	}
	if seen, _ := IsSeen(2, "forum", "563356"); seen {
		t.Errorf("la marca del chat 1 no debe valer para el chat 2")
	}
}

func TestBaselineIsPerCourse(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if has, err := HasBaseline(1, "material", 10325); err != nil || has {
		t.Fatalf("antes: has=%v err=%v", has, err)
	}
	if err := SetBaseline(1, "material", 10325); err != nil {
		t.Fatal(err)
	}
	if err := SetBaseline(1, "material", 10325); err != nil {
		t.Fatalf("SetBaseline repetido: %v", err)
	}
	if has, err := HasBaseline(1, "material", 10325); err != nil || !has {
		t.Errorf("tras SetBaseline: has=%v err=%v", has, err)
	}
	if has, _ := HasBaseline(1, "material", 10327); has {
		t.Errorf("la base de 10325 no debe valer para 10327")
	}
	if has, _ := HasBaseline(1, "grade", 10325); has {
		t.Errorf("la base de material no debe valer para grade")
	}
}

// N12 (/borrar): DeleteUser borra las filas de seen y seen_baseline de ese chat, y no las de otro.
// SaveUser (/login) no las borra.
func TestDeleteUserClearsSeenButSaveUserKeepsIt(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))
	for _, chatID := range []int64{1, 2} {
		if err := SaveUser(chatID, "ana", "clave"); err != nil {
			t.Fatal(err)
		}
		if err := MarkSeen(chatID, "forum", "k"); err != nil {
			t.Fatal(err)
		}
		if err := SetBaseline(chatID, "forum", 10); err != nil {
			t.Fatal(err)
		}
	}

	if err := SaveUser(1, "ana", "otra"); err != nil {
		t.Fatal(err)
	}
	if seen, _ := IsSeen(1, "forum", "k"); !seen {
		t.Fatalf("SaveUser no debe borrar lo visto")
	}
	if has, _ := HasBaseline(1, "forum", 10); !has {
		t.Fatalf("SaveUser no debe borrar la base")
	}

	if err := DeleteUser(1); err != nil {
		t.Fatal(err)
	}
	if seen, _ := IsSeen(1, "forum", "k"); seen {
		t.Errorf("tras /borrar la fila de seen sigue")
	}
	if has, _ := HasBaseline(1, "forum", 10); has {
		t.Errorf("tras /borrar la fila de seen_baseline sigue")
	}
	if seen, _ := IsSeen(2, "forum", "k"); !seen {
		t.Errorf("/borrar del chat 1 borró lo visto del chat 2")
	}
	if has, _ := HasBaseline(2, "forum", 10); !has {
		t.Errorf("/borrar del chat 1 borró la base del chat 2")
	}
}
