package cycles

import (
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// chain — журнал из пар (шаг, длительность мс), проходы идут подряд с момента 0
func chain(pairs ...int64) []steps.Step {
	var j []steps.Step
	var t int64
	for i := 0; i+1 < len(pairs); i += 2 {
		j = append(j, steps.Step{Value: pairs[i], From: t, To: t + pairs[i+1]})
		t += pairs[i+1]
	}
	j[0].CutStart = true // запись началась посреди первого шага
	j[len(j)-1].CutEnd = true
	return j
}

func TestSplit(t *testing.T) {
	// Исходный шаг 0. Три цикла 10 → 12 → 14; во втором шаг 12 затянулся (5 с вместо 1 с)
	j := chain(
		0, 3000, // ожидание, обрезано началом записи
		10, 2000, 12, 1000, 14, 4000, // цикл 1: 7 с
		0, 20000, // ожидание 20 с
		10, 2000, 12, 5000, 14, 4000, // цикл 2: 11 с, шаг 12 затянулся
		0, 1000,
		10, 2000, 12, 1000, 14, 4000, // цикл 3: 7 с
		0, 500, // конец записи
	)
	cs, inc := Split(j, 0)
	if len(cs) != 3 || inc != 0 {
		t.Fatalf("циклов %d (неполных %d), ждали 3: %+v", len(cs), inc, cs)
	}
	if cs[0].Dur() != 7000 || cs[0].Passes != 3 || cs[0].IdleMs != 3000 || !cs[0].IdleCut {
		t.Errorf("цикл 1: %+v", cs[0])
	}
	c2 := cs[1]
	if c2.Dur() != 11000 || c2.IdleMs != 20000 || c2.IdleCut {
		t.Errorf("цикл 2: %+v", c2)
	}
	// Медиана шага 12 — 1 с (три прохода: 1, 5, 1); в цикле 2 он стоял 5 с
	if c2.SlowStep != 12 || c2.SlowMs != 5000 || c2.SlowMedianMs != 1000 || c2.SlowAt != c2.From+2000 {
		t.Errorf("затянувшийся шаг цикла 2: %+v", c2)
	}

	st := Summarize(cs, inc)
	if st.Count != 3 || st.MedianMs != 7000 || st.MinMs != 7000 || st.MaxMs != 11000 || st.MeanMs != 25000/3 {
		t.Errorf("сводка: %+v", st)
	}
	if st.WorkMs != 25000 || st.IdleMs != 3000+20000+1000 {
		t.Errorf("работа и ожидание: %+v", st)
	}

	if l := Longest(cs, 2); len(l) != 2 || l[0].Dur() != 11000 || cs[0].Dur() != 7000 {
		t.Errorf("самые долгие (и исходный порядок не испорчен): %+v / %+v", l, cs[0])
	}
}

func TestSplitIncomplete(t *testing.T) {
	// Обрыв связи посреди цикла (шаг 12 обрезан) — цикл неполный, в сводку не идёт
	j := chain(0, 1000, 10, 2000, 12, 1000, 14, 4000, 0, 1000, 10, 2000, 12, 1000, 0, 1000)
	j[5].CutEnd = true // обрыв в конце второго прохода шага 12
	j[6].CutStart = true
	cs, inc := Split(j, 0)
	if len(cs) != 1 || inc != 1 {
		t.Fatalf("полных %d, неполных %d; ждали 1 и 1", len(cs), inc)
	}

	// Подряд два прохода исходного шага (связь оборвалась в нём) — цикла между ними нет
	j = chain(0, 1000, 0, 1000, 10, 2000, 0, 1000)
	if cs, inc := Split(j, 0); len(cs) != 1 || inc != 0 {
		t.Errorf("два исходных подряд: полных %d, неполных %d", len(cs), inc)
	}

	// Исходный шаг не встретился — циклов нет, и это не ошибка
	if cs, inc := Split(chain(10, 1000, 12, 1000), 0); len(cs) != 0 || inc != 0 {
		t.Errorf("без исходного шага: %d, %d", len(cs), inc)
	}
	if st := Summarize(nil, 0); st.Count != 0 || st.MedianMs != 0 {
		t.Errorf("пустая сводка: %+v", st)
	}
}

func TestSuggestHome(t *testing.T) {
	sum := []steps.Stat{
		{Value: 554, Count: 1, TotalMs: 900_000}, // больше всего времени, но был один раз — не исходный
		{Value: 0, Count: 25, TotalMs: 500_000},
		{Value: 7, Count: 25, TotalMs: 100_000},
	}
	if h, ok := SuggestHome(sum); !ok || h != 0 {
		t.Errorf("кандидат: %d, %v; ждали 0", h, ok)
	}
	if _, ok := SuggestHome([]steps.Stat{{Value: 3, Count: 1}}); ok {
		t.Error("один проход — кандидата быть не должно")
	}
}
