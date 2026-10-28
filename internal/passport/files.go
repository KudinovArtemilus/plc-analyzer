package passport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

// ---------- Паспорта на диске (34a-3) ----------
//
// Паспорт смены лежит рядом с записями ПЛК, повторяя их папки:
//
//	D:\plc-data\Пресс\2026\10\28\A_20261028-080000_g1.pla            — запись (удаляется через 30 дней)
//	D:\plc-data\Пресс\passport\2026\10\28\A_20261028-080000_g1.csv   — паспорт (хранится всегда)
//
// Очистка старых записей трогает только папки «год\месяц\день» прямо в папке ПЛК,
// поэтому папку passport она не удаляет.

// settle — запись считается дописанной, если файл не менялся столько времени.
// Текущая смена пишется каждую секунду, поэтому её паспорт не считаем, пока она идёт.
const settle = 5 * time.Minute

// CSVPath — путь паспорта для файла записи pla, лежащего в папке ПЛК plcDir
func CSVPath(plcDir, pla string) (string, error) {
	rel, err := filepath.Rel(plcDir, pla)
	if err != nil {
		return "", err
	}
	rel = rel[:len(rel)-len(filepath.Ext(rel))] + ".csv"
	return filepath.Join(plcDir, "passport", rel), nil
}

// stale — паспорт нужно посчитать (или пересчитать):
// запись дописана, а паспорта нет, или он старше записи (запись продолжилась после него),
// или старше настройки (настройку поменяли — циклы теперь ищутся иначе).
func stale(pla, csv os.FileInfo, saved, now time.Time) bool {
	if now.Sub(pla.ModTime()) < settle {
		return false // запись ещё идёт
	}
	return csv == nil || csv.ModTime().Before(pla.ModTime()) || csv.ModTime().Before(saved)
}

// Update — посчитать паспорта всех законченных записей ПЛК, у которых паспорта нет
// или он устарел. Возвращает, сколько паспортов записано.
// Плохой файл не останавливает остальные: ошибка в лог, идём дальше.
func Update(ctx context.Context, root, plcName string, c Config, saved time.Time) (int, error) {
	files, err := archive.ListFiles(root, plcName)
	if err != nil {
		return 0, err
	}
	plcDir := filepath.Join(root, archive.SafeName(plcName))
	now := time.Now()
	made := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return made, err // служба останавливается — не держим её
		}
		csv, err := CSVPath(plcDir, f.Path)
		if err != nil {
			continue
		}
		pi, err := os.Stat(f.Path)
		if err != nil {
			continue // файл удалила очистка, пока мы шли по списку
		}
		ci, _ := os.Stat(csv) // нет паспорта — ci == nil
		if !stale(pi, ci, saved, now) {
			continue
		}
		if err := build(f.Path, csv, c); err != nil {
			log.Printf("паспорт %s: %v", f.Path, err)
			continue
		}
		made++
	}
	return made, nil
}

// build — паспорт одной записи
func build(pla, csv string, c Config) error {
	f, err := archive.Load(pla)
	if err != nil {
		return err
	}
	t, err := Build(f, c)
	if errors.Is(err, ErrNoSignal) {
		// В этой записи нет сигнала шага или режима (его тогда ещё не писали или переименовали).
		// Пишем пустой паспорт: иначе служба пыталась бы снова каждые 10 минут.
		log.Printf("паспорт %s: %v — паспорт пустой", pla, err)
		t = Table{}
	} else if err != nil {
		return err
	}
	return writeFile(csv, t)
}

// writeFile — записать паспорт целиком или никак: сначала во временный файл, потом
// переименовать. Python или Excel, открывший паспорт в этот момент, не увидит его наполовину.
func writeFile(path string, t Table) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := t.WriteCSV(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("запись: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path) // в Windows Go заменяет существующий файл
}
