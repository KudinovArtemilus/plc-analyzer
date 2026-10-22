package archive

import (
	"path/filepath"
	"testing"
)

func TestExtract(t *testing.T) {
	f, err := Load(writeTestFile(t)) // файл из format_test.go: 2 группы, 3 сигнала
	if err != nil {
		t.Fatal(err)
	}

	// Берём temp (группа 0) и valve (группа 1) за 1000..1100 мс
	h, chunks := Extract(f, []string{"temp", "valve"}, 1000, 1100)

	if len(h.Groups) != 2 || h.Groups[0].Signals[0].Name != "temp" || h.Groups[1].Signals[0].Name != "valve" {
		t.Fatalf("заголовок слепка неверный: %+v", h.Groups)
	}
	if len(chunks) != 2 || len(chunks[0].Times) != 2 || chunks[0].Values[0][1] != 85.25 {
		t.Fatalf("данные слепка неверные: %+v", chunks)
	}
	if len(f.Header.Groups[0].Signals) != 2 {
		t.Fatal("исходный заголовок испорчен — слепок не должен его менять")
	}

	// Слепок записывается и читается обратно
	path := filepath.Join(t.TempDir(), "snapshots", "S_test.pla")
	w, err := Create(path, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if err := w.WriteChunk(c); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	if _, got, err := ReadAll(path); err != nil || len(got) != 2 {
		t.Fatalf("слепок не читается: %v", err)
	}
}
