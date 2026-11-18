package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// tgFixture — два ПЛК; у первого сигналы режима (INT), бит «авто» (BOOL), давление (REAL)
func tgFixture(t *testing.T) (*Store, context.Context, int64, int64, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "tg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p1, _ := st.AddPLC(ctx, "тест", PLC{Name: "Станок", IP: "192.168.0.1", Slot: 2})
	p2, _ := st.AddPLC(ctx, "тест", PLC{Name: "Пресс", IP: "192.168.0.2", Slot: 1})
	sig := map[string]int64{}
	for _, r := range []SignalRow{
		{PLCID: p1, Name: "Режим", Addr: "DB1.DBW0", Type: "INT"},
		{PLCID: p1, Name: "Авто", Addr: "DB1.DBX2.0", Type: "BOOL"},
		{PLCID: p1, Name: "Давление", Addr: "DB1.DBD4", Type: "REAL"},
		{PLCID: p2, Name: "Чужой", Addr: "DB1.DBW0", Type: "INT"},
	} {
		r.PeriodMs, r.Scale, r.Enabled, r.Kind = 100, 1, true, KindTrend
		id, err := st.AddSignal(ctx, "тест", r)
		if err != nil {
			t.Fatal(err)
		}
		sig[r.Name] = id
	}
	return st, ctx, p1, p2, sig
}

func TestTGMachine(t *testing.T) {
	st, ctx, p1, _, sig := tgFixture(t)

	// Строки ещё нет — выключен, всё показывать: по умолчанию наружу не уходит ничего
	m, err := st.TGMachine(ctx, p1)
	if err != nil || m.Enabled || !m.Show.State || !m.Show.Alarms || !m.AlarmState || !m.OfflineState {
		t.Fatalf("по умолчанию: %+v %v", m, err)
	}
	m.Enabled, m.Label, m.ModeSignalID, m.Show.Step = true, " Шлифовка ", sig["Режим"], false
	if err := st.SaveTGMachine(ctx, "тест", m); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.TGMachine(ctx, p1); !got.Enabled || got.Label != "Шлифовка" || got.ModeSignalID != sig["Режим"] || got.Show.Step {
		t.Errorf("после сохранения: %+v", got)
	}
	// Сохранить ещё раз — обновление, а не вторая строка
	m.Label = "Шлифовка 2"
	if err := st.SaveTGMachine(ctx, "тест", m); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.TGMachine(ctx, p1); got.Label != "Шлифовка 2" {
		t.Errorf("обновление: %q", got.Label)
	}
	// Сигнал режима чужого ПЛК и слишком длинная подпись — нельзя
	bad := m
	bad.ModeSignalID = sig["Чужой"]
	if err := st.SaveTGMachine(ctx, "тест", bad); !errors.Is(err, ErrTGSignal) {
		t.Errorf("чужой сигнал режима: %v", err)
	}
	bad = m
	bad.Label = strings.Repeat("я", TGNameMax+1)
	if err := st.SaveTGMachine(ctx, "тест", bad); !errors.Is(err, ErrTGName) {
		t.Errorf("длинная подпись: %v", err)
	}
}

func TestTGStates(t *testing.T) {
	st, ctx, p1, _, sig := tgFixture(t)

	auto, err := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Автомат", Color: "blue", Pct: true})
	if err != nil {
		t.Fatal(err)
	}
	manual, _ := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Ручной", Color: "steel", Pct: true})

	// Красный и жёлтый — только для аварий; повтор названия; бит — только BOOL этого ПЛК
	if _, err := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Тревога", Color: "red"}); !errors.Is(err, ErrTGColor) {
		t.Errorf("красный: %v", err)
	}
	if _, err := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Автомат", Color: "green"}); err == nil {
		t.Error("повтор названия должен быть отклонён")
	}
	for _, s := range []string{"Давление", "Чужой"} {
		if _, err := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "По " + s, Color: "green", BitSignalID: sig[s]}); !errors.Is(err, ErrTGBit) {
			t.Errorf("бит по сигналу «%s»: %v", s, err)
		}
	}
	bit, err := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Авто (бит)", Color: "green", BitSignalID: sig["Авто"]})
	if err != nil {
		t.Fatal(err)
	}

	// Значения режима: 1 и 2 — в «Автомат»; 2 в «Ручной» — ПЕРЕЕЗЖАЕТ, а не задваивается
	st.SetTGStateCode(ctx, "тест", p1, auto, 1, true)
	st.SetTGStateCode(ctx, "тест", p1, auto, 2, true)
	if err := st.SetTGStateCode(ctx, "тест", p1, manual, 2, true); err != nil {
		t.Fatal(err)
	}
	codes := func() string {
		list, _ := st.TGStates(ctx, p1)
		var out []string
		for _, s := range list {
			out = append(out, fmt.Sprintf("%s%v", s.Name, s.Codes))
		}
		return strings.Join(out, " ")
	}
	if got := codes(); got != "Автомат[1] Ручной[2] Авто (бит)[]" {
		t.Errorf("после переезда кода: %s", got)
	}
	// Значения — не у состояния «по биту»
	if err := st.SetTGStateCode(ctx, "тест", p1, bit, 3, true); !errors.Is(err, ErrTGCodes) {
		t.Errorf("код у состояния по биту: %v", err)
	}
	// Убрать значение
	st.SetTGStateCode(ctx, "тест", p1, auto, 1, false)
	if got := codes(); got != "Автомат[] Ручной[2] Авто (бит)[]" {
		t.Errorf("после «убрать»: %s", got)
	}

	// Порядок: «Авто (бит)» на самый верх
	st.MoveTGState(ctx, "тест", p1, bit, -1)
	st.MoveTGState(ctx, "тест", p1, bit, -1)
	if got := codes(); !strings.HasPrefix(got, "Авто (бит)") {
		t.Errorf("порядок: %s", got)
	}

	// Ручной стал «по биту» — его значения режима снимаются
	list, _ := st.TGStates(ctx, p1)
	var m TGState
	for _, s := range list {
		if s.ID == manual {
			m = s
		}
	}
	m.BitSignalID, m.Color = sig["Авто"], "orange"
	if err := st.UpdateTGState(ctx, "тест", m); err != nil {
		t.Fatal(err)
	}
	if got := codes(); strings.Contains(got, "Ручной[2]") {
		t.Errorf("у состояния по биту остались значения: %s", got)
	}

	// Удалить состояние
	if err := st.DeleteTGState(ctx, "тест", p1, auto); err != nil {
		t.Fatal(err)
	}
	if got := codes(); strings.Contains(got, "Автомат") {
		t.Errorf("удалённое состояние: %s", got)
	}
}

