package web

import (
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestCheckSignalAlarmTypes(t *testing.T) {
	row := func(addr, typ string) *store.SignalRow {
		return &store.SignalRow{Name: "E", Addr: addr, Type: typ, PeriodMs: 100,
			Kind: store.KindAlarm, Message: "Привод стола"}
	}
	// Бит и целые числа (код ошибки) — можно
	for _, r := range []*store.SignalRow{row("DB1.DBX0.0", "BOOL"), row("DB1.DBW2", "INT"),
		row("DB1.DBW2", "WORD"), row("DB1.DBD4", "DINT")} {
		if err := checkSignal(r); err != nil {
			t.Errorf("авария %s: %v", r.Type, err)
		}
	}
	// REAL — нельзя
	if err := checkSignal(row("DB1.DBD4", "REAL")); err == nil {
		t.Error("авария REAL должна быть отклонена")
	}
	// Без текста сообщения — нельзя и для кода
	r := row("DB1.DBW2", "INT")
	r.Message = ""
	if err := checkSignal(r); err == nil {
		t.Error("авария без текста должна быть отклонена")
	}
}
