package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Шаг 43b: каждое место, которое открывает файл смены, держит в памяти только нужные
// сигналы. Проверка — по кешу: после запроса в нём должен лежать файл без лишних сигналов.
// Если обработчик откроет файл целиком (как до шага 43), тест это увидит.
func TestLoadOnlySelected(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	pl, _ := st.PLC(ctx, id)

	// Пять сигналов: Давление (REAL), Шаг и Режим (INT), Дверь (BOOL), Лишний (REAL)
	root := t.TempDir()
	rel := "2026/11/09/A_20261109-080000_g1.pla"
	path := filepath.Join(root, "SGGT", filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	names := []string{"Давление", "Шаг", "Режим", "Дверь", "Лишний"}
	types := []string{"REAL", "INT", "INT", "BOOL", "REAL"}
	var sig []archive.SignalInfo
	for i, n := range names {
		sig = append(sig, archive.SignalInfo{Name: n, Type: types[i], Scale: 1})
	}
	w, _ := archive.Create(path, archive.Header{PLC: "SGGT", Shift: "A", ShiftDate: "2026-11-09",
		Groups: []archive.GroupInfo{{PeriodMs: 100, Signals: sig}}})
	c := archive.Chunk{Values: make([][]float64, len(sig))}
	for k := range 40 {
		c.Times = append(c.Times, 1_000_000+int64(k)*100)
		for s := range sig {
			c.Values[s] = append(c.Values[s], float64((k/5)%3+s))
		}
	}
	w.WriteChunk(c)
	w.Close()

	// loaded — какие сигналы лежат в кеше после запроса
	loaded := func(cache *archive.Cache) []string {
		f, err := cache.GetSignals(path, nil) // ничего не дочитывает: «без значений» покрыто любым
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for si, sg := range f.Header.Groups[0].Signals {
			if f.Has(0, si) {
				out = append(out, sg.Name)
			}
		}
		return out
	}
	enc := url.QueryEscape
	base := fmt.Sprintf("plc=%d&file=%s", id, enc(rel))
	cases := []struct {
		name, method, target, body string
		want                       string // какие сигналы должны оказаться в памяти
	}{
		{"заголовок просмотра", "GET", "/api/history/meta?" + base + "&s=" + enc("Давление"), "", ""},
		{"данные графика", "GET", "/api/history/data?width=500&" + base + "&s=" + enc("Давление") + "&s=" + enc("Дверь"), "", "Давление Дверь"},
		{"Σ Статистика", "GET", "/ui/history/stats?" + base + "&s=" + enc("Давление"), "", "Давление"},
		{"CSV", "GET", "/api/history/csv?" + base + "&s=" + enc("Дверь"), "", "Дверь"},
		{"Шаги", "GET", "/steps?" + base + "&sig=" + enc("Шаг") + "&mode=" + enc("Режим"), "", "Шаг Режим"},
		{"Шаги без выбора", "GET", "/steps?" + base, "", ""},
		{"слепок", "POST", "/api/snapshots", fmt.Sprintf(`{"file":%q,"signals":["Давление"],"from_ms":1000000,"to_ms":1003900,"name":"проба"}`, rel), "Давление"},
		// Пустой ?s= по старому правилу — «все сигналы»: тогда файл целиком, как раньше
		{"все сигналы", "GET", "/api/history/data?width=500&" + base, "", "Давление Шаг Режим Дверь Лишний"},
	}
	for _, tc := range cases {
		cache := archive.NewCache(2)
		h := (&Server{Store: st, PLC: pl, DataRoot: root, Cache: cache}).Handler()
		target := tc.target
		if tc.method == "POST" {
			target += "?plc=" + fmt.Sprint(id)
		}
		r := httptest.NewRequest(tc.method, target, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:5000" // ПК анализатора — слепок сохранять можно
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Errorf("%s: код %d: %s", tc.name, rec.Code, rec.Body.String())
			continue
		}
		if got := strings.Join(loaded(cache), " "); got != tc.want {
			t.Errorf("%s: в памяти %q, ждали %q", tc.name, got, tc.want)
		}
	}
}
