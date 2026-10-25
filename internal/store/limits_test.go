package store

import (
	"context"
	"path/filepath"
	"testing"
)

func f64(x float64) *float64 { return &x }

func TestLimits(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	id, _ := st.AddSignal(ctx, "тест", SignalRow{PLCID: plcID, Name: "P", Addr: "DB1.DBD0",
		Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true})
	sig, _ := st.Signal(ctx, id)

	changes := 0
	st.OnChange = func() { changes++ }

	// Проверки
	bad := []Limit{
		{Lo: f64(10), Hi: f64(5), Kind: KindWarning},          // низ выше верха
		{Hi: f64(5), Kind: "trend"},                           // не тот тип
		{Lo: f64(0), Hi: f64(10), Hyst: 6, Kind: KindWarning}, // возврат больше половины
		{Jump: f64(5), JumpMs: 0, Kind: KindAlarm},            // скачок без времени
		{Jump: f64(-1), JumpMs: 1000, Kind: KindAlarm},        // отрицательный скачок
	}
	for i, l := range bad {
		if err := st.SetLimit(ctx, "тест", sig, l); err == nil {
			t.Errorf("нормы %d должны быть отклонены: %+v", i, l)
		}
	}

	// Только верхняя граница; нижней нет — в базе NULL, при чтении nil
	if err := st.SetLimit(ctx, "тест", sig, Limit{Hi: f64(180), Hyst: 2, Kind: KindWarning}); err != nil {
		t.Fatal(err)
	}
	m, err := st.Limits(ctx, plcID)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := m[id]
	if !ok || l.Lo != nil || l.Hi == nil || *l.Hi != 180 || l.Hyst != 2 || l.Jump != nil {
		t.Fatalf("прочитано: %+v", l)
	}

	// Повторная запись — обновление той же строки (UPSERT), а не вторая строка
	if err := st.SetLimit(ctx, "тест", sig, Limit{Lo: f64(140), Hi: f64(180),
		Jump: f64(20), JumpMs: 1000, Kind: KindAlarm}); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Limits(ctx, plcID)
	if l = m[id]; len(m) != 1 || *l.Lo != 140 || *l.Jump != 20 || l.Kind != KindAlarm {
		t.Fatalf("после обновления: %+v", m)
	}
	if changes != 2 {
		t.Fatalf("опрос должен перестроиться 2 раза, а не %d", changes)
	}

	// Пустые нормы — то же, что удалить
	if err := st.SetLimit(ctx, "тест", sig, Limit{Kind: KindWarning}); err != nil {
		t.Fatal(err)
	}
	if m, _ = st.Limits(ctx, plcID); len(m) != 0 {
		t.Fatalf("после снятия норм осталось: %+v", m)
	}
	// Снять ещё раз — не ошибка
	if err := st.DeleteLimit(ctx, "тест", sig); err != nil {
		t.Fatalf("повторное снятие: %v", err)
	}

	// Удалили сигнал — нормы удалились вместе с ним (ON DELETE CASCADE)
	st.SetLimit(ctx, "тест", sig, Limit{Hi: f64(1), Kind: KindWarning})
	if err := st.DeleteSignal(ctx, "тест", sig); err != nil {
		t.Fatal(err)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM limits`).Scan(&n)
	if n != 0 {
		t.Fatalf("после удаления сигнала норм осталось %d", n)
	}
}

func TestDescribeLimit(t *testing.T) {
	cases := []struct {
		l    Limit
		want string
	}{
		{Limit{Lo: f64(140), Hi: f64(180), Hyst: 2, Kind: KindAlarm}, "140…180 бар, возврат 2 · авария"},
		{Limit{Hi: f64(85.5), Kind: KindWarning}, "не выше 85.5 бар · предупреждение"},
		{Limit{Jump: f64(20), JumpMs: 1000, Kind: KindWarning}, "скачок > 20 бар за 1000 мс · предупреждение"},
		{Limit{}, "нет"},
	}
	for _, c := range cases {
		if got := DescribeLimit(c.l, "бар"); got != c.want {
			t.Errorf("%q, ожидалось %q", got, c.want)
		}
	}
}
