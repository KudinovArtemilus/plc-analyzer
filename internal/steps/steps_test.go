package steps

import (
	"reflect"
	"testing"
)

// series — точки каждые 100 мс начиная с t0: значения по порядку
func series(t0 int64, vals ...float64) ([]int64, []float64) {
	t := make([]int64, len(vals))
	for i := range vals {
		t[i] = t0 + int64(i)*100
	}
	return t, vals
}

func TestJournal(t *testing.T) {
	const gap = 1000
	cases := []struct {
		name string
		t    []int64
		v    []float64
		want []Step
	}{
		{"пусто", nil, nil, nil},
		{
			"один шаг всю запись — обрезан с обеих сторон",
			[]int64{0, 100, 200}, []float64{3, 3, 3},
			[]Step{{Value: 3, From: 0, To: 200, CutStart: true, CutEnd: true}},
		},
		{
			"переходы: первый и последний обрезаны краями, средние — полные",
			[]int64{0, 100, 200, 300, 400, 500}, []float64{3, 3, 10, 10, 12, 12},
			[]Step{
				{Value: 3, From: 0, To: 200, CutStart: true},
				{Value: 10, From: 200, To: 400},
				{Value: 12, From: 400, To: 500, CutEnd: true},
			},
		},
		{
			"дребезг не фильтруется",
			[]int64{0, 100, 200, 300}, []float64{12, 13, 12, 12},
			[]Step{
				{Value: 12, From: 0, To: 100, CutStart: true},
				{Value: 13, From: 100, To: 200},
				{Value: 12, From: 200, To: 300, CutEnd: true},
			},
		},
		{
			// Связь пропала на 5 с: что было — неизвестно. Номер тот же, но это два прохода,
			// оба обрезаны со стороны обрыва
			"обрыв связи режет шаг даже с тем же номером",
			[]int64{0, 100, 200, 5200, 5300, 5400}, []float64{3, 10, 10, 10, 12, 12},
			[]Step{
				{Value: 3, From: 0, To: 100, CutStart: true},
				{Value: 10, From: 100, To: 200, CutEnd: true},
				{Value: 10, From: 5200, To: 5300, CutStart: true},
				{Value: 12, From: 5300, To: 5400, CutEnd: true},
			},
		},
		{
			// Масштаб и погрешность float: 11.9999 — это шаг 12
			"номер округляется",
			[]int64{0, 100}, []float64{11.9999, 12.0001},
			[]Step{{Value: 12, From: 0, To: 100, CutStart: true, CutEnd: true}},
		},
	}
	for _, c := range cases {
		got := Journal(c.t, c.v, gap)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
}

func TestSummary(t *testing.T) {
	// Шаг 3 (исходное) по 1 с, шаг 12 обычно 0.3 с, но один раз завис на 5 с.
	// Края записи — обрезанные проходы шага 3.
	vals := []float64{}
	add := func(step float64, n int) {
		for i := 0; i < n; i++ {
			vals = append(vals, step)
		}
	}
	add(3, 4) // начало записи: обрезан
	add(12, 3)
	add(3, 10)
	add(12, 3)
	add(3, 10)
	add(12, 50) // зависание
	add(3, 10)
	add(12, 3)
	add(3, 6) // конец записи: обрезан
	tm, v := series(0, vals...)
	sum := Summary(Journal(tm, v, 1000))

	// Из-за одного зависания шаг 12 занял больше времени (5.9 с), чем исходный шаг 3 (3.9 с):
	// такой шаг и должен оказаться наверху
	if len(sum) != 2 || sum[0].Value != 12 || sum[1].Value != 3 {
		t.Fatalf("порядок по суммарному времени: %+v", sum)
	}
	s12, s3 := sum[0], sum[1]

	// Шаг 3: 5 проходов, 3 полных по 1 с; два обрезаны краями
	if s3.Count != 5 || s3.Full != 3 || s3.MedianMs != 1000 || s3.MeanMs != 1000 {
		t.Errorf("шаг 3: %+v", s3)
	}
	// Самый долгий проход шага 3 — полный (1 с), обрезанные короче: «≥» не ставим
	if s3.MaxMs != 1000 || s3.MaxCut {
		t.Errorf("шаг 3, максимум: %+v", s3)
	}

	// Шаг 12: 4 прохода — 0.3, 0.3, 5.0, 0.3 с. Медиана 0.3 с, среднее сдвинуто зависанием.
	if s12.Count != 4 || s12.Full != 4 || s12.MedianMs != 300 || s12.MeanMs != (300*3+5000)/4 {
		t.Errorf("шаг 12: %+v", s12)
	}
	if s12.MaxMs != 5000 || s12.MaxAt != tm[4+3+10+3+10] || s12.MaxCut {
		t.Errorf("шаг 12, зависание: %+v", s12)
	}
	if s12.TotalMs != 300*3+5000 {
		t.Errorf("шаг 12, всего: %d", s12.TotalMs)
	}
}

func TestSummaryCutMax(t *testing.T) {
	// Самый долгий проход обрезан концом записи — помечаем «≥», а в медиану он не идёт.
	// Шаг 7: полный проход 200–400 (0.2 с) и обрезанный 600–1100 (0.5 с — последняя точка)
	tm, v := series(0, 5, 5, 7, 7, 5, 5, 7, 7, 7, 7, 7, 7)
	var s7 Stat
	for _, s := range Summary(Journal(tm, v, 1000)) {
		if s.Value == 7 {
			s7 = s
		}
	}
	if s7.Count != 2 || s7.Full != 1 || s7.MedianMs != 200 || s7.MaxMs != 500 || !s7.MaxCut {
		t.Errorf("шаг 7: %+v", s7)
	}
}

func TestMedianEven(t *testing.T) {
	if m := median([]int64{400, 100, 300, 200}); m != 250 {
		t.Errorf("медиана 100,200,300,400 = %d, ждали 250", m)
	}
}

func TestSpansClip(t *testing.T) {
	// Режим: 1 (автомат) 0–1000, 2 (ручной) 1000–1500, 1 снова 1500–3000
	mode := []Step{
		{Value: 1, From: 0, To: 1000, CutStart: true},
		{Value: 2, From: 1000, To: 1500},
		{Value: 1, From: 1500, To: 3000, CutEnd: true},
	}
	auto := Spans(mode, 1)
	if !reflect.DeepEqual(auto, []Span{{0, 1000}, {1500, 3000}}) || Total(auto) != 2500 {
		t.Fatalf("отрезки автомата: %+v, всего %d", auto, Total(auto))
	}

	j := []Step{
		{Value: 10, From: 0, To: 400, CutStart: true}, // целиком в автомате (но начало — край записи)
		{Value: 12, From: 400, To: 800},               // целиком в автомате — полный
		{Value: 14, From: 800, To: 1800},              // через ручной режим — две обрезанные части
		{Value: 3, From: 1800, To: 3000, CutEnd: true},
	}
	got := Clip(j, auto)
	want := []Step{
		{Value: 10, From: 0, To: 400, CutStart: true},
		{Value: 12, From: 400, To: 800},
		{Value: 14, From: 800, To: 1000, CutEnd: true},
		{Value: 14, From: 1500, To: 1800, CutStart: true},
		{Value: 3, From: 1800, To: 3000, CutEnd: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("обрезка:\n got  %+v\n want %+v", got, want)
	}

	// Шаг целиком в ручном режиме — пропадает
	if c := Clip([]Step{{Value: 5, From: 1100, To: 1400}}, auto); len(c) != 0 {
		t.Errorf("шаг в ручном режиме попал в автомат: %+v", c)
	}
	// Нет отрезков (режим ни разу не был выбранным) — пусто
	if c := Clip(j, nil); len(c) != 0 {
		t.Errorf("без отрезков: %+v", c)
	}
}
