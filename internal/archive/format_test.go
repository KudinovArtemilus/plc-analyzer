package archive

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testHeader() Header {
	return Header{
		PLC: "315F", ShiftDate: "2026-10-21", Shift: "A", Gen: 1,
		Groups: []GroupInfo{
			{PeriodMs: 100, Signals: []SignalInfo{
				{Name: "pressure", Addr: "DB11.DBW242", Type: "INT"},
				{Name: "temp", Addr: "DB11.DBD100", Type: "REAL", Scale: 1, Unit: "°C"},
			}},
			{PeriodMs: 1000, Signals: []SignalInfo{
				{Name: "valve", Addr: "DB11.DBX4.3", Type: "BOOL"},
			}},
		},
	}
}

var (
	chunk1 = Chunk{Group: 0, Times: []int64{1000, 1100, 1200},
		Values: [][]float64{{10, -5, 300}, {85.3, 85.25, -0.5}}}
	chunk2 = Chunk{Group: 1, Times: []int64{1000, 2000},
		Values: [][]float64{{0, 1}}}
)

// writeTestFile пишет файл с двумя порциями и возвращает путь
func writeTestFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "2026", "10", "21", "test.pla") // папки создаст Create
	w, err := Create(path, testHeader())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Chunk{chunk1, chunk2} {
		if err := w.WriteChunk(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRoundTrip(t *testing.T) {
	r, err := Open(writeTestFile(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if h := r.Header(); h.PLC != "315F" || len(h.Groups) != 2 || h.Groups[0].Signals[1].Unit != "°C" {
		t.Fatalf("заголовок прочитан неверно: %+v", h)
	}
	for _, want := range []Chunk{chunk1, chunk2} {
		got, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("получили %+v, ожидалось %+v", got, want)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("в конце ожидался io.EOF, получили %v", err)
	}
}

func TestDamagedTail(t *testing.T) {
	path := writeTestFile(t)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, data[:len(data)-3], 0o644) // «питание отключили» посреди последней порции

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if got, err := r.Next(); err != nil || !reflect.DeepEqual(got, chunk1) {
		t.Fatalf("первая порция должна читаться: %v", err)
	}
	if _, err := r.Next(); !errors.Is(err, ErrDamaged) {
		t.Fatalf("ожидался ErrDamaged, получили %v", err)
	}
}

func TestCorruptedByte(t *testing.T) {
	path := writeTestFile(t)
	data, _ := os.ReadFile(path)
	data[len(data)-1] ^= 0xFF // испортить один байт в последней порции
	os.WriteFile(path, data, 0o644)

	r, _ := Open(path)
	defer r.Close()
	r.Next() // первая цела
	if _, err := r.Next(); !errors.Is(err, ErrDamaged) {
		t.Fatalf("испорченный байт должен давать ErrDamaged, получили %v", err)
	}
}

func TestNotOurFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pla")
	os.WriteFile(path, []byte("hello world, это не наш файл"), 0o644)
	if _, err := Open(path); err == nil {
		t.Fatal("чужой файл должен давать ошибку")
	}
}

func TestCompression(t *testing.T) {
	// 1 час, 10 медленно меняющихся INT-сигналов по 100 мс, порции по 2 с (20 точек)
	h := Header{PLC: "test", Groups: []GroupInfo{{PeriodMs: 100}}}
	for s := 0; s < 10; s++ {
		h.Groups[0].Signals = append(h.Groups[0].Signals, SignalInfo{Name: "s", Type: "INT"})
	}
	path := filepath.Join(t.TempDir(), "big.pla")
	w, err := Create(path, h)
	if err != nil {
		t.Fatal(err)
	}

	const points = 36000
	for start := 0; start < points; start += 20 {
		c := Chunk{Values: make([][]float64, 10)}
		for i := start; i < start+20; i++ {
			c.Times = append(c.Times, int64(i)*100)
			for s := range c.Values {
				c.Values[s] = append(c.Values[s],
					math.Round(1000+500*math.Sin(float64(i)/300+float64(s))))
			}
		}
		if err := w.WriteChunk(c); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	info, _ := os.Stat(path)
	plain := int64(points * 10 * 2) // те же данные «голыми» INT по 2 байта
	t.Logf("1 час, 10 сигналов по 100 мс: %d КБ (голые INT: %d КБ)", info.Size()/1024, plain/1024)
	if info.Size() >= plain {
		t.Errorf("файл не меньше голых INT — сжатие не работает")
	}
}
