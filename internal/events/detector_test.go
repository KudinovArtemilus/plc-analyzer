package events

import (
	"testing"
	"time"
)

// edgesText — фронты строкой, чтобы сравнивать коротко: "AL1+ AL2-"
func edgesText(edges []Edge) string {
	s := ""
	for _, e := range edges {
		if s != "" {
			s += " "
		}
		s += e.Bit.Signal
		if e.On {
			s += "+"
		} else {
			s += "-"
		}
	}
	return s
}

func TestDetector(t *testing.T) {
	bits := []Bit{
		{Index: 1, Signal: "AL1", Kind: "alarm", Message: "Перегрев"},
		{Index: 3, Signal: "AL2", Kind: "warning", Message: "Низкий уровень"},
	}
	// В выборке 4 значения: [0] и [2] — обычные сигналы (их значения детектору безразличны)
	steps := []struct {
		name   string
		values []float64
		want   string
	}{
		{"запуск, аварий нет", []float64{12.5, 0, 99, 0}, ""},
		{"ничего не изменилось", []float64{13, 0, 98, 0}, ""},
		{"пришла AL1", []float64{13, 1, 98, 0}, "AL1+"},
		{"AL1 держится", []float64{14, 1, 97, 0}, ""},
		{"пришла AL2, AL1 держится", []float64{14, 1, 97, 1}, "AL2+"},
		{"обе ушли разом", []float64{15, 0, 96, 0}, "AL1- AL2-"},
		{"снова AL1", []float64{15, 1, 96, 0}, "AL1+"},
	}

	d := NewDetector(bits, nil)
	t0 := time.Date(2026, 10, 23, 8, 0, 0, 0, time.Local)
	for i, st := range steps {
		at := t0.Add(time.Duration(i) * 100 * time.Millisecond)
		got := d.Update(at, st.values)
		if edgesText(got) != st.want {
			t.Fatalf("шаг %d «%s»: фронты %q, ожидалось %q", i, st.name, edgesText(got), st.want)
		}
		for _, e := range got {
			if !e.At.Equal(at) {
				t.Fatalf("шаг %d: время фронта %v, ожидалось %v", i, e.At, at)
			}
		}
	}
}

func TestDetectorAlarmAtStart(t *testing.T) {
	bits := []Bit{{Index: 0, Signal: "AL1"}}
	d := NewDetector(bits, nil)
	// При запуске авария уже есть — сообщаем «пришла», чтобы она попала в журнал и на панель
	if got := edgesText(d.Update(time.Now(), []float64{1})); got != "AL1+" {
		t.Fatalf("авария при запуске: %q, ожидалось AL1+", got)
	}
}

func TestDetectorRestartWithOpenEvent(t *testing.T) {
	bits := []Bit{{Index: 0, Signal: "AL1"}, {Index: 1, Signal: "AL2"}}
	// В базе открыты события по AL1 и AL2 — программу перезапустили во время аварий
	active := map[string]bool{"AL1": true, "AL2": true}
	d := NewDetector(bits, active)

	// AL1 всё ещё 1 — та же авария, нового события нет; AL2 уже 0 — закрываем
	if got := edgesText(d.Update(time.Now(), []float64{1, 0})); got != "AL2-" {
		t.Fatalf("после перезапуска: %q, ожидалось AL2-", got)
	}
}

func TestDetectorShortSample(t *testing.T) {
	// Индекс за пределами выборки (настройки поменялись) — не паникуем, просто пропускаем
	d := NewDetector([]Bit{{Index: 5, Signal: "AL1"}}, nil)
	if got := d.Update(time.Now(), []float64{1, 1}); len(got) != 0 {
		t.Fatalf("короткая выборка: %v", got)
	}
}
