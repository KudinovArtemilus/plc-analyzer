package main

import (
	"fmt"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Настоящий опрос делит сигналы по PDU своего ПЛК — так же, как план на странице «Сигналы»
func TestBuildGroupsPDU(t *testing.T) {
	var rows []store.SignalRow
	for i := range 40 { // 40 клапанов по 12 байт = 480 байт подряд
		rows = append(rows, store.SignalRow{Name: fmt.Sprintf("Y%d", i+1), Addr: fmt.Sprintf("DB120.DBD%d", i*12+2),
			Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true})
	}
	for pdu, want := range map[int]int{240: 3, 480: 2} {
		g := buildGroups(store.PLC{Name: "Пресс", PDU: pdu}, rows)
		if len(g) != 1 || len(g[0].Blocks) != want {
			t.Errorf("PDU %d: запросов %d, ждали %d", pdu, len(g[0].Blocks), want)
		}
	}
}
