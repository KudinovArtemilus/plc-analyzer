package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEvents(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	changes := 0
	st.OnChange = func() { changes++ }

	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	changes = 0 // добавление ПЛК — изменение настроек, его не считаем

	t0 := time.Date(2026, 10, 23, 8, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	on := func(sig string, sec int) EventEdge {
		return EventEdge{Signal: sig, Kind: KindAlarm, Message: "текст " + sig, On: true, At: at(sec)}
	}
	off := func(sig string, sec int) EventEdge { return EventEdge{Signal: sig, At: at(sec)} }

	// Лавина: три аварии в одной выборке
	if err := st.SaveEdges(ctx, plcID, []EventEdge{on("AL1", 0), on("AL2", 0), on("AL3", 0)}); err != nil {
		t.Fatal(err)
	}
	// Повторный «пришла» по уже активной — не создаёт вторую строку
	if err := st.SaveEdges(ctx, plcID, []EventEdge{on("AL1", 5)}); err != nil {
		t.Fatal(err)
	}
	// AL2 ушла
	if err := st.SaveEdges(ctx, plcID, []EventEdge{off("AL2", 10)}); err != nil {
		t.Fatal(err)
	}

	act, err := st.ActiveEvents(ctx, plcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(act) != 2 || act[0].Signal != "AL1" || act[1].Signal != "AL3" || !act[0].Active() {
		t.Fatalf("активные: %+v, ожидались AL1 и AL3", act)
	}
	if !act[0].Came.Equal(at(0)) || act[0].Message != "текст AL1" {
		t.Fatalf("AL1: пришла %v, текст %q", act[0].Came, act[0].Message)
	}

	// AL3 больше не авария (сигнал выключили) — закрыть всё, кроме AL1
	n, err := st.CloseEventsExcept(ctx, plcID, []string{"AL1"}, at(20))
	if err != nil || n != 1 {
		t.Fatalf("CloseEventsExcept: закрыто %d, ошибка %v; ожидалось 1", n, err)
	}

	// Журнал за всё время: 3 события, новые сверху (при равном времени — по id)
	all, err := st.Events(ctx, EventFilter{PLCID: plcID, From: at(-1), To: at(100)})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Signal != "AL3" || all[2].Signal != "AL1" {
		t.Fatalf("журнал: %+v", all)
	}
	for _, e := range all {
		switch e.Signal {
		case "AL1":
			if !e.Active() {
				t.Fatalf("AL1 должна быть активна")
			}
		case "AL2":
			if !e.Went.Equal(at(10)) || e.Went.Sub(e.Came) != 10*time.Second {
				t.Fatalf("AL2: ушла %v, длительность %v", e.Went, e.Went.Sub(e.Came))
			}
		case "AL3":
			if !e.Went.Equal(at(20)) {
				t.Fatalf("AL3 закрыта в %v, ожидалось %v", e.Went, at(20))
			}
		}
	}

	// Фильтр по времени: пришедшие после 1 с — ни одного
	late, _ := st.Events(ctx, EventFilter{PLCID: plcID, From: at(1), To: at(100)})
	if len(late) != 0 {
		t.Fatalf("фильтр по времени: %d событий, ожидалось 0", len(late))
	}

	// Аварии не должны трогать опрос
	if changes != 0 {
		t.Fatalf("OnChange вызван %d раз — события не должны перестраивать опрос", changes)
	}
}
