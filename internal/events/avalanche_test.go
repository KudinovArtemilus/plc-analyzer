package events

import (
	"testing"
	"time"
)

func TestAvalanche(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	edge := func(sig string, sec int) Edge { return Edge{Bit: Bit{Signal: sig}, On: true, At: at(sec)} }
	a := &Avalanche{Before: 5 * time.Minute, After: 2 * time.Minute, Max: 9 * time.Minute}

	// Лавина: три аварии за 50 с — одно окно, продлевается до последней + 2 мин
	for _, e := range []Edge{edge("AL1", 0), edge("AL2", 20), edge("AL3", 50)} {
		if w := a.Add(e); w != nil {
			t.Fatalf("внутри лавины слепок закрываться не должен: %+v", w)
		}
	}
	if w := a.Due(at(50+120), time.Second); w != nil {
		t.Fatal("рано: последняя точка ещё не дошла (нужен запас settle)")
	}
	w := a.Due(at(50+121), time.Second)
	if w == nil {
		t.Fatal("слепок должен быть готов через 2 мин + 1 с после последней аварии")
	}
	if !w.From.Equal(at(-300)) || !w.To.Equal(at(170)) || len(w.Alarms) != 3 {
		t.Fatalf("окно %v — %v, аварий %d; ожидалось −5 мин — +2:50, 3 аварии", w.From, w.To, len(w.Alarms))
	}
	if a.Due(at(1000), time.Second) != nil {
		t.Fatal("одно окно отдаётся один раз")
	}

	// Авария уже ПОСЛЕ окна — новая лавина, новый слепок
	a.Add(edge("AL1", 1000))
	if w := a.Due(at(1000+121), time.Second); w == nil || len(w.Alarms) != 1 {
		t.Fatalf("отдельная авария — отдельный слепок: %+v", w)
	}
}

func TestAvalancheMax(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	edge := func(sec int) Edge { return Edge{Bit: Bit{Signal: "AL"}, On: true, At: at(sec)} }
	a := &Avalanche{Before: 5 * time.Minute, After: 2 * time.Minute, Max: 9 * time.Minute}

	// Аварии каждые 100 с: окно растёт (−5 мин … +2 мин), пока не упрётся в 9 мин
	a.Add(edge(0))   // окно −300…120 (7 мин)
	a.Add(edge(100)) // −300…220 (≈8,7 мин) — ещё влезает
	done := a.Add(edge(200))
	if done == nil {
		t.Fatal("−300…320 = 10,3 мин > 9 мин — текущий слепок должен закрыться")
	}
	if !done.To.Equal(at(200)) || len(done.Alarms) != 2 {
		t.Fatalf("закрытый слепок: до %v, аварий %d; ожидалось до +200 с, 2 аварии", done.To, len(done.Alarms))
	}
	// Третья авария начала новый слепок
	if w := a.Due(at(200+121), time.Second); w == nil || !w.From.Equal(at(-100)) {
		t.Fatalf("новый слепок должен начинаться за 5 мин до третьей аварии: %+v", w)
	}
}
