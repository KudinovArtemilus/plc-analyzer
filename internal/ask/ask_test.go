package ask

import (
	"fmt"
	"strings"
	"testing"
)

var machines = []Machine{
	{PLCID: 1, Title: "Шлифовка", Names: []string{"Шлифовка", "SGGT", "Мехобработка"}, Signals: []Signal{
		{ID: 11, Name: "P_clamp", Label: "Давление зажима, бар", Type: "REAL"},
		{ID: 12, Name: "T_oil", Label: "Масло, °C", Type: "REAL"},
		{ID: 13, Name: "Door", Label: "Дверь закрыта", Type: "BOOL"},
	}},
	{PLCID: 2, Title: "Пресс М10", Names: []string{"Пресс М10", "Пресс", "Прессовый"}, Signals: []Signal{
		{ID: 21, Name: "Force", Label: "Усилие, т", Type: "REAL"},
		{ID: 22, Name: "P_pump", Label: "Давление насоса", Type: "REAL"},
	}},
}

func show(r Result) string {
	if r.Problem != "" {
		return "? " + r.Problem + " " + strings.Join(r.Options, "|")
	}
	var s []string
	for _, x := range r.Signals {
		s = append(s, x.Label)
	}
	per := fmt.Sprint(r.Minutes, "мин")
	if r.Shift {
		per = "смена"
	}
	return fmt.Sprintf("%s: %s · %s · график=%v стат=%v", r.Machine.Title, strings.Join(s, "+"), per, r.Chart, r.Stats)
}

func TestParse(t *testing.T) {
	cases := []struct{ q, want string }{
		{"давление шлифовка 10 мин", "Шлифовка: Давление зажима, бар · 10мин · график=true стат=true"},
		{"Покажи график давления на ПРЕССЕ за час", "Пресс М10: Давление насоса · 60мин · график=true стат=false"},
		{"статистика масла sggt за смену", "Шлифовка: Масло, °C · смена · график=false стат=true"},
		{"масло шлифовка 2 часа", "Шлифовка: Масло, °C · 120мин · график=true стат=true"},
		{"усилие пресс 30сек", "Пресс М10: Усилие, т · 1мин · график=true стат=true"},
		{"давленее шлифофка полчаса", "Шлифовка: Давление зажима, бар · 30мин · график=true стат=true"}, // опечатки
		{"дверь шлифовка за последние 15 минут", "Шлифовка: Дверь закрыта · 15мин · график=true стат=true"},
		{"масло шлифовка 99999 мин", "Шлифовка: Масло, °C · 720мин · график=true стат=true"},
		{"давление 10 мин", "? Не понял, какой станок. Шлифовка|Пресс М10"},
		{"шлифовка 10 мин", "? Не понял, какой сигнал на «Шлифовка». Давление зажима, бар|Масло, °C|Дверь закрыта"},
		{"температура шлифовка", "? Не понял, какой сигнал на «Шлифовка». Давление зажима, бар|Масло, °C|Дверь закрыта"},
	}
	for _, c := range cases {
		if got := show(Parse(c.q, machines)); got != c.want {
			t.Errorf("%q:\n  %s\nждали\n  %s", c.q, got, c.want)
		}
	}
}

// Станок один — его можно не называть
func TestParseOneMachine(t *testing.T) {
	if got := show(Parse("масло за 5 мин", machines[:1])); got != "Шлифовка: Масло, °C · 5мин · график=true стат=true" {
		t.Error(got)
	}
	if r := Parse("масло", nil); !strings.Contains(r.Problem, "Ни один станок") {
		t.Error(r.Problem)
	}
}

func TestHelp(t *testing.T) {
	h := Help(machines)
	if !strings.Contains(h, "Шлифовка: Давление зажима, бар, Масло, °C, Дверь закрыта") || !strings.Contains(h, "Пресс М10: Усилие, т") {
		t.Error(h)
	}
}
