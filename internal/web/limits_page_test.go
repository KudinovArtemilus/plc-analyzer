package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestLimitFromForm(t *testing.T) {
	// Пустые поля — границы нет; запятая вместо точки тоже понимается
	l, err := limitFromForm(limitView{Hi: "180,5", Hyst: "", Jump: "", JumpMs: "1000", Kind: "alarm"})
	if err != nil || l.Lo != nil || l.Hi == nil || *l.Hi != 180.5 || l.Jump != nil || l.Kind != "alarm" {
		t.Fatalf("только верх: %+v, %v", l, err)
	}

	// Все поля пустые — это «снять нормы», не ошибка
	if l, err := limitFromForm(limitView{JumpMs: "1000", Kind: "warning"}); err != nil || !l.Empty() {
		t.Fatalf("пустая форма: %+v, %v", l, err)
	}

	bad := []limitView{
		{Lo: "abc", Kind: "warning"},                    // не число
		{Lo: "200", Hi: "100", Kind: "warning"},         // низ выше верха
		{Jump: "5", JumpMs: "секунда", Kind: "warning"}, // время не число
		{Hi: "10", Kind: "trend"},                       // не тот тип
	}
	for i, v := range bad {
		if _, err := limitFromForm(v); err == nil {
			t.Errorf("форма %d должна дать ошибку: %+v", i, v)
		}
	}
}

func TestLimitTemplates(t *testing.T) {
	sig := store.SignalRow{ID: 7, Name: "P_hyd", Unit: "бар", Type: "REAL", Kind: store.KindTrend, Enabled: true}
	hi := 180.0
	v := newLimitView(sig, store.Limit{Hi: &hi, Hyst: 2, Kind: store.KindAlarm}, true)

	cases := []struct {
		name string
		data any
		want []string
	}{
		{"limitrow", v, []string{"P_hyd", "не выше 180 бар, возврат 2 · авария", "/ui/limits/7/edit"}},
		{"limitrow", newLimitView(sig, store.Limit{}, false), []string{">нет<"}},
		{"limitedit", v, []string{`name="hi" value="180"`, `value="alarm" selected`, `hx-put="/ui/limits/7"`}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		render(w, c.name, c.data)
		body := strings.Join(strings.Fields(w.Body.String()), " ")
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("шаблон %s: нет %q\n%s", c.name, s, body)
			}
		}
	}
}

func TestToViewLimit(t *testing.T) {
	now := time.Now()
	// Нарушение нормы — кнопки «на график» нет: сам сигнал и так дорожка
	if v := toView(store.Event{Signal: "P_hyd#hi", Kind: store.KindWarning, Came: now}, "С1", now); v.PlotSig != "" {
		t.Fatalf("норма: PlotSig %q, ожидалось пусто", v.PlotSig)
	}
	if v := toView(store.Event{Signal: "AL1", Kind: store.KindAlarm, Came: now}, "С1", now); v.PlotSig != "AL1" {
		t.Fatalf("аварийный бит: PlotSig %q, ожидалось AL1", v.PlotSig)
	}
}
