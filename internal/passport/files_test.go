package passport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

// writePla — тестовая смена из shift() на диск, как её пишет служба
func writePla(t *testing.T, root, plc string) string {
	t.Helper()
	r := shift()
	started := time.UnixMilli(r.f.Groups[0].Times[0])
	path := archive.Path(root, plc, archive.ShiftOf(started), started, 1)
	w, err := archive.Create(path, r.f.Header)
	if err != nil {
		t.Fatal(err)
	}
	g := r.f.Groups[0]
	if err := w.WriteChunk(archive.Chunk{Group: 0, Times: g.Times, Values: g.Values}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return path
}

// age — сдвинуть время изменения файла в прошлое
func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pla := writePla(t, root, "Пресс 1")
	cfg := Config{Step: "Step", Mode: "Mode", ModeVal: 1, Home: 0}
	saved := time.Now().Add(-2 * time.Hour)

	csv, err := CSVPath(filepath.Join(root, archive.SafeName("Пресс 1")), pla)
	if err != nil || !strings.Contains(csv, string(filepath.Separator)+"passport"+string(filepath.Separator)) ||
		!strings.HasSuffix(csv, "_g1.csv") {
		t.Fatalf("путь паспорта: %q, %v", csv, err)
	}

	// Запись только что менялась — смена идёт, паспорт не считаем
	if n, err := Update(ctx, root, "Пресс 1", cfg, saved); n != 0 || err != nil {
		t.Fatalf("идущая смена: посчитано %d, %v", n, err)
	}

	// Запись дописана — паспорт появился: заголовок и два полных цикла
	age(t, pla, time.Hour)
	if n, err := Update(ctx, root, "Пресс 1", cfg, saved); n != 1 || err != nil {
		t.Fatalf("законченная смена: посчитано %d, %v", n, err)
	}
	b, err := os.ReadFile(csv)
	if err != nil || len(strings.Split(strings.TrimSpace(string(b)), "\n")) != 3 {
		t.Fatalf("паспорт:\n%s\n%v", b, err)
	}
	if _, err := os.Stat(csv + ".tmp"); !os.IsNotExist(err) {
		t.Error("временный файл остался")
	}

	// Уже посчитан и не устарел — второй раз не считаем
	if n, _ := Update(ctx, root, "Пресс 1", cfg, saved); n != 0 {
		t.Errorf("повторный проход посчитал %d", n)
	}

	// Настройку поменяли после паспорта — пересчитать
	if n, _ := Update(ctx, root, "Пресс 1", cfg, time.Now().Add(time.Minute)); n != 1 {
		t.Errorf("после смены настройки посчитано %d, ждали 1", n)
	}

	// Запись продолжилась после паспорта (связь была потеряна дольше 5 мин, потом вернулась)
	age(t, csv, 30*time.Minute)
	age(t, pla, 10*time.Minute)
	if n, _ := Update(ctx, root, "Пресс 1", cfg, saved); n != 1 {
		t.Errorf("запись новее паспорта: посчитано %d, ждали 1", n)
	}
}

func TestUpdateNoSignal(t *testing.T) {
	// В старой записи нет сигнала шага — пустой паспорт (только заголовок), и больше не пытаемся
	root := t.TempDir()
	pla := writePla(t, root, "П2")
	age(t, pla, time.Hour)
	cfg := Config{Step: "Шаг_новый", Home: 0}
	if n, err := Update(context.Background(), root, "П2", cfg, time.Time{}); n != 1 || err != nil {
		t.Fatalf("посчитано %d, %v", n, err)
	}
	if n, _ := Update(context.Background(), root, "П2", cfg, time.Time{}); n != 0 {
		t.Errorf("пустой паспорт пересчитывается снова: %d", n)
	}
}

func TestUpdateStops(t *testing.T) {
	// Служба останавливается — Update сразу выходит
	root := t.TempDir()
	age(t, writePla(t, root, "П3"), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := Update(ctx, root, "П3", Config{Step: "Step"}, time.Time{}); n != 0 || err == nil {
		t.Errorf("после отмены: %d, %v", n, err)
	}
	// ПЛК без записей — не ошибка
	if n, err := Update(context.Background(), root, "Нет такого", Config{Step: "Step"}, time.Time{}); n != 0 || err != nil {
		t.Errorf("нет записей: %d, %v", n, err)
	}
}
