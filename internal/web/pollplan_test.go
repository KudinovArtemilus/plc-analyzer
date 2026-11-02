package web

import (
	"net/http/httptest"
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
