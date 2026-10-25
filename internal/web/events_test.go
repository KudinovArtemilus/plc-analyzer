package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestToView(t *testing.T) {
	came := time.Date(2026, 10, 23, 23, 59, 58, 500e6, time.Local)
	now := came.Add(90 * time.Second)

	// Активная авария: «ушла» пусто, длительность — до текущего момента
	act := toView(store.Event{Signal: "AL1", Kind: store.KindAlarm, Message: "Перегрев", Came: came}, "С1", now)
	if !act.Active || act.Went != "" || act.Dur != "1 мин 30 с" || act.Warn {
		t.Fatalf("активная: %+v", act)
	}
	if act.Came != "23:59:58.500" {
		t.Fatalf("время прихода %q", act.Came)
	}

	// Ушла после полуночи — с датой; предупреждение
	went := came.Add(2500 * time.Millisecond)
	done := toView(store.Event{Signal: "W1", Kind: store.KindWarning, Came: came, Went: went}, "С1", now)
	if done.Active || !done.Warn || done.Went != "24.10 00:00:01.000" || done.Dur != "2,500 с" {
		t.Fatalf("ушедшая: %+v", done)
	}

	// Идущая меньше минуты — целыми секундами
	short := toView(store.Event{Signal: "AL2", Kind: store.KindAlarm, Came: now.Add(-12300 * time.Millisecond)}, "С1", now)
	if short.Dur != "12 с" {
		t.Fatalf("идущая 12,3 с: %q, ожидалось «12 с»", short.Dur)
	}
}

// Шаблоны разбираются при запуске программы; ошибка в них — паника на старте.
// Этот тест рендерит их на примерах, чтобы ошибка нашлась раньше.
func TestEventTemplates(t *testing.T) {
	list := []eventView{
		{Came: "08:00:00.000", Dur: "5 с", PLC: "С1", Signal: "AL1", PlotSig: "AL1", Message: "Перегрев <масла>", Kind: "авария", Active: true},
		{Came: "07:00:00.000", Went: "07:00:02.000", Dur: "2 с", PLC: "С1", Signal: "W1", Message: "Уровень", Kind: "предупреждение", Warn: true},
	}

	cases := []struct {
		name string
		data any
		want []string
	}{
		{"eventlist", list, []string{"ev-alarm", "активна", "07:00:02.000",
			"Перегрев &lt;масла&gt;"}}, // текст пользователя экранируется — html/template
		{"eventlist", []eventView{}, []string{"событий нет"}},
		{"alarms", map[string]any{"List": list, "Alarms": 1, "Warns": 1, "PLCID": int64(2)},
			[]string{"Аварий: 1", "Предупреждений: 1", "bar bad", "bar warn", "/events?plc=2",
				`data-sig="AL1"`}}, // кнопка «на график» знает свой сигнал
		{"alarms", map[string]any{"List": []eventView{}, "PLCID": int64(2)}, []string{"Активных аварий нет"}},
		{"events", map[string]any{
			"Q":    eventsQuery{PLCID: 2, Day: "2026-10-23", Kind: "alarm"},
			"PLCs": []store.PLC{{ID: 1, Name: "С1"}, {ID: 2, Name: "С2"}},
			"List": list,
		}, []string{`value="2026-10-23"`, `value="2" selected`, `value="alarm" selected`, "/signals?plc=2"}},
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
	}
}

func TestParseEventsQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/events?plc=x&day=вчера&kind=drop", nil)
	q := parseEventsQuery(r)
	if q.PLCID != 0 || q.Day != time.Now().Format("2006-01-02") || q.Kind != "" {
		t.Fatalf("плохие параметры должны давать «все ПЛК, сегодня, все типы»: %+v", q)
	}
}
