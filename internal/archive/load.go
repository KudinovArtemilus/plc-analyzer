package archive

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ---------- Список файлов ----------

// FileInfo — один файл-сегмент на диске
type FileInfo struct {
	Path    string
	Shift   Shift
	Started time.Time
	Gen     int
	Size    int64
}

// A_20261021-080000_g1.pla
var fileNameRe = regexp.MustCompile(`^([AB])_(\d{8}-\d{6})_g(\d+)\.pla$`)

// ListFiles — все файлы ПЛК, новые сверху
func ListFiles(root, plc string) ([]FileInfo, error) {
	base := filepath.Join(root, SafeName(plc))
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return nil, nil // записей ещё нет — это не ошибка
	}

	var out []FileInfo
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // нечитаемую папку пропускаем, идём дальше
		}
		m := fileNameRe.FindStringSubmatch(d.Name())
		if m == nil {
			return nil // не наш файл
		}
		started, err := time.ParseInLocation("20060102-150405", m[2], time.Local)
		if err != nil {
			return nil
		}
		gen, _ := strconv.Atoi(m[3])
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, FileInfo{
			Path: path, Shift: ShiftOf(started), Started: started, Gen: gen, Size: info.Size(),
		})
		return nil
	})

	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, err
}

// ---------- Файл в памяти ----------

// GroupData — все точки одной группы файла
type GroupData struct {
	Times  []int64     // мс Unix
	Values [][]float64 // Values[сигнал][точка]
}

// File — файл, целиком загруженный в память
type File struct {
	Header  Header
	Groups  []GroupData
	Damaged bool // хвост был оборван — показана только целая часть
}

// Load читает файл целиком. Повреждённый хвост — не ошибка: берём всё, что цело.
func Load(path string) (*File, error) {
	h, chunks, err := ReadAll(path)
	damaged := errors.Is(err, ErrDamaged)
	if err != nil && (!damaged || len(h.Groups) == 0) {
		return nil, err // не читается даже заголовок
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

// ---------- Кеш последних открытых файлов ----------

type cacheItem struct {
	path string
	size int64 // размер при загрузке: вырос — значит, файл ещё пишется, перечитать
	file *File
}

// Cache держит в памяти несколько последних открытых файлов
type Cache struct {
	mu    sync.Mutex
	max   int
	items []cacheItem // самый свежий — в конце
}

func NewCache(max int) *Cache {
	return &Cache{max: max}
}

func (c *Cache) Get(path string) (*File, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	for i, it := range c.items {
		if it.path == path && it.size == fi.Size() {
			// Нашли и файл не менялся — переставляем в конец как самый свежий
			c.items = append(append(c.items[:i:i], c.items[i+1:]...), it)
			c.mu.Unlock()
			return it.file, nil
		}
	}
	c.mu.Unlock()

	// Загрузка — долгая, поэтому БЕЗ блокировки: другие запросы не ждут
	f, err := Load(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.items[:0]
	for _, it := range c.items {
		if it.path != path { // старую версию этого же файла выбрасываем
			kept = append(kept, it)
		}
	}
	c.items = append(kept, cacheItem{path: path, size: fi.Size(), file: f})
	if len(c.items) > c.max {
		c.items = c.items[len(c.items)-c.max:]
	}
	return f, nil
}
