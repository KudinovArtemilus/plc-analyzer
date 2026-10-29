package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// TestHelperProcess — не тест, а «поддельный Python». Тест запускает сам тестовый бинарник
// с -test.run=TestHelperProcess, и эта функция притворяется скриптом: пишет report.html
// со своими аргументами или падает с текстом в stderr. Так проверяется запуск программы
// без настоящего Python — тесты идут на любом ПК. (Тот же приём — в тестах пакета os/exec.)
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_PYTHON") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:] // скрипт, папка ПЛК, папка отчёта, дальше параметры
	if os.Getenv("GO_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "в папке нет паспортов — настроен ли паспорт?")
		os.Exit(1)
	}
	os.MkdirAll(args[2], 0o755)
	os.WriteFile(filepath.Join(args[2], "report.html"), []byte("отчёт: "+strings.Join(args, " | ")), 0o644)
	os.Exit(0)
}

// fakePython — команда, которая вместо python запустит TestHelperProcess
func fakePython(fail bool) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=TestHelperProcess", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "GO_HELPER_PYTHON=1")
		if fail {
			cmd.Env = append(cmd.Env, "GO_HELPER_FAIL=1")
		}
		return cmd
	}
}

func TestExplore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plcID, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	st.SavePassport(ctx, "тест", store.Passport{PLCID: plcID, Step: "Шаги", Mode: "Режим", ModeVal: 1})
	pl, _ := st.PLC(ctx, plcID)
	root := t.TempDir()
	s := &Server{Store: st, PLC: pl, DataRoot: root, Scripts: "scripts", command: fakePython(false)}
	h := s.Handler()

	get := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.RemoteAddr = "10.0.0.99:5000" // чужой ПК — смотреть анализ можно и ему
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	plc := "/explore?plc=" + itoa(plcID)

	// Тренды: скрипт получил свой файл, папку ПЛК и папку отчёта; отчёт отдан в браузер
	w := get(plc + "&kind=trend")
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, filepath.Join("scripts", "trend.py")) ||
		!strings.Contains(b, filepath.Join(root, "SGGT")) || !strings.Contains(b, filepath.Join(root, "explore", "SGGT", "trend")) {
		t.Fatalf("тренды: код %d, %s", w.Code, b)
	}

	// Отчёт свежий — второй раз Python не запускаем, отдаём готовый
	report := filepath.Join(root, "explore", "SGGT", "trend", "report.html")
	os.WriteFile(report, []byte("готовый"), 0o644)
	if b := get(plc + "&kind=trend").Body.String(); b != "готовый" {
		t.Errorf("свежий отчёт пересчитан: %q", b)
	}
	if b := get(plc + "&kind=trend&fresh=1").Body.String(); !strings.HasPrefix(b, "отчёт:") {
		t.Errorf("fresh=1 не пересчитал: %q", b)
	}

	// Аномалии: номер ПЛК, ссылки от корня сайта, сигналы шага и режима из паспорта
	b = get(plc + "&kind=anomaly").Body.String()
	if !strings.Contains(b, "--plc | "+itoa(plcID)+" | --url |  | --step | Шаги | --mode | Режим") {
		t.Errorf("аномалии: %s", b)
	}

	// Чужой файл через kind запустить нельзя
	if w := get(plc + "&kind=..%2F..%2Fevil"); w.Code != http.StatusBadRequest {
		t.Errorf("неизвестный вид: код %d", w.Code)
	}

	// Скрипт упал — в ответе его объяснение из stderr
	s.command = fakePython(true)
	w = get(plc + "&kind=anomaly&fresh=1")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "нет паспортов") {
		t.Errorf("ошибка скрипта: код %d, %s", w.Code, w.Body.String())
	}

	// Python не найден — подсказка про -python
	s.command, s.Python = nil, "нет-такого-python-xyz"
	w = get(plc + "&kind=trend&fresh=1")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "-python") {
		t.Errorf("нет python: код %d, %s", w.Code, w.Body.String())
	}
}

func TestPythonPath(t *testing.T) {
	dir := t.TempDir()
	s := &Server{Scripts: dir}
	if p := s.pythonPath(); p != "python" {
		t.Errorf("без окружения: %q, ждали python из PATH", p)
	}
	// Окружение рядом со скриптами — берём его (полным путём)
	venv := filepath.Join(dir, ".venv", "Scripts", "python.exe")
	os.MkdirAll(filepath.Dir(venv), 0o755)
	os.WriteFile(venv, nil, 0o755)
	if p := s.pythonPath(); p != venv {
		t.Errorf("с окружением: %q, ждали %q", p, venv)
	}
	// -python важнее всего
	s.Python = `C:\Python312\python.exe`
	if p := s.pythonPath(); p != s.Python {
		t.Errorf("с -python: %q", p)
	}
}
