package plc

import (
	"testing"
	"time"
)

// mustSig — помощник для тестов: создать сигнал или остановить тест
func mustSig(t *testing.T, name, addr string, typ DataType, p time.Duration) Signal {
	t.Helper() // при ошибке показывать строку теста, а не этой функции
	s, err := NewSignal(name, addr, typ, p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBuildGroups(t *testing.T) {
	fast, slow := 10*time.Millisecond, time.Second
	sigs := []Signal{
		mustSig(t, "a", "DB11.DBW16", Int, fast),
		mustSig(t, "b", "DB11.DBW12", Int, fast),  // не по порядку — отсортируется
		mustSig(t, "c", "DB11.DBW40", Int, fast),  // дыра 22 байта — тот же блок
		mustSig(t, "d", "DB11.DBW238", Int, fast), // дыра 196 байт — новый блок
		mustSig(t, "e", "DB12.DBD0", Real, fast),  // другой DB — новый блок
		mustSig(t, "f", "DB11.DBW240", Int, slow), // другая частота — другая группа
	}

	groups := BuildGroups(sigs, DefaultMaxGap, DefaultMaxSize)
	if len(groups) != 2 {
		t.Fatalf("групп %d, ожидалось 2", len(groups))
	}

	g := groups[0]
	if g.Period != fast {
		t.Fatalf("первая группа %v, ожидалась %v", g.Period, fast)
	}

	want := []struct{ db, start, size, n int }{
		{11, 12, 30, 3}, // DBW12, DBW16, DBW40: байты 12..41
		{11, 238, 2, 1}, // DBW238
		{12, 0, 4, 1},   // DB12.DBD0
	}
	if len(g.Blocks) != len(want) {
		t.Fatalf("блоков %d, ожидалось %d", len(g.Blocks), len(want))
	}
	for i, w := range want {
		b := g.Blocks[i]
		if b.DB != w.db || b.Start != w.start || b.Size != w.size || len(b.Signals) != w.n {
			t.Errorf("блок %d: DB%d start %d size %d сигналов %d, ожидалось DB%d start %d size %d сигналов %d",
				i, b.DB, b.Start, b.Size, len(b.Signals), w.db, w.start, w.size, w.n)
		}
	}

	// Порядок сигналов в группе — по блокам и адресам
	order := ""
	for _, s := range g.Signals {
		order += s.Name
	}
	if order != "bacde" {
		t.Errorf("порядок сигналов %q, ожидался %q", order, "bacde")
	}

	if groups[1].Period != slow || len(groups[1].Blocks) != 1 {
		t.Errorf("медленная группа собрана неверно: %+v", groups[1])
	}
}

func TestBuildGroupsMaxSize(t *testing.T) {
	p := 100 * time.Millisecond
	// Дыры по 48 байт (меньше 64) — блок растёт, пока не упрётся в размер 200
	sigs := []Signal{
		mustSig(t, "a", "DB1.DBW0", Int, p),
		mustSig(t, "b", "DB1.DBW50", Int, p),
		mustSig(t, "c", "DB1.DBW100", Int, p),
		mustSig(t, "d", "DB1.DBW150", Int, p),
		mustSig(t, "e", "DB1.DBW198", Int, p), // конец на 200 — ещё влезает
		mustSig(t, "f", "DB1.DBW200", Int, p), // конец на 202 — уже нет
	}
	g := BuildGroups(sigs, DefaultMaxGap, DefaultMaxSize)[0]
	if len(g.Blocks) != 2 || g.Blocks[0].Size != 200 || g.Blocks[1].Start != 200 {
		t.Errorf("ограничение размера не сработало: %+v", g.Blocks)
	}
}
