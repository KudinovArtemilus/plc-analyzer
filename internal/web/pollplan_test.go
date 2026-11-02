package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func row(name, addr, typ string, period int64) store.SignalRow {
	return store.SignalRow{Name: name, Addr: addr, Type: typ, PeriodMs: period, Enabled: true}
}

func TestPollPlan(t *testing.T) {
	rows := []store.SignalRow{
		row("Давление", "DB103.DBD24", "REAL", 100),
		row("Шаги", "DB103.DBW76", "INT", 100),
		row("Зажим, ошибка", "DB103.DBW80", "INT", 100),
		row("Дверь, ошибка", "DB103.DBW80", "INT", 100), // опечатка: тот же адрес
		row("K16", "DB103.DBX100.3", "BOOL", 100),
		row("S23", "DB103.DBX100.3", "BOOL", 100), // и здесь
		row("Далеко", "DB103.DBW400", "INT", 100), // пропуск больше 64 байт — отдельный запрос
		row("Другой DB", "DB5.DBW0", "INT", 100),
		row("Медленный", "DB103.DBD0", "REAL", 1000),
		{Name: "Выключен", Addr: "DB103.DBW2", Type: "INT", PeriodMs: 100},
		row("Плохой", "DB103.XYZ", "INT", 100),
	}
	v := pollPlan(rows, 240)
	if v.Total != 11 || v.Enabled != 10 || len(v.Bad) != 1 {
		t.Fatalf("сигналов %d/%d, плохих %v", v.Enabled, v.Total, v.Bad)
	}
	if len(v.Groups) != 2 || v.Groups[0].Period != "100 мс" || v.Groups[1].Period != "1 с" {
		t.Fatalf("группы: %+v", v.Groups)
	}
	// 100 мс: DB5 (сортировка по DB), DB103 0–101, DB103 400 → 3 запроса; 1 с: 1 запрос
	b := v.Groups[0].Blocks
	if len(b) != 3 || b[0].DB != 5 || b[1].From != 24 || b[1].To != 100 || !strings.Contains(b[2].Why, "пропуск") ||
		!strings.Contains(b[1].Why, "другой DB") {
		t.Fatalf("блоки 100 мс: %+v", b)
	}
	if v.Requests != 4 || v.PerSec != "31" { // 3 × 10/с + 1 × 1/с
		t.Errorf("запросов %d, в секунду %s", v.Requests, v.PerSec)
	}
	if len(v.Dups) != 2 || v.Dups[0].Addr != "DB103.DBW80" || v.Dups[0].Names != "Зажим, ошибка, Дверь, ошибка" ||
		v.Dups[1].Addr != "DB103.DBX100.3" {
		t.Errorf("одинаковые адреса: %+v", v.Dups)
	}

	// На странице: план и предупреждение об одинаковых адресах, страница целиком
	w := httptest.NewRecorder()
	render(w, "page", map[string]any{"PLC": store.PLC{ID: 1}, "Plan": v, "Form": formView{}})
	page := w.Body.String()
	for _, want := range []string{"План опроса:", "10 сигналов → 4", "одинаковые адреса: 2", "DB103.DBW80", "пропуск", "</html>"} {
		if !strings.Contains(page, want) {
			t.Errorf("страница: нет %q", want)
		}
	}
}

func TestFreeSpace(t *testing.T) {
	cases := []struct {
		from, limit, free int
		hint              string
	}{
		{113, 200, 87, "или 21 REAL/DINT, или 43 INT/WORD, или 696 BOOL"}, // SGGT: с нечётного 113 — числа со 114
		{180, 200, 20, "или 5 REAL/DINT, или 10 INT/WORD, или 160 BOOL"},  // с чётного — ничего не теряем
		{200, 200, 0, ""}, // запрос полный
		{150, 120, 0, ""}, // следующий запрос уже начался раньше
	}
	for _, c := range cases {
		free, hint := freeSpace(c.from, c.limit)
		if free != c.free || hint != c.hint {
			t.Errorf("freeSpace(%d, %d) = %d %q, ждали %d %q", c.from, c.limit, free, hint, c.free, c.hint)
		}
	}
}

// Свободное место — в плане и на странице «Сигналы» (через сервер)
func TestPlanFree(t *testing.T) {
	// Как SGGT: DB103 байты 0–112 одним запросом при PDU 240
	// (сигнал через каждые 16 байт, чтобы дыры были меньше 64 и всё склеилось в один запрос)
	var rows []store.SignalRow
	for a := 0; a <= 96; a += 16 {
		rows = append(rows, row(fmt.Sprintf("Слово %d", a), fmt.Sprintf("DB103.DBW%d", a), "INT", 100))
	}
	rows = append(rows, row("Последний", "DB103.DBX112.0", "BOOL", 100))
	b := pollPlan(rows, 240).Groups[0].Blocks[0]
	if b.Size != 113 || b.Free != 87 || b.FreeFrom != 113 || !strings.Contains(b.FreeHint, "21 REAL") {
		t.Errorf("SGGT: %+v", b)
	}
	// При PDU 480 — тот же запрос, свободно 440 − 113
	if b := pollPlan(rows, 480).Groups[0].Blocks[0]; b.Free != 327 {
		t.Errorf("PDU 480: свободно %d", b.Free)
	}
	// Буфер пресса при PDU 240 делится: первый запрос (байты 2–197) может вырасти только
	// до начала второго (202) — свободно 4 байта, а не 200 − 196
	v := pollPlan(pressRows(), 240)
	if bl := v.Groups[0].Blocks; bl[0].Free != 4 || bl[len(bl)-1].Free == 0 {
		t.Errorf("пресс: свободно %d … %d", bl[0].Free, bl[len(bl)-1].Free)
	}

	st, _ := store.Open(filepath.Join(t.TempDir(), "f.db"))
	defer st.Close()
	ctx := context.Background()
	id, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	for _, r := range rows {
		r.PLCID, r.Scale = id, 1
		if _, err := st.AddSignal(ctx, "тест", r); err != nil {
			t.Fatal(err)
		}
	}
	pl, _ := st.PLC(ctx, id)
	w := httptest.NewRecorder()
	(&Server{Store: st, PLC: pl}).Handler().ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/signals?plc=%d", id), nil))
	page := w.Body.String()
	for _, want := range []string{"<th title=\"сколько можно добавить", "87 байт", "(с байта 113)", "или 21 REAL/DINT, или 43 INT/WORD, или 696 BOOL"} {
		if !strings.Contains(page, want) {
			t.Errorf("страница «Сигналы»: нет %q", want)
		}
	}
}
