package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFileRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log") // t.TempDir сам удалится после теста
	l, err := openLog(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 байт

	for i := 0; i < 2; i++ { // 80 байт — в пределах 100
		l.Write([]byte(line))
	}
	if _, err := os.Stat(path + ".old"); err == nil {
		t.Fatal(".old появился раньше времени")
	}

	l.Write([]byte(line)) // 120 > 100 — переворот ДО записи, строка идёт в новый файл
	l.Close()

	old, _ := os.ReadFile(path + ".old")
	cur, _ := os.ReadFile(path)
	if len(old) != 80 || len(cur) != 40 {
		t.Fatalf("после переворота: .old %d байт (ждали 80), новый %d (ждали 40)", len(old), len(cur))
	}

	// Повторный запуск программы дописывает, а не затирает
	l, err = openLog(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte(line))
	l.Close()
	if cur, _ = os.ReadFile(path); len(cur) != 80 {
		t.Fatalf("после перезапуска %d байт, ждали 80 — файл затёрт?", len(cur))
	}
}
