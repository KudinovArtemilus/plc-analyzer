package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// plcBase — папка данных ПЛК из запроса (?plc=…, по свежему имени из базы)
func (s *Server) plcBase(r *http.Request) (string, store.PLC, error) {
	pl, err := s.Store.PLC(r.Context(), s.reqPLCID(r))
	if err != nil {
		return "", pl, err
	}
	return filepath.Join(s.DataRoot, archive.SafeName(pl.Name)), pl, nil
}

// resolve превращает путь из браузера в путь на диске.
// Пропускает только .pla строго внутри папки ПЛК — защита от "../../.."
func (s *Server) resolve(r *http.Request, rel string) (string, error) {
	base, _, err := s.plcBase(r)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") ||
		filepath.Ext(clean) != ".pla" {
		return "", errors.New("неверный путь к файлу")
	}
	full := filepath.Join(base, clean)
	if !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", errors.New("неверный путь к файлу")
	}
	return full, nil
}

// ---------- Страница со списком файлов ----------

type fileView struct {
	ID    int
	Rel   string
	Shift string
	Start string
	Gen   int
	Size  string
	Live  bool
}

type dayView struct {
	Title string
	Files []fileView
}

func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	base, pl, err := s.plcBase(r)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files, err := archive.ListFiles(s.DataRoot, pl.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Какой файл этого ПЛК сейчас пишется
	live := ""
	if s.Units != nil {
		_, rs, _ := s.Units(pl.ID)
		live = rs.File
	}

	var days []dayView
	for i, f := range files {
		title := f.Shift.Date.Format("02.01.2006")
		if len(days) == 0 || days[len(days)-1].Title != title {
			days = append(days, dayView{Title: title})
		}
		rel, _ := filepath.Rel(base, f.Path)
		d := &days[len(days)-1]
		d.Files = append(d.Files, fileView{
			ID: i, Rel: filepath.ToSlash(rel), Shift: f.Shift.Title(),
			Start: f.Started.Format("02.01 15:04:05"), Gen: f.Gen,
			Size: fmtBytes(f.Size), Live: f.Path == live,
		})
	}
	render(w, "history", map[string]any{"PLC": pl, "PLCs": all, "Days": days})
}

// ---------- Галочки сигналов из заголовка файла ----------

type sigView struct {
	Name string
	Info string
}

func (s *Server) historyPick(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("file")
	f, err := s.openFile(r, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var sigs []sigView
	for _, ref := range pickSignals(f, nil) {
		info := fmtPeriodMs(ref.PeriodMs)
		if ref.Unit != "" {
			info = ref.Unit + " · " + info
		}
		sigs = append(sigs, sigView{Name: ref.Name, Info: info})
	}
	from, to, _ := fileRange(f)
	// Наборы ПЛК — кнопки «отметить набор». Не загрузились — галочки по одной, как раньше.
	sets, _ := s.Store.Sets(r.Context(), s.reqPLCID(r))
	render(w, "pick", map[string]any{
		"PLC": s.reqPLCID(r), "Rel": rel, "Damaged": f.Damaged, "Signals": sigs,
		"From": fmtMs(from), "To": fmtMs(to), "Sets": sets,
	})
}

// ---------- API для страницы просмотра ----------

type sigJSON struct {
	Name     string `json:"name"`
	Unit     string `json:"unit"`
	PeriodMs int64  `json:"period_ms"`
}

func (s *Server) historyMeta(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := s.openFile(r, q.Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to, ok := fileRange(f)
	if !ok {
		http.Error(w, "в файле нет данных", http.StatusNotFound)
		return
	}
	h := f.Header
	out := struct {
		Title   string    `json:"title"`
		FromMs  int64     `json:"from_ms"`
		ToMs    int64     `json:"to_ms"`
		Damaged bool      `json:"damaged"`
		Signals []sigJSON `json:"signals"`
	}{
		Title:  fmt.Sprintf("%s · %s · смена %s · с %s", h.PLC, h.ShiftDate, h.Shift, fmtMs(from)),
		FromMs: from, ToMs: to, Damaged: f.Damaged,
	}
	for _, ref := range pickSignals(f, q["s"]) {
		out.Signals = append(out.Signals, sigJSON{Name: ref.Name, Unit: ref.Unit, PeriodMs: ref.PeriodMs})
	}
	writeJSON(w, out)
}

func (s *Server) historyData(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := s.openFile(r, q.Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to, ok := fileRange(f)
	if !ok {
		http.Error(w, "в файле нет данных", http.StatusNotFound)
		return
	}
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
		from = v
	}
	if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
		to = v
	}
	width, _ := strconv.Atoi(q.Get("width"))
	if width < 100 || width > 5000 {
		width = 1500
	}

	type seriesJSON struct {
		Name string `json:"name"`
		archive.Series
	}
	out := struct {
		Exact  bool         `json:"exact"`
		Series []seriesJSON `json:"series"`
	}{Exact: true}

	for _, ref := range pickSignals(f, q["s"]) {
		gd := f.Groups[ref.g]
		if archive.CountIn(gd.Times, from, to) > 2*width {
			out.Exact = false
		}
		sr := archive.Downsample(gd.Times, gd.Values[ref.i], from, to, width, 3*ref.PeriodMs)
		if sr.T == nil { // nil-срез превратился бы в JSON null, а браузеру нужен []
			sr.T, sr.V = []int64{}, []*float64{}
		}
		out.Series = append(out.Series, seriesJSON{Name: ref.Name, Series: sr})
	}
	writeJSON(w, out)
}

// ---------- Помощники ----------

func (s *Server) openFile(r *http.Request, rel string) (*archive.File, error) {
	path, err := s.resolve(r, rel)
	if err != nil {
		return nil, err
	}
	f, err := s.Cache.Get(path)
	if err != nil {
		return nil, fmt.Errorf("файл не открывается: %w", err)
	}
	return f, nil
}

// sigRef — где лежит сигнал внутри файла
type sigRef struct {
	Name     string
	Unit     string
	PeriodMs int64
	g, i     int // группа и номер сигнала в ней
}

// pickSignals — сигналы файла по списку имён (пустой список — все), в порядке файла
func pickSignals(f *archive.File, names []string) []sigRef {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []sigRef
	for gi, g := range f.Header.Groups {
		for si, sg := range g.Signals {
			if len(names) == 0 || want[sg.Name] {
				out = append(out, sigRef{Name: sg.Name, Unit: sg.Unit, PeriodMs: g.PeriodMs, g: gi, i: si})
			}
		}
	}
	return out
}

// fileRange — время первой и последней точки файла
func fileRange(f *archive.File) (from, to int64, ok bool) {
	for _, g := range f.Groups {
		if len(g.Times) == 0 {
			continue
		}
		if !ok || g.Times[0] < from {
			from = g.Times[0]
		}
		if !ok || g.Times[len(g.Times)-1] > to {
			to = g.Times[len(g.Times)-1]
		}
		ok = true
	}
	return
}

func fmtMs(ms int64) string {
	if ms == 0 {
		return "—"
	}
	return timeFromMs(ms).Format("02.01 15:04:05")
}

func fmtPeriodMs(ms int64) string {
	if ms >= 1000 {
		return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) + " с"
	}
	return strconv.FormatInt(ms, 10) + " мс"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func timeFromMs(ms int64) time.Time { return time.UnixMilli(ms) }
