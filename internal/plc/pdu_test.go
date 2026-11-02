package plc

import "testing"

func TestMaxBlock(t *testing.T) {
	// 0 — не указан, 960 — больше gos7 не просит, поэтому как 480
	for pdu, want := range map[int]int{0: 200, 240: 200, 480: 440, 960: 440} {
		if got := MaxBlock(pdu); got != want {
			t.Errorf("MaxBlock(%d) = %d, ждали %d", pdu, got, want)
		}
	}
	if MaxBlock(PDUDefault) != DefaultMaxSize {
		t.Error("при PDU 240 размер запроса должен остаться прежним — 200 байт")
	}
}
