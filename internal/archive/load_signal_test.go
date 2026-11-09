package archive

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// refLoad — чтение, как было до шага 43 (всё через ReadAll). Новое обязано совпасть с ним.
func refLoad(path string) (*File, error) {
	h, chunks, err := ReadAll(path)
	damaged := errors.Is(err, ErrDamaged)
	if err != nil && (!damaged || len(h.Groups) == 0) {
		return nil, err
	}
	f := &File{Header: h, Groups: make([]GroupData, len(h.Groups)), Damaged: damaged}
	for gi, g := range h.Groups {
		f.Groups[gi].Values = make([][]float64, len(g.Signals))
	}
	for _, c := range chunks {
		gd := &f.Groups[c.Group]
		gd.Times = append(gd.Times, c.Times...)
		for s := range c.Values {
			gd.Values[s] = append(gd.Values[s], c.Values[s]...)
		}
	}
	return f, nil
}

// twoGroups — файл: «быстрая» группа 100 мс (a, b, c) и «медленная» 1 с (x, y), порции вперемешку
func twoGroups(t *testing.T, path string) {
	t.Helper()
	w, err := Create(path, Header{PLC: "Т", Shift: "A", ShiftDate: "2026-11-09", Groups: []GroupInfo{
		{PeriodMs: 100, Signals: []SignalInfo{{Name: "a", Type: "REAL"}, {Name: "b", Type: "BOOL"}, {Name: "c", Type: "INT"}}},
		{PeriodMs: 1000, Signals: []SignalInfo{{Name: "x", Type: "REAL"}, {Name: "y", Type: "INT"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t0 := int64(1_762_675_200_000)
	for p := range 5 {
		fast := Chunk{Group: 0, Values: make([][]float64, 3)}
		for k := range 20 {
			i := p*20 + k
			fast.Times = append(fast.Times, t0+int64(i)*100)
			fast.Values[0] = append(fast.Values[0], math.Sin(float64(i)/7)*10)
			fast.Values[1] = append(fast.Values[1], float64(i/13%2))
			fast.Values[2] = append(fast.Values[2], float64(i/30))
		}
		slow := Chunk{Group: 1, Times: []int64{t0 + int64(p)*2000, t0 + int64(p)*2000 + 1000},
			Values: [][]float64{{1.5 * float64(p), 2.5 * float64(p)}, {float64(p), float64(p + 1)}}}
		w.WriteChunk(fast)
		w.WriteChunk(slow)
	}
	w.Close()
}

// same — LoadSignals совпадает со старым чтением: заголовок, обрыв, времена ВСЕХ групп,
// значения загруженных сигналов; незагруженные — nil
func same(t *testing.T, got, ref *File, names []string) {
	t.Helper()
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	if !reflect.DeepEqual(got.Header, ref.Header) || got.Damaged != ref.Damaged {
		t.Fatalf("%v: заголовок или «оборван» не совпали", names)
	}
	for gi, g := range ref.Groups {
		if !reflect.DeepEqual(got.Groups[gi].Times, g.Times) {
			t.Errorf("%v: группа %d: времена не совпали", names, gi)
		}
		for si, sg := range ref.Header.Groups[gi].Signals {
			v := got.Groups[gi].Values[si]
			switch {
			case want[sg.Name] && !reflect.DeepEqual(v, g.Values[si]) && len(v)+len(g.Values[si]) > 0:
				t.Errorf("%v: %s: значения не совпали", names, sg.Name)
			case want[sg.Name] && !got.Has(gi, si):
				t.Errorf("%v: %s должен быть загружен", names, sg.Name)
			case !want[sg.Name] && v != nil:
				t.Errorf("%v: %s не просили, а он загружен", names, sg.Name)
			}
		}
	}
}

func TestLoadSignalsSameAsBefore(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.pla")
	twoGroups(t, full)

	// Оборванный файл: отключили питание посреди записи порции
	b, _ := os.ReadFile(full)
	cut := filepath.Join(dir, "cut.pla")
	os.WriteFile(cut, b[:len(b)-7], 0o644)

	for _, path := range []string{full, cut} {
		ref, err := refLoad(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, names := range [][]string{
			nil,                       // только времена — для выбора сигналов
			{"a"},                     // один из быстрой группы
			{"b", "y"},                // из обеих групп
			{"a", "b", "c", "x", "y"}, // все
			{"нет такого", "c"},       // неизвестное имя просто пропускаем
		} {
			got, err := LoadSignals(path, names)
			if err != nil {
				t.Fatal(err)
			}
			same(t, got, ref, names)
		}
		// И старый Load (теперь тоже порциями) — тот же результат, что раньше
		all, err := Load(path)
		if err != nil || !reflect.DeepEqual(all, ref) {
			t.Errorf("%s: Load не совпал со старым чтением: %v", filepath.Base(path), err)
		}
	}
	if f, _ := LoadSignals(cut, []string{"a"}); !f.Damaged {
		t.Error("оборванный файл должен быть помечен")
	}
	if _, err := LoadSignals(filepath.Join(dir, "нет.pla"), nil); err == nil {
		t.Error("нет файла — должна быть ошибка")
	}
}

func TestCacheSignals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.pla")
	twoGroups(t, path)
	c := NewCache(2)

	f1, _ := c.GetSignals(path, []string{"a"})
	if !f1.Has(0, 0) || f1.Has(0, 1) {
		t.Fatal("ждали только a")
	}
	if f2, _ := c.GetSignals(path, []string{"a"}); f2 != f1 {
		t.Error("тот же сигнал — из кеша")
	}
	if f2, _ := c.GetSignals(path, nil); f2 != f1 {
		t.Error("только времена — уже есть в кеше")
	}
	// Добавили сигнал — дочитали вместе с уже загруженным, копия файла одна
	f3, _ := c.GetSignals(path, []string{"y"})
	if !f3.Has(0, 0) || !f3.Has(1, 1) || len(c.items) != 1 {
		t.Errorf("ждали a и y в одной записи кеша; записей %d", len(c.items))
	}
	// Все сигналы — новая загрузка; дальше любые сигналы берутся из неё
	all, _ := c.Get(path)
	if f4, _ := c.GetSignals(path, []string{"c"}); f4 != all {
		t.Error("после Get любой сигнал — из полной копии")
	}
}

// Замер: память при открытии 3 сигналов из 114 — как ваша смена SGGT, только час
func TestLoadSignalsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("замер памяти")
	}
	path := filepath.Join(t.TempDir(), "m.pla")
	sig := make([]SignalInfo, 114)
	for i := range sig {
		sig[i] = SignalInfo{Name: fmt.Sprint("s", i), Type: "REAL"}
	}
	w, _ := Create(path, Header{PLC: "Т", Shift: "A", Groups: []GroupInfo{{PeriodMs: 100, Signals: sig}}})
	for p := 0; p < 36000; p += 20 { // час по 100 мс, порции по 2 с
		c := Chunk{Values: make([][]float64, len(sig))}
		for k := range 20 {
			c.Times = append(c.Times, int64(p+k)*100)
			for s := range sig {
				c.Values[s] = append(c.Values[s], float64((p+k)/50%7+s))
			}
		}
		w.WriteChunk(c)
	}
	w.Close()

	held := func(load func() (*File, error)) (float64, time.Duration) {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		t0 := time.Now()
		f, err := load()
		d := time.Since(t0)
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&m1)
		runtime.KeepAlive(f)
		return float64(m1.HeapAlloc-m0.HeapAlloc) / 1e6, d
	}
	allMB, allT := held(func() (*File, error) { return Load(path) })
	selMB, selT := held(func() (*File, error) { return LoadSignals(path, []string{"s1", "s50", "s100"}) })
	t.Logf("час, 114 сигналов: все — %.0f МБ за %v; 3 сигнала — %.1f МБ за %v",
		allMB, allT.Round(time.Millisecond), selMB, selT.Round(time.Millisecond))
	if selMB > allMB/10 {
		t.Errorf("3 сигнала из 114 должны занимать в разы меньше: %.1f МБ против %.0f", selMB, allMB)
	}
}

// Ваш реальный файл: сравнить новое чтение со старым и замерить память. Запуск (PowerShell):
//
//	$env:PLA = "D:\plc-data\SGGT\2026\11\08\B_20261108-200000_g1.pla"
//	go test ./internal/archive -run RealFile -v
//
// Без PLA тест пропускается — файл никуда не копируется и в репозиторий не попадает.
func TestRealFile(t *testing.T) {
	path := os.Getenv("PLA")
	if path == "" {
		t.Skip("PLA не задан")
	}
	ref, err := refLoad(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range ref.Header.Groups {
		for i, s := range g.Signals {
			if i%40 == 0 { // каждый 40-й: 3 сигнала из 114
				names = append(names, s.Name)
			}
		}
	}
	got, err := LoadSignals(path, names)
	if err != nil {
		t.Fatal(err)
	}
	same(t, got, ref, names)
	all, _ := Load(path)
	if !reflect.DeepEqual(all, ref) {
		t.Error("Load (порциями) не совпал со старым чтением")
	}
	ref, all, got = nil, nil, nil

	measure := func(load func() (*File, error)) float64 {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		f, _ := load()
		runtime.GC()
		runtime.ReadMemStats(&m1)
		runtime.KeepAlive(f)
		return float64(m1.HeapAlloc-m0.HeapAlloc) / 1e6
	}
	t.Logf("сигналов %v: совпадает со старым чтением", names)
	t.Logf("память: все сигналы — %.0f МБ, эти %d — %.1f МБ",
		measure(func() (*File, error) { return Load(path) }), len(names),
		measure(func() (*File, error) { return LoadSignals(path, names) }))
}
