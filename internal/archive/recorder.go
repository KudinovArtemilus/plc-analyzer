package archive

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	flushEvery   = 2 * time.Second
	cleanupEvery = time.Hour
	queueSize    = 1000
	retryAfter   = 5 * time.Second
)

type Status struct {
	File    string
	Bytes   int64
	Dropped int64
	LastErr string
	ErrAt   time.Time
}

// msg — элемент очереди: новый состав групп, переименование или точка
type msg struct {
	cfg    *cfgMsg
	rename string // новое имя ПЛК (пусто — не переименование)
	gen    int
	group  int
	t      int64
	values []float64
}

type cfgMsg struct {
	gen    int
	groups []GroupInfo
}

type Recorder struct {
	root     string
	plc      string // меняется только внутри Run
	keepDays int
	in       chan msg
	dropped  atomic.Int64

	mu      sync.Mutex
	file    string
	lastErr string
	errAt   time.Time
}

func NewRecorder(root, plc string, keepDays int) *Recorder {
	return &Recorder{root: root, plc: plc, keepDays: keepDays, in: make(chan msg, queueSize)}
}

// SetGroups — новый состав сигналов. Значения в файле уже с масштабом.
func (r *Recorder) SetGroups(gen int, groups []GroupInfo) {
	r.in <- msg{cfg: &cfgMsg{gen: gen, groups: groups}}
}

// Rename — ПЛК переименовали: закрыть файл и переименовать папку с данными
func (r *Recorder) Rename(newName string) {
	r.in <- msg{rename: newName}
}

func (r *Recorder) Add(gen, group int, t time.Time, values []float64) {
	select {
	case r.in <- msg{gen: gen, group: group, t: t.UnixMilli(), values: values}:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) Status() Status {
	r.mu.Lock()
	s := Status{File: r.file, LastErr: r.lastErr, ErrAt: r.errAt}
	r.mu.Unlock()
	s.Dropped = r.dropped.Load()
	if s.File != "" {
		if fi, err := os.Stat(s.File); err == nil {
			s.Bytes = fi.Size()
		}
	}
	return s
}

func (s Shift) Same(o Shift) bool {
	return s.Name == o.Name && s.Date.Equal(o.Date)
}

func (r *Recorder) Run(ctx context.Context) {
	var (
		w       *Writer
		shift   Shift
		gen     = -1
		groups  []GroupInfo
		pending []Chunk
		retryAt time.Time
	)

	resetPending := func() {
		pending = make([]Chunk, len(groups))
		for gi, g := range groups {
			pending[gi] = Chunk{Group: gi, Values: make([][]float64, len(g.Signals))}
		}
	}

	flush := func() {
		for gi := range pending {
			c := &pending[gi]
			if len(c.Times) == 0 {
				continue
			}
			if w != nil {
				if err := w.WriteChunk(*c); err != nil {
					r.fail("запись в файл: %v", err)
					w.Close()
					w = nil
					retryAt = time.Now().Add(retryAfter)
				}
			}
			c.Times = c.Times[:0]
			for s := range c.Values {
				c.Values[s] = c.Values[s][:0]
			}
		}
	}

	closeFile := func() {
		flush()
		if w != nil {
			w.Close()
			w = nil
		}
	}

	openFile := func(sh Shift, at time.Time) {
		path := Path(r.root, r.plc, sh, at, gen)
		h := Header{
			PLC: r.plc, ShiftDate: sh.Date.Format("2006-01-02"), Shift: sh.Name,
			Gen: gen, Created: at, Groups: groups,
		}
		nw, err := Create(path, h)
		if err != nil {
			r.fail("создание файла: %v", err)
			retryAt = time.Now().Add(retryAfter)
			return
		}
		w, shift = nw, sh
		r.mu.Lock()
		r.file = path
		r.mu.Unlock()
		log.Printf("запись: %s, новый файл %s", sh.Title(), path)
	}

	// renameDir переименовывает папку ПЛК. Файл к этому моменту уже закрыт.
	renameDir := func(newName string) {
		oldBase := filepath.Join(r.root, SafeName(r.plc))
		newBase := filepath.Join(r.root, SafeName(newName))
		r.plc = newName
		r.mu.Lock()
		r.file = ""
		r.mu.Unlock()

		if oldBase == newBase {
			return
		}
		if _, err := os.Stat(oldBase); os.IsNotExist(err) {
			return
		}
		if _, err := os.Stat(newBase); err == nil {
			log.Printf("запись: папка %s уже существует — не сливаю; старые данные остались в %s",
				newBase, oldBase)
			return
		}
		if err := os.Rename(oldBase, newBase); err != nil {
			r.fail("переименование папки %s → %s: %v", oldBase, newBase, err)
			return
		}
		log.Printf("запись: папка данных переименована %s → %s", oldBase, newBase)
	}

	handle := func(m msg) {
		if m.rename != "" {
			closeFile() // в Windows открытый файл не даст переименовать папку
			renameDir(m.rename)
			return
		}
		if m.cfg != nil {
			closeFile()
			gen, groups = m.cfg.gen, m.cfg.groups
			resetPending()
			return
		}
		if m.gen != gen || m.group >= len(groups) || len(m.values) != len(pending[m.group].Values) {
			return
		}

		t := time.UnixMilli(m.t)
		sh := ShiftOf(t)
		if w != nil && !sh.Same(shift) {
			closeFile()
		}
		if w == nil {
			if time.Now().Before(retryAt) {
				r.dropped.Add(1)
				return
			}
			openFile(sh, t)
			if w == nil {
				r.dropped.Add(1)
				return
			}
		}

		c := &pending[m.group]
		c.Times = append(c.Times, m.t)
		for s, v := range m.values {
			c.Values[s] = append(c.Values[s], v)
		}
	}

	r.cleanup()
	flushT := time.NewTicker(flushEvery)
	defer flushT.Stop()
	cleanT := time.NewTicker(cleanupEvery)
	defer cleanT.Stop()

	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case m := <-r.in:
					handle(m)
				default:
					closeFile()
					return
				}
			}
		case m := <-r.in:
			handle(m)
		case <-flushT.C:
			flush()
		case <-cleanT.C:
			r.cleanup()
		}
	}
}

