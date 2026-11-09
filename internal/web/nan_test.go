package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Запись, где REAL прислал «не число»: график, статистика и CSV работают (до шага 44 —
// пустой ответ данных графика и ни одного сигнала на экране)
func TestNaNInRecord(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "n.db"))
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
	w, _ := archive.Create(path, archive.Header{PLC: "SGGT", Shift: "A", ShiftDate: "2026-11-09",
		Groups: []archive.GroupInfo{{PeriodMs: 100, Signals: []archive.SignalInfo{
			{Name: "Давление", Type: "REAL", Scale: 1}, {Name: "Шаг", Type: "INT", Scale: 1}}}}})
	w.WriteChunk(archive.Chunk{Times: []int64{1000, 1100, 1200, 1300},
		Values: [][]float64{{10, math.NaN(), math.Inf(1), 30}, {1, 2, 3, 4}}})
	w.Close()

	h := (&Server{Store: st, PLC: pl, DataRoot: root, Cache: archive.NewCache(2)}).Handler()
	get := func(target string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec.Code, rec.Body.String()
	}
	q := fmt.Sprintf("plc=%d&file=%s&s=%s&s=%s", id, url.QueryEscape(rel), url.QueryEscape("Давление"), url.QueryEscape("Шаг"))

	// Данные графика: правильный JSON, NaN и Inf — null, остальное на месте
	code, body := get("/api/history/data?width=1000&" + q)
	var data struct {
		Series []struct {
			Name string     `json:"name"`
			V    []*float64 `json:"v"`
		} `json:"series"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &data) != nil || len(data.Series) != 2 {
		t.Fatalf("данные графика: %d %q", code, body)
	}
	if v := data.Series[0].V; len(v) != 4 || *v[0] != 10 || v[1] != nil || v[2] != nil || *v[3] != 30 {
		t.Errorf("давление: %s", body)
	}

	// Статистика: по числам 10 и 30, «не число: 2 раз»
	_, body = get("/ui/history/stats?" + q)
	for _, want := range []string{"▲ не число: 2 раз", ">10<", ">30<", ">20<"} {
		if !strings.Contains(body, want) {
			t.Errorf("статистика: нет %q", want)
		}
	}

	// CSV: пустые ячейки вместо NaN и +Inf
	_, body = get("/api/history/csv?" + q)
	if strings.Contains(body, "NaN") || strings.Contains(body, "Inf") || !strings.Contains(body, ";;2") {
		t.Errorf("CSV:\n%s", body)
	}
}
