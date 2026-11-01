package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSnapshotMarks(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plc, _ := st.AddPLC(ctx, "тест", PLC{Name: "p1", IP: "192.168.0.1", Slot: 2})

	// Слепок по аварии сразу с отметками; табуляция в подписи не ломает хранение
	_, err = st.AddSnapshot(ctx, "система", Snapshot{PLCID: plc, Name: "Авария", Category: "авария",
		File: "snapshots/A_1.pla", FromMs: 1000, ToMs: 9000, Signals: []string{"P"},
		Marks: []Mark{{Ms: 5000, Text: "EStop — Аварийный\tостанов"}}})
	if err != nil {
		t.Fatal(err)
	}
	sn, err := st.SnapshotByFile(ctx, plc, "snapshots/A_1.pla")
	if err != nil || len(sn.Marks) != 1 || sn.Marks[0].Ms != 5000 || sn.Marks[0].Text != "EStop — Аварийный останов" {
		t.Fatalf("отметки слепка: %+v, %v", sn.Marks, err)
	}

	// Отметка вручную дописывается к существующим
	if err := st.AddMark(ctx, "веб 127.0.0.1", plc, "snapshots/A_1.pla", Mark{Ms: 4200, Text: "давление начало падать"}); err != nil {
		t.Fatal(err)
	}
	list, _ := st.Snapshots(ctx, plc, "")
	if len(list) != 1 || len(list[0].Marks) != 2 || list[0].Marks[1].Text != "давление начало падать" {
		t.Fatalf("после отметки: %+v", list)
	}

	// Слепка нет — ErrNotFound
	if err := st.AddMark(ctx, "т", plc, "snapshots/нет.pla", Mark{Ms: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("нет слепка: %v", err)
	}
	if _, err := st.SnapshotByFile(ctx, plc, "snapshots/нет.pla"); !errors.Is(err, ErrNotFound) {
		t.Errorf("нет слепка (чтение): %v", err)
	}
}
