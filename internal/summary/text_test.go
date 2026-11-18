package summary

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	v, lo, hi := 182.4, 170.0, 190.0
	s := Summary{At: "2026-11-17T10:00:00+05:00", Period: 60, Areas: []Area{{Name: "Мехобработка", Machines: []Machine{
		{Name: "Шлифовка", Online: true, State: &State{Name: "Авария", Color: AlarmColor}, Step: "12 · Зажим закрыт",
			Shift:   &Shift{Name: "А", From: "08:00", Shares: []Share{{Name: "Автомат", Color: "blue", Pct: 37.5}, {Name: "Авария", Color: AlarmColor, Pct: 12.5}}},
			Signals: []Signal{{Label: "Давление, бар", Value: &v, Min: &lo, Max: &hi}, {Label: "Масло"}},
			Alarms:  []Alarm{{Text: "Дверь открыта", Since: "09:58"}}},
		{Name: "Токарный", Online: false},
	}}}}
	want := `Мехобработка
🔴 Шлифовка — Авария
   шаг 12 · Зажим закрыт
   смена А с 08:00: Автомат 37.5% · Авария 12.5%
   Давление, бар: 182.4 (170–190)
   Масло: —
   ■ Дверь открыта — с 09:58
▫️ Токарный — нет связи

на 10:00:00`
	if got := Text(s); got != want {
		t.Errorf("текст:\n%s\nждали:\n%s", got, want)
	}
	if got := Text(Summary{}); !strings.Contains(got, "Ни один станок") {
		t.Errorf("пустая сводка: %s", got)
	}
	// Очень длинная — обрезается под предел Telegram
	long := Summary{Areas: []Area{{Name: strings.Repeat("я", 5000)}}}
	if n := len([]rune(Text(long))); n > TextMax {
		t.Errorf("длина %d > %d", n, TextMax)
	}
}
