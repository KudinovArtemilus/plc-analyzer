package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestShelfTemplates(t *testing.T) {
	list := []shelfView{
		{ID: 7, Signal: "AL1", Message: "Перегрев", Reason: "датчик <сломан>", Who: "веб 10.0.0.5",
			From: "25.10 08:00", Until: "25.10 20:00", State: "действует · осталось 11 ч 30 мин", Active: true},
		{ID: 6, Signal: "AL2", Reason: "ремонт", From: "24.10 08:00", Until: "24.10 09:00",
			State: "истёк — авария вернулась на панель"},
	}
	cases := []struct {
		name string
		data any
		want []string
		not  []string
	}{
		{"shelveform", shelfForm{PLCID: 2, Signal: "AL1", Message: "Перегрев", Hours: 12, Options: shelfHours},
			[]string{`name="sig" value="AL1"`, `value="12" selected`, "Перегрев", "required"}, nil},
		{"shelveform", shelfForm{PLCID: 2, Signal: "AL1", Hours: 1, Options: shelfHours, Err: "укажите причину"},
			[]string{`value="1" selected`, "укажите причину"}, nil},
		{"shelvedone", map[string]any{"Signal": "AL1", "Until": "25.10 20:00", "PLCID": int64(2)},
			[]string{"до 25.10 20:00", "/shelves?plc=2"}, nil},
		// Кнопка «снять» — только у действующего шунта (id 7), у истёкшего (id 6) её нет
		{"shelflist", map[string]any{"List": list, "PLCID": int64(2), "Err": ""},
			[]string{"/ui/shelves/7/remove?plc=2", "датчик &lt;сломан&gt;", "истёк"},
			[]string{"/ui/shelves/6/remove"}},
		{"shelflist", map[string]any{"List": []shelfView{}, "PLCID": int64(2), "Err": ""},
			[]string{"шунтов не было"}, nil},
		{"shelves", map[string]any{
			"PLC": store.PLC{ID: 2, Name: "С2"}, "PLCs": []store.PLC{{ID: 2, Name: "С2"}},
			"Table": map[string]any{"List": list, "PLCID": int64(2), "Err": ""},
		}, []string{"Шунты аварий ПЛК", "/ui/shelves/7/remove"}, nil},
		// Панель: кнопка «шунт» и счётчик шунтов
		{"alarms", map[string]any{
			"List":   []eventView{{Signal: "AL3", Message: "Дверь", Came: "08:00:00.000", Dur: "5 с"}},
			"Alarms": 1, "PLCID": int64(2), "Shelves": 1, "Shelved": 1,
		}, []string{"/ui/shelve?plc=2&sig=AL3", "шунтов: 1", "скрыто активных: 1"}, nil},
		{"alarms", map[string]any{"List": []eventView{}, "PLCID": int64(2), "Shelves": 2, "Shelved": 0},
			[]string{"Активных аварий нет", "шунтов: 2"}, []string{"скрыто активных"}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		render(w, c.name, c.data)
		body := w.Body.String()
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("шаблон %s: нет %q в\n%s", c.name, s, body)
			}
		}
		for _, s := range c.not {
			if strings.Contains(body, s) {
				t.Errorf("шаблон %s: не должно быть %q", c.name, s)
			}
		}
	}
}

func TestValidHours(t *testing.T) {
	if !validHours(12) || validHours(0) || validHours(1000) {
		t.Fatal("validHours: разрешены только сроки из списка")
	}
}
