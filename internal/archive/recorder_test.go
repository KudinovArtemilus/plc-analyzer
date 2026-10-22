package archive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runRecorder запускает запись, вызывает feed и корректно останавливает её
func runRecorder(t *testing.T, root string, feed func(r *Recorder)) {
	t.Helper()
	r := NewRecorder(root, "315F", 30)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	feed(r)
	cancel()
	<-done // ждём, пока очередь допишется и файл закроется
}

func TestRecorderShiftChange(t *testing.T) {
	root := t.TempDir()
	groups := []GroupInfo{{PeriodMs: 1000, Signals: []SignalInfo{{Name: "p", Type: "INT"}}}}

	runRecorder(t, root, func(r *Recorder) {
		r.SetGroups(1, groups)
		start := at(21, 19, 59).Add(58 * time.Second) // 19:59:58
		for i := 0; i < 4; i++ {                       // 19:59:58, 59, 20:00:00, 01
			r.Add(1, 0, start.Add(time.Duration(i)*time.Second), []float64{float64(i)})
		}
	})

	files, _ := filepath.Glob(filepath.Join(root, "315F", "2026", "10", "21", "*.pla"))
	if len(files) != 2 {
		t.Fatalf("файлов %d, ожидалось 2 (смена А и смена Б): %v", len(files), files)
	}
	for i, wantShift := range []string{"A", "B"} {
		h, chunks, err := ReadAll(files[i])
		if err != nil {
			t.Fatal(err)
		}
		points := 0
		for _, c := range chunks {
			points += len(c.Times)
		}
		if h.Shift != wantShift || points != 2 {
			t.Errorf("%s: смена %s, точек %d; ожидалось %s и 2",
				filepath.Base(files[i]), h.Shift, points, wantShift)
		}
	}
}

func TestRecorderNewFileOnConfig(t *testing.T) {
	root := t.TempDir()
	g1 := []GroupInfo{{PeriodMs: 100, Signals: []SignalInfo{{Name: "a"}}}}
	g2 := []GroupInfo{{PeriodMs: 100, Signals: []SignalInfo{{Name: "a"}, {Name: "b"}}}}
	t0 := at(21, 10, 0)

	runRecorder(t, root, func(r *Recorder) {
		r.SetGroups(1, g1)
		r.Add(1, 0, t0, []float64{1})
		r.SetGroups(2, g2)
		r.Add(1, 0, t0.Add(time.Second), []float64{2})       // старая конфигурация — отбросится
		r.Add(2, 0, t0.Add(2*time.Second), []float64{3, 4})  // новая
	})

	files, _ := filepath.Glob(filepath.Join(root, "315F", "2026", "10", "21", "*.pla"))
	if len(files) != 2 || !strings.HasSuffix(files[0], "_g1.pla") || !strings.HasSuffix(files[1], "_g2.pla") {
		t.Fatalf("ожидались файлы _g1 и _g2: %v", files)
	}
	h, chunks, _ := ReadAll(files[1])
	if len(h.Groups[0].Signals) != 2 || len(chunks) != 1 || chunks[0].Values[1][0] != 4 {
		t.Errorf("второй файл записан неверно: %+v %+v", h, chunks)
	}
}

func TestCleanup(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "315F")
	dayDir := func(d time.Time) string {
		return filepath.Join(base, d.Format("2006"), d.Format("01"), d.Format("02"))
	}
	old, recent := time.Now().AddDate(0, 0, -40), time.Now().AddDate(0, 0, -5)
	for _, d := range []string{dayDir(old), dayDir(recent), filepath.Join(base, "заметки")} {
		os.MkdirAll(d, 0o755)
	}

	NewRecorder(root, "315F", 30).cleanup()

	if _, err := os.Stat(dayDir(old)); !os.IsNotExist(err) {
		t.Error("папка старше 30 дней должна быть удалена")
	}
	if _, err := os.Stat(dayDir(recent)); err != nil {
		t.Error("свежая папка не должна удаляться")
	}
	if _, err := os.Stat(filepath.Join(base, "заметки")); err != nil {
		t.Error("чужая папка не должна удаляться")
	}
}
