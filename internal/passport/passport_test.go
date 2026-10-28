package passport

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

// rec — запись смены для тестов: одна группа 100 мс, сигналы Mode, Step, P, Valve
type rec struct {
	f *archive.File
	t int64
}

func newRec() *rec {
	return &rec{f: &archive.File{
		Header: archive.Header{Groups: []archive.GroupInfo{{PeriodMs: 100, Signals: []archive.SignalInfo{
			{Name: "Mode", Type: "INT"}, {Name: "Step", Type: "INT"},
			{Name: "P", Type: "REAL"}, {Name: "Valve", Type: "BOOL"},
		}}}},
		Groups: []archive.GroupData{{Values: make([][]float64, 4)}},
	}, t: 1_000_000}
}

// seg — n точек подряд: режим, шаг, давление; клапан включён на первых valveOn точках
func (r *rec) seg(n int, mode, step, p float64, valveOn int) {
	g := &r.f.Groups[0]
	for i := 0; i < n; i++ {
		v := 0.0
		if i < valveOn {
			v = 1
		}
		g.Times = append(g.Times, r.t)
		for k, x := range []float64{mode, step, p, v} {
			g.Values[k] = append(g.Values[k], x)
		}
		r.t += 100
	}
}

func shift() *rec {
	r := newRec()
	r.seg(10, 1, 0, 10, 0) // ожидание в исходном шаге, начало записи — обрезано
	// цикл 1: 7 с
	r.seg(20, 1, 10, 10, 5)
	r.seg(10, 1, 12, 100, 0)
	r.seg(40, 1, 14, 10, 5)
	r.seg(20, 1, 0, 10, 0) // ожидание 2 с
	// цикл 2: 9 с, шаг 12 затянулся (3 с вместо обычных)
	r.seg(20, 1, 10, 10, 5)
	r.seg(30, 1, 12, 120, 0)
	r.seg(40, 1, 14, 10, 5)
	r.seg(10, 1, 0, 10, 0)
	// цикл 3: обрыв связи посреди шага 12 — неполный
	r.seg(20, 1, 10, 10, 0)
	r.seg(5, 1, 12, 100, 0)
	r.t += 2000
	r.seg(5, 1, 12, 100, 0)
	r.seg(40, 1, 14, 10, 0)
	r.seg(10, 1, 0, 10, 0)
	// наладка: в автомате не считается
	r.seg(50, 2, 10, 500, 3)
	return r
}

func TestBuild(t *testing.T) {
	tb, err := Build(shift().f, Config{Step: "Step", Mode: "Mode", ModeVal: 1, Home: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Rows) != 2 || tb.Incomplete != 1 {
		t.Fatalf("циклов %d (неполных %d), ждали 2 и 1", len(tb.Rows), tb.Incomplete)
	}
	if strings.Join(tb.Num, ",") != "P" || strings.Join(tb.Bits, ",") != "Valve" {
		t.Errorf("сигналы: числа %v, биты %v — шаг и режим в признаки не идут", tb.Num, tb.Bits)
	}
	if len(tb.Steps) != 3 || tb.Steps[0] != 10 || tb.Steps[2] != 14 {
		t.Errorf("шаги-столбцы: %v", tb.Steps)
	}

	c1 := tb.Rows[0]
	if c1.Dur() != 7000 || c1.IdleMs != 1000 || !c1.IdleCut {
		t.Errorf("цикл 1: %+v", c1.Cycle)
	}
	if c1.StepMs[10] != 2000 || c1.StepMs[12] != 1000 || c1.StepMs[14] != 4000 {
		t.Errorf("время шагов цикла 1: %v", c1.StepMs)
	}
	if p := c1.Num["P"]; !p.OK || p.Min != 10 || p.Max != 100 || p.Mean != 1600.0/70 {
		t.Errorf("давление в цикле 1: %+v", p)
	}
	if c1.Ons["Valve"] != 2 {
		t.Errorf("клапан в цикле 1 включался %d раз, ждали 2", c1.Ons["Valve"])
	}

	c2 := tb.Rows[1]
	if c2.Dur() != 9000 || c2.IdleMs != 2000 || c2.IdleCut || c2.Num["P"].Max != 120 {
		t.Errorf("цикл 2: %+v, P %+v", c2.Cycle, c2.Num["P"])
	}
	if c2.SlowStep != 12 || c2.SlowMs != 3000 {
		t.Errorf("затянувшийся шаг цикла 2: %d, %d мс", c2.SlowStep, c2.SlowMs)
	}
}

func TestBuildWithoutMode(t *testing.T) {
	// Без режима наладка тоже попадает в журнал, но цикла там нет — в исходный шаг не вернулись
	tb, err := Build(shift().f, Config{Step: "Step", Home: 0})
	if err != nil || len(tb.Rows) != 2 {
		t.Fatalf("без режима: %v, циклов %d", err, len(tb.Rows))
	}
	if len(tb.Num) != 2 { // теперь Mode — обычный числовой сигнал
		t.Errorf("числовые сигналы: %v", tb.Num)
	}
}

func TestBuildNoSignal(t *testing.T) {
	_, err := Build(shift().f, Config{Step: "Шаг_старый"})
	if !errors.Is(err, ErrNoSignal) || !strings.Contains(err.Error(), "Шаг_старый") {
		t.Errorf("ошибка: %v", err)
	}
	_, err = Build(shift().f, Config{Step: "Step", Mode: "Нет"})
	if !errors.Is(err, ErrNoSignal) {
		t.Errorf("нет режима: %v", err)
	}
}

func TestWriteCSV(t *testing.T) {
	tb, _ := Build(shift().f, Config{Step: "Step", Mode: "Mode", ModeVal: 1, Home: 0})
	tb.Rows[0].Num["P"] = Agg{} // у сигнала не было точек — пустые ячейки, не нули
	var b bytes.Buffer
	if err := tb.WriteCSV(&b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("строк %d, ждали заголовок + 2 цикла:\n%s", len(lines), b.String())
	}
	want := "start;cycle_s;idle_s;idle_cut;passes;slow_step;slow_s;step_10_s;step_12_s;step_14_s;P.min;P.max;P.mean;Valve.on"
	if lines[0] != want {
		t.Errorf("заголовок:\n%s\nждали\n%s", lines[0], want)
	}
	// время — местное, поэтому сравниваем всё, кроме него
	if got := lines[1][strings.Index(lines[1], ";"):]; got != ";7;1;1;3;10;2;2;1;4;;;;2" {
		t.Errorf("цикл 1: %s", got)
	}
	if got := lines[2][strings.Index(lines[2], ";"):]; got != ";9;2;0;3;12;3;2;3;4;10;120;46,6667;2" {
		t.Errorf("цикл 2: %s", got)
	}
}
