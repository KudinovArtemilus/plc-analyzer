package events

import (
	"testing"
	"time"
)

func f64(x float64) *float64 { return &x }

func TestLimitHiLo(t *testing.T) {
	// Давление: норма 140…180, зона возврата 2
	lim := Limit{Index: 0, Signal: "P", Text: "Давление", Unit: "бар", Kind: "warning",
		Lo: f64(140), Hi: f64(180), Hyst: 2}
	d := NewLimitDetector([]Limit{lim}, nil)

	steps := []struct {
		v    float64
		want string
	}{
		{160, ""},        // запуск в норме — молчим
		{181, "P#hi+"},   // выше 180
		{179, ""},        // ниже 180, но в зоне возврата (> 178) — ещё нарушено
		{185, ""},        // снова выше — то же нарушение, нового события нет
		{178, "P#hi-"},   // вернулось на 2 внутрь — норма
		{139.5, "P#lo+"}, // ниже 140
		{141.9, ""},      // в зоне возврата
		{142, "P#lo-"},   // норма
		{200, "P#hi+"},
	}
	t0 := time.Date(2026, 10, 25, 8, 0, 0, 0, time.Local)
	for i, st := range steps {
		got := edgesText(d.Update(t0.Add(time.Duration(i)*100*time.Millisecond), []float64{st.v}))
		if got != st.want {
			t.Fatalf("шаг %d (%v): %q, ожидалось %q", i, st.v, got, st.want)
		}
	}

	// Текст сообщения — для журнала и панели аварий
	e := NewLimitDetector([]Limit{lim}, nil).Update(t0, []float64{190})
	if len(e) != 1 || e[0].Bit.Message != "Давление: выше 180 бар" || !e[0].Initial || e[0].Bit.Kind != "warning" {
		t.Fatalf("нарушение при запуске: %+v", e)
	}
}

func TestLimitRestart(t *testing.T) {
	// В базе открыто событие «выше нормы» — программу перезапустили во время нарушения
	lim := Limit{Signal: "P", Hi: f64(180)}
	d := NewLimitDetector([]Limit{lim}, map[string]bool{"P#hi": true})
	if got := edgesText(d.Update(time.Now(), []float64{190})); got != "" {
		t.Fatalf("то же нарушение после перезапуска: %q, нового события быть не должно", got)
	}
	if got := edgesText(d.Update(time.Now(), []float64{100})); got != "P#hi-" {
		t.Fatalf("вернулось в норму: %q", got)
	}
}

func TestLimitJump(t *testing.T) {
	// Скачок больше 20 за 1000 мс; опрос раз в 250 мс
	lim := Limit{Signal: "P", Text: "P", Jump: f64(20), JumpMs: 1000, Kind: "alarm"}
	d := NewLimitDetector([]Limit{lim}, nil)
	t0 := int64(1_000_000)
	upd := func(ms int64, v float64) string {
		return edgesText(d.Update(time.UnixMilli(t0+ms), []float64{v}))
	}

	// Медленный рост: 10 за секунду — не скачок
	for i, v := range []float64{100, 102.5, 105, 107.5, 110, 112.5} {
		if got := upd(int64(i)*250, v); got != "" {
			t.Fatalf("медленный рост, точка %d: %q", i, got)
		}
	}
	// Резкий рост на 25 за 250 мс — скачок
	if got := upd(1500, 137.5); got != "P#jump+" {
		t.Fatalf("резкий рост: %q, ожидалось P#jump+", got)
	}
	// Значение держится; через секунду старая точка уходит из окна — скачок закончился
	upd(1750, 137.5)
	upd(2000, 137.5)
	upd(2250, 137.5)
	if got := upd(2500, 137.5); got != "P#jump-" {
		t.Fatalf("через секунду после скачка: %q, ожидалось P#jump-", got)
	}

	// Связь пропадала 5 с: значение до разрыва в окно не попадает — ложного скачка нет
	if got := upd(7500, 200); got != "" {
		t.Fatalf("после разрыва связи: %q, скачка быть не должно", got)
	}
}

func TestLimitEventNames(t *testing.T) {
	l := Limit{Signal: "P", Hi: f64(1), Jump: f64(1), JumpMs: 100}
	got := l.EventNames()
	if len(got) != 2 || got[0] != "P#hi" || got[1] != "P#jump" {
		t.Fatalf("имена событий: %v", got)
	}
}
