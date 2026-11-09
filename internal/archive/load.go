package archive

import (
	"errors"
	"io"
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

// File — файл в памяти: времена всех групп и значения загруженных сигналов.
// Values[сигнал] == nil — этот сигнал не загружали (LoadSignals без него).
type File struct {
	Header  Header
	Groups  []GroupData
	Damaged bool // хвост был оборван — показана только целая часть
}

// Has — загружены ли значения сигнала (по номеру группы и сигнала в ней)
func (f *File) Has(g, s int) bool {
	return g < len(f.Groups) && s < len(f.Groups[g].Values) && f.Groups[g].Values[s] != nil
}

// Load читает файл целиком — все сигналы. Повреждённый хвост — не ошибка: берём всё, что цело.
func Load(path string) (*File, error) {
	return load(path, func(string) bool { return true })
}

// LoadSignals читает только перечисленные сигналы (по имени) и времена всех групп.
//
// Файл всё равно читается весь: в порции сигналы сжаты вместе, и один отдельно не достать.
// Но лишние столбцы выбрасываются сразу после распаковки порции — память зависит от того,
// СКОЛЬКО сигналов смотрят, а не сколько их в файле: 3 из 114 — ~10 МБ вместо ~430 МБ на смену.
// Пустой список — только времена (хватит, чтобы показать, с какого по какое время записано).
func LoadSignals(path string, names []string) (*File, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	return load(path, func(name string) bool { return want[name] })
}

// load — общее чтение: порция за порцией (не держим в памяти все порции разом, как ReadAll),
// значения — только у сигналов, для которых keep(имя) == true
func load(path string, keep func(name string) bool) (*File, error) {
	rd, err := Open(path)
	if err != nil {
		return nil, err // не читается даже заголовок
	}
	defer rd.Close()

	h := rd.Header()
	f := &File{Header: h, Groups: make([]GroupData, len(h.Groups))}
	for gi, g := range h.Groups {
		f.Groups[gi].Values = make([][]float64, len(g.Signals))
		for si, sg := range g.Signals {
			if keep(sg.Name) {
				f.Groups[gi].Values[si] = []float64{} // не nil: «загружен», даже если точек нет
			}
		}
	}
	for {
		c, err := rd.Next()
		if err == io.EOF {
			return f, nil
		}
		if errors.Is(err, ErrDamaged) {
			f.Damaged = true // оборванный хвост: всё, что до него, — цело
			return f, nil
		}
		if err != nil {
			return nil, err
		}
		gd := &f.Groups[c.Group]
		gd.Times = append(gd.Times, c.Times...)
		for s, col := range c.Values {
			if gd.Values[s] != nil {
				gd.Values[s] = append(gd.Values[s], col...)
			}
		}
	}
}

// ---------- Кеш последних открытых файлов ----------

type cacheItem struct {
	path string
	size int64           // размер при загрузке: вырос — значит, файл ещё пишется, перечитать
	all  bool            // загружены все сигналы (Get)
	has  map[string]bool // какие сигналы загружены (GetSignals)
	file *File
}

// covers — есть ли в загруженном все нужные сигналы
func (it cacheItem) covers(all bool, names []string) bool {
	if it.all {
		return true
	}
	if all {
		return false
	}
	for _, n := range names {
		if !it.has[n] {
			return false
		}
	}
	return true
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

// Get — файл со всеми сигналами
func (c *Cache) Get(path string) (*File, error) {
	return c.get(path, true, nil)
}

// GetSignals — файл с этими сигналами (и временами всех групп). Если в кеше этот файл уже
// есть с другими сигналами — дочитываем недостающие вместе с уже загруженными, а не заводим
// вторую копию файла.
func (c *Cache) GetSignals(path string, names []string) (*File, error) {
	return c.get(path, false, names)
}

func (c *Cache) get(path string, all bool, names []string) (*File, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	need := map[string]bool{}
	for _, n := range names {
		need[n] = true
	}
	for i, it := range c.items {
		if it.path != path || it.size != fi.Size() {
			continue
		}
		if it.covers(all, names) {
			// Нашли и файл не менялся — переставляем в конец как самый свежий
			c.items = append(append(c.items[:i:i], c.items[i+1:]...), it)
			c.mu.Unlock()
			return it.file, nil
		}
		for n := range it.has { // не хватает сигналов — грузим вместе с уже загруженными
			need[n] = true
		}
	}
	c.mu.Unlock()

	// Загрузка — долгая, поэтому БЕЗ блокировки: другие запросы не ждут
	var f *File
	if all {
		f, err = Load(path)
	} else {
		list := make([]string, 0, len(need))
		for n := range need {
			list = append(list, n)
		}
		f, err = LoadSignals(path, list)
	}
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
	c.items = append(kept, cacheItem{path: path, size: fi.Size(), all: all, has: need, file: f})
	if len(c.items) > c.max {
		c.items = c.items[len(c.items)-c.max:]
	}
	return f, nil
}