func TestTGSignals(t *testing.T) {
	st, ctx, p1, _, sig := tgFixture(t)

	for _, n := range []string{"Давление", "Режим"} {
		if err := st.AddTGSignal(ctx, "тест", p1, sig[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddTGSignal(ctx, "тест", p1, sig["Чужой"]); !errors.Is(err, ErrTGSignal) {
		t.Errorf("сигнал чужого ПЛК: %v", err)
	}
	if err := st.AddTGSignal(ctx, "тест", p1, sig["Давление"]); err == nil {
		t.Error("тот же сигнал дважды")
	}
	list, _ := st.TGSignals(ctx, p1)
	if len(list) != 2 || list[0].Name != "Давление" || list[0].Label != "Давление" || list[0].How != "value" || list[0].Type != "REAL" {
		t.Fatalf("сигналы: %+v", list)
	}

	g := list[0]
	g.How, g.Label = "minmax", "Давление, бар"
	if err := st.UpdateTGSignal(ctx, "тест", g); err != nil {
		t.Fatal(err)
	}
	g.How = "chart"
	if err := st.UpdateTGSignal(ctx, "тест", g); !errors.Is(err, ErrTGHow) {
		t.Errorf("неизвестный показ: %v", err)
	}
	st.MoveTGSignal(ctx, "тест", p1, sig["Режим"], -1)
	list, _ = st.TGSignals(ctx, p1)
	if list[0].Name != "Режим" || list[1].Label != "Давление, бар" || list[1].How != "minmax" {
		t.Errorf("после правки и порядка: %+v", list)
	}
	if err := st.DeleteTGSignal(ctx, "тест", p1, sig["Режим"]); err != nil {
		t.Fatal(err)
	}
	if list, _ = st.TGSignals(ctx, p1); len(list) != 1 {
		t.Errorf("после удаления: %+v", list)
	}
}

// Удалили сигнал или ПЛК — настройки Telegram уходят вместе с ними
func TestTGCascade(t *testing.T) {
	st, ctx, p1, p2, sig := tgFixture(t)
	st.SaveTGMachine(ctx, "тест", TGMachine{PLCID: p1, Enabled: true, ModeSignalID: sig["Режим"]})
	st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Авто (бит)", Color: "green", BitSignalID: sig["Авто"]})
	a, _ := st.AddTGState(ctx, "тест", TGState{PLCID: p1, Name: "Автомат", Color: "blue"})
	st.SetTGStateCode(ctx, "тест", p1, a, 1, true)
	st.AddTGSignal(ctx, "тест", p1, sig["Давление"])

	for _, n := range []string{"Режим", "Авто", "Давление"} {
		row, _ := st.Signal(ctx, sig[n])
		if err := st.DeleteSignal(ctx, "тест", row); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := st.TGMachine(ctx, p1)
	states, _ := st.TGStates(ctx, p1)
	sigs, _ := st.TGSignals(ctx, p1)
	if m.ModeSignalID != 0 || len(states) != 1 || states[0].Name != "Автомат" || len(sigs) != 0 {
		t.Errorf("после удаления сигналов: режим %d, состояния %+v, сигналы %+v", m.ModeSignalID, states, sigs)
	}

	if _, err := st.DeletePLC(ctx, "тест", PLC{ID: p1}); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, table := range []string{"tg_machines", "tg_states", "tg_state_codes", "tg_signals"} {
		st.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("после удаления ПЛК в %s осталось %d", table, n)
		}
	}
	_ = p2
}

func TestTGPeriod(t *testing.T) {
	st, ctx, _, _, _ := tgFixture(t)
	if p, _ := st.TGPeriod(ctx); p != 60 {
		t.Errorf("по умолчанию: %d", p)
	}
	if err := st.SetTGPeriod(ctx, "тест", 120); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.TGPeriod(ctx); p != 120 {
		t.Errorf("после установки: %d", p)
	}
	for _, bad := range []int{10, 30, 45} { // чаще минуты Telegram не даёт
		if err := st.SetTGPeriod(ctx, "тест", bad); !errors.Is(err, ErrTGPeriod) {
			t.Errorf("%d с: %v", bad, err)
		}
	}
	// Старое значение 10 с из базы (до этого исправления) читается как минута
	st.SetSetting(ctx, "тест", tgPeriodKey, "10", "")
	if p, _ := st.TGPeriod(ctx); p != 60 {
		t.Errorf("старые 10 с: %d", p)
	}
}
