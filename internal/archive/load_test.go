package archive

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListFiles(t *testing.T) {
	root := t.TempDir()
	h := Header{Groups: []GroupInfo{{PeriodMs: 100, Signals: []SignalInfo{{Name: "p"}}}}}

	for _, started := range []time.Time{at(21, 8, 0), at(21, 20, 0), at(22, 3, 15)} {
		path := Path(root, "Пресс-1", ShiftOf(started), started, 1)
		w, err := Create(path, h)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}

	files, err := ListFiles(root, "Пресс-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("файлов %d, ожидалось 3", len(files))
	}
	// Новые сверху; ночной перезапуск 22-го в 03:15 относится к смене Б 21-го
	if !files[0].Started.Equal(at(22, 3, 15)) || files[0].Shift.Name != "B" || files[0].Shift.Date.Day() != 21 {
		t.Errorf("первый файл: %+v", files[0])
	}
	if files[2].Shift.Name != "A" {
		t.Errorf("последний файл должен быть сменой А: %+v", files[2])
	}

	// ПЛК без записей — пустой список, не ошибка
	if f, err := ListFiles(root, "нет такого"); err != nil || len(f) != 0 {
		t.Errorf("ожидался пустой список без ошибки: %v %v", f, err)
	}
}

func TestCacheReloadsGrowingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.pla")
	w, err := Create(path, testHeader())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.WriteChunk(chunk1)

	c := NewCache(2)
	f1, err := c.Get(path)
	if err != nil {
		t.Fatal(err)
	}
	if f2, _ := c.Get(path); f2 != f1 {
		t.Error("файл не менялся — должен прийти из кеша")
	}

	w.WriteChunk(chunk2) // файл «вырос», как текущая смена
	f3, _ := c.Get(path)
	if f3 == f1 || len(f3.Groups[1].Times) != 2 {
		t.Error("выросший файл должен перечитаться с новыми данными")
	}
}
