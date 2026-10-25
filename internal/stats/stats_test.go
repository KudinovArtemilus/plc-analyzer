package stats

import "testing"

func TestAnalyze(t *testing.T) {
	ts := []int64{1000, 1100, 1200, 1300, 1400, 1500}
	vs := []float64{5, 2, 9, 2, 9, 100}

	// Участок 1100…1400: точки 2, 9, 2, 9 (крайние 5 и 100 — вне участка)
	a := Analyze(ts, vs, 1100, 1400)
	if a.N != 4 || a.Min != 2 || a.Max != 9 || a.Mean != 5.5 {
		t.Fatalf("сводка: %+v", a)
	}
	// Минимум и максимум встречаются дважды — запоминаем первый раз
	if a.MinAt != 1100 || a.MaxAt != 1200 {
		t.Fatalf("время мин %d (ожидалось 1100), макс %d (ожидалось 1200)", a.MinAt, a.MaxAt)
	}

	// Участок между точками — данных нет
	if e := Analyze(ts, vs, 1110, 1190); e.N != 0 {
		t.Fatalf("пустой участок: %+v", e)
	}
}

func TestBits(t *testing.T) {
	// Опрос раз в 100 мс: 0 0 1 1 1 0 1 0
	ts := []int64{0, 100, 200, 300, 400, 500, 600, 700}
	vs := []float64{0, 0, 1, 1, 1, 0, 1, 0}
	b := Bits(ts, vs, 0, 800, 300)

	if b.N != 8 || b.Rises != 2 {
		t.Fatalf("точек %d, включений %d; ожидалось 8 и 2", b.N, b.Rises)
	}
	// «1» с 200 до 500 (300 мс) и с 600 до 700 (100 мс)
	if b.OnMs != 400 || b.CoveredMs != 800 {
		t.Fatalf("в «1» %d мс, с данными %d мс; ожидалось 400 и 800", b.OnMs, b.CoveredMs)
	}
	if b.LongestMs != 300 || b.LongestAt != 200 || b.LongestCut {
		t.Fatalf("самое долгое: %d мс с %d, обрезано %v; ожидалось 300 с 200, не обрезано",
			b.LongestMs, b.LongestAt, b.LongestCut)
	}
}

func TestBitsEdges(t *testing.T) {
	ts := []int64{0, 100, 200, 300}
	vs := []float64{1, 1, 0, 1}
	b := Bits(ts, vs, 0, 500, 300)

	// Первая точка участка уже «1» — это не включение: что было до неё, не видно
	if b.Rises != 1 {
		t.Fatalf("включений %d, ожидалось 1", b.Rises)
	}
	// «1» с начала участка: 200 мс, но могло начаться раньше — помечено обрезанным
	// «1» в конце: с 300 до 500 — тоже 200 мс, тоже обрезано; самым долгим остаётся первое
	if b.LongestMs != 200 || b.LongestAt != 0 || !b.LongestCut {
		t.Fatalf("самое долгое: %+v", b)
	}
	if b.OnMs != 400 {
		t.Fatalf("в «1» %d мс, ожидалось 400", b.OnMs)
	}
}

func TestBitsGap(t *testing.T) {
	// Связь пропала на 5 секунд, пока бит был «1»
	ts := []int64{0, 100, 5100, 5200}
	vs := []float64{0, 1, 1, 0}
	b := Bits(ts, vs, 0, 5300, 300)

	// Время разрыва не считаем ни в «1», ни в «есть данные»: точка 100 держится только 300 мс
	if b.OnMs != 300+100 {
		t.Fatalf("в «1» %d мс, ожидалось 400 (300 до разрыва и 100 после)", b.OnMs)
	}
	if b.CoveredMs != 100+300+100+100 {
		t.Fatalf("с данными %d мс, ожидалось 600", b.CoveredMs)
	}
	// Разрыв рвёт «1» на два куска; включение одно — после разрыва бит уже был «1»
	if b.Rises != 1 || b.LongestMs != 300 || b.LongestAt != 100 {
		t.Fatalf("включений %d, самое долгое %d мс с %d", b.Rises, b.LongestMs, b.LongestAt)
	}
}

func TestBitsEmpty(t *testing.T) {
	if b := Bits(nil, nil, 0, 1000, 300); b.N != 0 || b.OnMs != 0 {
		t.Fatalf("без точек: %+v", b)
	}
}