func (r *Recorder) fail(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if text != r.lastErr || time.Since(r.errAt) > time.Minute {
		log.Printf("ЗАПИСЬ: %s", text)
	}
	r.lastErr, r.errAt = text, time.Now()
}

// cleanup удаляет папки дней старше keepDays.
// Трогает ТОЛЬКО папки вида 2026\10\21 — всё остальное не наше.
func (r *Recorder) cleanup() {
	if r.keepDays <= 0 {
		return
	}
	base := filepath.Join(r.root, SafeName(r.plc))
	now := time.Now()
	limit := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).
		AddDate(0, 0, -r.keepDays)

	years, _ := os.ReadDir(base)
	for _, y := range years {
		if !y.IsDir() || !isDigits(y.Name(), 4) {
			continue // не папка года — не трогаем
		}
		yp := filepath.Join(base, y.Name())
		months, _ := os.ReadDir(yp)
		for _, m := range months {
			if !m.IsDir() || !isDigits(m.Name(), 2) {
				continue
			}
			mp := filepath.Join(yp, m.Name())
			days, _ := os.ReadDir(mp)
			for _, d := range days {
				if !d.IsDir() || !isDigits(d.Name(), 2) {
					continue
				}
				date, err := time.ParseInLocation("2006/01/02",
					y.Name()+"/"+m.Name()+"/"+d.Name(), time.Local)
				if err != nil {
					continue
				}
				if date.Before(limit) {
					dp := filepath.Join(mp, d.Name())
					if err := os.RemoveAll(dp); err != nil {
						r.fail("удаление %s: %v", dp, err)
					} else {
						log.Printf("запись: удалена папка старше %d дней: %s", r.keepDays, dp)
					}
				}
			}
			os.Remove(mp) // удалится, только если стала пустой
		}
		os.Remove(yp)
	}
}

// isDigits — строка ровно из n цифр: "2026", "09"
func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func ReadAll(path string) (Header, []Chunk, error) {
	rd, err := Open(path)
	if err != nil {
		return Header{}, nil, err
	}
	defer rd.Close()

	var chunks []Chunk
	for {
		c, err := rd.Next()
		if err == io.EOF {
			return rd.Header(), chunks, nil
		}
		if err != nil {
			return rd.Header(), chunks, err
		}
		chunks = append(chunks, c)
	}
}
