package summary

import (
	"fmt"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// Автомат (1, 4), Ручной (2), Дверь открыта — по биту; Наладка (3) — никуда
var rules = Rules{
	States: []Rule{
		{Name: "Дверь открыта", Color: "orange", Bit: "Дверь", Pct: false},
		{Name: "Автомат", Color: "blue", Codes: []int64{1, 4}, Pct: true},
		{Name: "Ручной", Color: "steel", Codes: []int64{2}, Pct: true},
	},
	Alarm: true, Offline: true,
}

func TestClassify(t *testing.T) {
	cases := []struct {
		m    Moment
		want int
	}{
		{Moment{Online: false, Alarm: true}, InOffline},                      // без связи — важнее всего
		{Moment{Online: true, Alarm: true, Mode: 1, HasMode: true}, InAlarm}, // авария важнее режима
		{Moment{Online: true, Mode: 4, HasMode: true}, 1},
		{Moment{Online: true, Mode: 2, HasMode: true, Bits: map[string]bool{"Дверь": true}}, 0}, // выше — важнее
		{Moment{Online: true, Mode: 3, HasMode: true}, InNone},                                  // Наладка — никуда
		{Moment{Online: true}, InNone},                                                          // режим неизвестен
	}
	for i, c := range cases {
		if got := Classify(rules, c.m); got != c.want {
			t.Errorf("%d: %+v → %d, ждали %d", i, c.m, got, c.want)
		}
	}
	// «Авария» и «Нет связи» выключены в админке — не участвуют
	off := rules
	off.Alarm, off.Offline = false, false
	if got := Classify(off, Moment{Online: true, Alarm: true, Mode: 2, HasMode: true}); got != 2 {
		t.Errorf("авария выключена: %d", got)
	}
	if got := Classify(off, Moment{Online: false}); got != InNone {
		t.Errorf("нет связи выключено: %d", got)
	}
	if s := StateOf(rules, InAlarm); s == nil || s.Color != AlarmColor {
		t.Errorf("StateOf(авария): %+v", s)
	}
}

const minute = 60_000 // мс

// Смена 100 минут: запись с 0 до 90 (дальше — нет связи), режим 1 → 2 → 3,
// авария 10–20 и (перекрываясь) 15–25, дверь открыта 70–75 (в %, но не показывается)
func TestShares(t *testing.T) {
	tl := Timeline{
		From: 0, To: 100 * minute,
		Online: []steps.Span{{From: 0, To: 90 * minute}},
		Alarms: []steps.Span{{From: 15 * minute, To: 25 * minute}, {From: 10 * minute, To: 20 * minute}},
		Mode: []steps.Step{
			{Value: 1, From: 0, To: 50 * minute},
			{Value: 2, From: 50 * minute, To: 80 * minute},
			{Value: 3, From: 80 * minute, To: 90 * minute},
		},
		Bits: map[string][]steps.Step{"Дверь": {
			{Value: 0, From: 0, To: 70 * minute}, {Value: 1, From: 70 * minute, To: 75 * minute}, {Value: 0, From: 75 * minute, To: 90 * minute}}},
	}
	got := fmt.Sprint(Shares(rules, tl))
	// Автомат: 0–50 минус авария 10–25 = 35; Ручной: 50–80 минус дверь 70–75 = 25;
	// Авария: 15 (две перекрытые = одна); Нет связи: 90–100 = 10; Наладка и дверь — остаток
	want := "[{Автомат blue 35 35} {Ручной steel 25 25} {Авария red 15 15} {Нет связи gray 10 10}]"
	if got != want {
		t.Errorf("доли:\n%s\nждали\n%s", got, want)
	}
}

// Две записи за смену (перезапуск): разрыв между ними — «нет связи»; доли с одним знаком
func TestSharesGapAndRounding(t *testing.T) {
	r := Rules{States: []Rule{{Name: "Автомат", Color: "blue", Codes: []int64{1}, Pct: true}}, Offline: true}
	tl := Timeline{
		From: 0, To: 3 * minute,
		Online: []steps.Span{{From: 0, To: minute}, {From: 2 * minute, To: 3 * minute}},
		Mode:   []steps.Step{{Value: 1, From: 0, To: minute}, {Value: 1, From: 2 * minute, To: 3 * minute}},
	}
	got := fmt.Sprint(Shares(r, tl))
	if want := "[{Автомат blue 66.7 2} {Нет связи gray 33.3 1}]"; got != want {
		t.Errorf("доли: %s, ждали %s", got, want)
	}
	if Shares(r, Timeline{From: 5, To: 5}) != nil {
		t.Error("пустая смена — нет долей")
	}
}
