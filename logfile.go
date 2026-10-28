package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// ---------- Лог в файл ----------
//
// У службы Windows нет консоли: всё, что программа пишет через log, пропадёт.
// Поэтому лог пишем ещё и в файл рядом с программой. Файл не растёт бесконечно:
// дорос до max — становится «.old» (прежний «.old» удаляется), начинается новый.
// Итого на диске не больше двух файлов по max.

// logMax — размер одного файла лога
const logMax = 10 << 20 // 10 МБ

// logFile — io.Writer, который пишет в файл и сам «переворачивает» его по размеру.
// Пакету log всё равно, куда писать: ему нужен любой тип с методом Write.
type logFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64 // сколько уже в текущем файле
}

// openLog открывает (или создаёт) файл лога и дописывает в конец
func openLog(path string, max int64) (*logFile, error) {
	l := &logFile{path: path, max: max}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, fi.Size()
	return nil
}

// Write — запись одной строки лога (пакет log вызывает Write один раз на строку)
func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		// Не вышло перевернуть — не беда: продолжаем писать в старый файл,
		// потерять строки лога хуже, чем превысить размер.
		_ = l.rotate()
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate: текущий файл → «.old», открыть новый пустой
func (l *logFile) rotate() error {
	l.f.Close()
	old := l.path + ".old"
	os.Remove(old) // в Windows Rename не перезаписывает существующий файл
	err := os.Rename(l.path, old)
	if oerr := l.open(); oerr != nil {
		return oerr
	}
	return err
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// setupLog направляет log в файл и в консоль. path "-" — только консоль.
// Возвращает функцию закрытия файла (её вызывают в самом конце).
func setupLog(path string) (func(), error) {
	log.SetFlags(log.Ldate | log.Ltime)
	if path == "-" {
		log.SetFlags(log.Ltime)
		return func() {}, nil
	}
	// Папки может ещё не быть (первый запуск на новом ПК) — создаём
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lf, err := openLog(path, logMax)
	if err != nil {
		return nil, err
	}
	// Файл — ПЕРВЫМ. MultiWriter останавливается на первой ошибке, а у службы
	// консоли нет и запись в неё даёт ошибку: будь консоль первой, до файла
	// строка бы не дошла.
	log.SetOutput(io.MultiWriter(lf, os.Stderr))
	return func() { lf.Close() }, nil
}
