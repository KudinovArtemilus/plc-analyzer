package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestDictTemplates(t *testing.T) {
	steps := store.Dict{ID: 3, Name: "Шаги", Items: 2, Used: 1}
	cases := []struct {
		name string
		data any
		want []string
	}{
		{"dicts", dictsView{PLC: 2, List: []store.Dict{steps}},
			[]string{`action="/dicts?plc=2"`, `href="/dicts/3?plc=2"`, "Шаги", "<b>Справочники</b>"}},
		{"dicts", dictsView{PLC: 2, Name: "Шаги", Err: "имя занято"}, []string{"Справочников пока нет", "имя занято", `value="Шаги"`}},
		// Ссылка «описать» с панели аварий: число подставлено, курсор — в поле текста
		{"dict", dictsView{PLC: 2, Dict: steps, Value: "7501",
			Items: []store.DictItem{{Value: 12, Text: "Зажим <закрыт>"}}},
			[]string{`action="/dicts/3/items?plc=2"`, `name="value" value="7501"`, "Зажим &lt;закрыт&gt;",
				"/dicts/3/items/12/delete", "v=12", `href="/dicts?plc=2"`}},
		// Выбор справочника в форме сигнала: назначенный выбран
		{"dictselect", formView{Row: store.SignalRow{DictID: 3}, Dicts: []store.Dict{{ID: 1, Name: "Привод"}, steps}},
			[]string{`name="dict"`, `value="0"`, `value="3" selected`, "Привод"}},
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

func TestCheckSignalDict(t *testing.T) {
	row := func(addr, typ string) *store.SignalRow {
		return &store.SignalRow{Name: "S", Addr: addr, Type: typ, PeriodMs: 100, Kind: store.KindTrend, DictID: 3}
	}
	if err := checkSignal(row("DB1.DBW0", "INT")); err != nil {
		t.Errorf("справочник у INT: %v", err)
	}
	for _, r := range []*store.SignalRow{row("DB1.DBD0", "REAL"), row("DB1.DBX0.0", "BOOL")} {
		if err := checkSignal(r); err == nil {
			t.Errorf("справочник у %s должен быть отклонён", r.Type)
		}
	}
}
