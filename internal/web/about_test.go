package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/about"
	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Подпись программы — на страницах, в /api/about (для Live, просмотра и PNG) и в CSV
func TestAboutEverywhere(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.1", Slot: 1})
	pl, _ := st.PLC(ctx, id)

	// Файл смены для выгрузки CSV
	root := t.TempDir()
	rel := "2026/11/06/A_20261106-080000_g1.pla"
	path := filepath.Join(root, "Пресс", filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	w, err := archive.Create(path, archive.Header{PLC: "Пресс", Shift: "A", ShiftDate: "2026-11-06",
		Groups: []archive.GroupInfo{{PeriodMs: 100, Signals: []archive.SignalInfo{{Name: "P", Type: "REAL", Scale: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	w.WriteChunk(archive.Chunk{Group: 0, Times: []int64{1000, 1100}, Values: [][]float64{{1, 2}}})
	w.Close()

	h := (&Server{Store: st, PLC: pl, DataRoot: root, Cache: archive.NewCache(4)}).Handler()
	get := func(target string) string {
		r := httptest.NewRequest("GET", target, nil) // 127.0.0.1 — без пароля
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Body.String()
	}
	line := about.Line()

	// Страницы-шаблоны: подвал с подписью
	for _, p := range []string{"/signals", "/plcs", "/history", "/events", "/login"} {
		if !strings.Contains(get(p), `<footer class="about">`+line+`</footer>`) {
			t.Errorf("%s: нет подвала с подписью", p)
		}
	}
	// Live и просмотр: пустой подвал, текст подставит isa101.js из /api/about
	for _, p := range []string{"/", "/view.html"} {
		if !strings.Contains(get(p), `<footer class="about"></footer>`) {
			t.Errorf("%s: нет подвала", p)
		}
	}
	if !strings.Contains(get("/isa101.js"), `fetch("/api/about")`) {
		t.Error("isa101.js не берёт подпись с сервера")
	}
	var a map[string]string
	if err := json.Unmarshal([]byte(get("/api/about")), &a); err != nil || a["line"] != line || a["author"] != "Артём Кудинов" {
		t.Errorf("/api/about: %v %v", a, err)
	}

	// CSV: после метки UTF-8 — строка-подпись, потом заголовок таблицы
	csv := strings.TrimPrefix(get(fmt.Sprintf("/api/history/csv?plc=%d&file=%s", id, rel)), "\xEF\xBB\xBF")
	lines := strings.Split(csv, "\r\n")
	if !strings.HasPrefix(lines[0], "# "+line+" · выгружено ") || !strings.HasPrefix(lines[1], "Время;P") {
		t.Errorf("CSV:\n%s", csv)
	}
}
