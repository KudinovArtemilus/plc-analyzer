package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Выбор сигналов в «Истории»: поле поиска, счётчик и адрес у каждого сигнала (по нему тоже ищут)
func TestHistoryPickFind(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	pl, _ := st.PLC(ctx, id)

	root := t.TempDir()
	rel := "2026/11/09/A_20261109-080000_g1.pla"
	path := filepath.Join(root, "SGGT", filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	w, err := archive.Create(path, archive.Header{PLC: "SGGT", Shift: "A", ShiftDate: "2026-11-09",
		Groups: []archive.GroupInfo{{PeriodMs: 100, Signals: []archive.SignalInfo{
			{Name: "Давление зажима", Addr: "DB103.DBD4", Type: "REAL", Scale: 1, Unit: "бар"},
			{Name: "Дверь закрыта", Addr: "DB103.DBX12.0", Type: "BOOL", Scale: 1},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	w.WriteChunk(archive.Chunk{Group: 0, Times: []int64{1000, 1100}, Values: [][]float64{{1, 2}, {0, 1}}})
	w.Close()

	cache := archive.NewCache(2)
	h := (&Server{Store: st, PLC: pl, DataRoot: root, Cache: cache}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/ui/history/pick?plc=%d&file=%s", id, rel), nil))
	page := rec.Body.String()
	for _, want := range []string{
		`type="search"`, `oninput="pickFind(this.form)"`, `class="pick-count`, `class="pick-none`,
		"DB103.DBD4 · бар · 100 мс", "DB103.DBX12.0 · 100 мс", // адрес — в подписи: по нему ищут
	} {
		if !strings.Contains(page, want) {
			t.Errorf("выбор сигналов: нет %q", want)
		}
	}
	// Выбор сигналов не загружает значения (шаг 43): в кеше — файл без единого сигнала.
	// Если бы выбор открыл файл целиком, кеш вернул бы полную копию со всеми значениями.
	if f, err := cache.GetSignals(path, nil); err != nil || f.Has(0, 0) || f.Has(0, 1) {
		t.Errorf("выбор сигналов загрузил значения: %v", err)
	}

	// Функции поиска — на самой странице «История»
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/history?plc=%d", id), nil))
	for _, fn := range []string{"function pickFind(", "function pickCount(", "function pickSet("} {
		if !strings.Contains(rec.Body.String(), fn) {
			t.Errorf("«История»: нет %s", fn)
		}
	}
}
