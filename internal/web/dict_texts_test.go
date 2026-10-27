package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyCode(t *testing.T) {
	l := dictLookup{plcID: 2,
		dict:  map[string]int64{"ERR": 5},
		texts: map[int64]map[int64]string{5: {7501: "перегрузка по току"}}}

	// Код есть в справочнике — текст
	v := eventView{Signal: "ERR", Message: "Привод стола: код 7501"}
	l.applyCode(&v)
	if v.CodeText != "перегрузка по току" || v.Describe != "" {
		t.Fatalf("известный код: %+v", v)
	}
	// Кода нет — ссылка «описать» с подставленным числом
	v = eventView{Signal: "ERR", Message: "Привод стола: код 7900"}
	l.applyCode(&v)
	if v.CodeText != "" || v.Describe != "/dicts/5?plc=2&v=7900" {
		t.Fatalf("неизвестный код: %+v", v)
	}
	// У сигнала нет справочника — ни текста, ни ссылки; у бита кода нет вовсе
	for _, e := range []eventView{
		{Signal: "OTHER", Message: "Насос: код 3"},
		{Signal: "ERR", Message: "Дверь открыта"},
	} {
		l.applyCode(&e)
		if e.CodeText != "" || e.Describe != "" {
			t.Errorf("не должно быть ни текста, ни ссылки: %+v", e)
		}
	}
}

func TestCodeTextTemplate(t *testing.T) {
	list := []eventView{
		{Signal: "ERR", Message: "Привод: код 7501", CodeText: "перегрузка <ток>", Active: true},
		{Signal: "ERR", Message: "Привод: код 7900", Describe: "/dicts/5?plc=2&v=7900", Active: true},
	}
	for _, name := range []string{"alarms", "eventlist"} {
		var data any = list
		if name == "alarms" {
			data = map[string]any{"List": list, "Alarms": 2, "PLCID": int64(2)}
		}
		w := httptest.NewRecorder()
		render(w, name, data)
		body := strings.Join(strings.Fields(w.Body.String()), " ")
		for _, s := range []string{"код 7501 — перегрузка &lt;ток&gt;", `href="/dicts/5?plc=2&amp;v=7900"`, ">описать</a>"} {
			if !strings.Contains(body, s) {
				t.Errorf("шаблон %s: нет %q\n%s", name, s, body)
			}
		}
	}
}
