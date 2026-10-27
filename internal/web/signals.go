package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

//go:embed templates
var templatesFS embed.FS

var dataTypes = []string{"BOOL", "BYTE", "INT", "WORD", "DINT", "DWORD", "REAL"}

var tmpl = template.Must(template.New("").
	Funcs(template.FuncMap{
		"types":     func() []string { return dataTypes },
		"kinds":     func() []string { return []string{store.KindTrend, store.KindAlarm, store.KindWarning} },
		"kindTitle": kindTitle,
	}).
	ParseFS(templatesFS, "templates/*.html"))

// kindTitle — название типа сигнала для экрана
func kindTitle(k string) string {
	switch k {
	case store.KindAlarm:
		return "авария"
	case store.KindWarning:
		return "предупреждение"
	default:
		return "тренд"
	}
}

// formView — данные для формы: значения полей и текст ошибки
type formView struct {
	Row   store.SignalRow
	Err   string
	Dicts []store.Dict // для выбора справочника в форме «изменить»
}

// dicts — список справочников для формы; без него форма просто без выбора, это не ошибка
func (s *Server) dicts(r *http.Request) []store.Dict {
	list, _ := s.Store.Dicts(r.Context())
	return list
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("шаблон %s: %v", name, err)
	}
}

// who — кто сделал изменение (пока — адрес компьютера, позже — имя пользователя)
func who(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "веб " + host
}

// ---------- Страница целиком ----------

// pagePLC — ПЛК страницы: ?plc=2 или первый, если не указан
func (s *Server) pagePLC(r *http.Request) (store.PLC, error) {
	id, err := strconv.ParseInt(r.URL.Query().Get("plc"), 10, 64)
	if err != nil {
		id = s.defaultPLCID(r.Context())
	}
	return s.Store.PLC(r.Context(), id)
}

func (s *Server) signalsPage(w http.ResponseWriter, r *http.Request) {
	pl, err := s.pagePLC(r)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows, err := s.Store.Signals(r.Context(), pl.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "page", map[string]any{
		"PLC":     pl,
		"PLCs":    all,
		"Signals": rows,
		"Form":    formView{Row: store.SignalRow{PLCID: pl.ID, Type: "INT", PeriodMs: 100, Scale: 1}},
	})
}

func (s *Server) addSignal(w http.ResponseWriter, r *http.Request) {
	row, err := signalFromForm(r) // внутри вызывается r.ParseForm()

	// К какому ПЛК добавляем — из скрытого поля формы
	plcID, perr := strconv.ParseInt(r.PostForm.Get("plc"), 10, 64)
	if perr != nil {
		plcID = s.defaultPLCID(r.Context())
	}
	row.PLCID = plcID
	if err == nil {
		if _, e := s.Store.PLC(r.Context(), plcID); e != nil {
			err = errors.New("ПЛК не найден")
		}
	}
	if err == nil {
		row.Enabled = true
		_, err = s.Store.AddSignal(r.Context(), who(r), row)
		err = friendly(err)
	}
	if err != nil {
		render(w, "addform", formView{Row: row, Err: err.Error()})
		return
	}
	w.Header().Set("HX-Refresh", "true")
}

func (s *Server) showRow(w http.ResponseWriter, r *http.Request) {
	if row, ok := s.rowFromPath(w, r); ok {
		render(w, "row", row)
	}
}

func (s *Server) editRow(w http.ResponseWriter, r *http.Request) {
	if row, ok := s.rowFromPath(w, r); ok {
		render(w, "editrow", formView{Row: row, Dicts: s.dicts(r)})
	}
}

func (s *Server) saveSignal(w http.ResponseWriter, r *http.Request) {
	old, ok := s.rowFromPath(w, r)
	if !ok {
		return
	}
	row, err := signalFromForm(r)
	row.ID, row.PLCID, row.Enabled, row.Hidden = old.ID, old.PLCID, old.Enabled, old.Hidden
	if err == nil {
		err = friendly(s.Store.UpdateSignal(r.Context(), who(r), row))
	}
	if err != nil {
		render(w, "editrow", formView{Row: row, Err: err.Error(), Dicts: s.dicts(r)})
		return
	}
	render(w, "row", row)
}

func (s *Server) toggleSignal(w http.ResponseWriter, r *http.Request) {
	row, ok := s.rowFromPath(w, r)
	if !ok {
		return
	}
	row.Enabled = !row.Enabled
	if err := s.Store.SetSignalEnabled(r.Context(), who(r), row); err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "row", row)
}

// hideSignal — POST /ui/signals/{id}/hide: скрыть с графиков Live или вернуть.
// Опрос и запись продолжаются: в «Истории» сигнал можно отметить и посмотреть.
func (s *Server) hideSignal(w http.ResponseWriter, r *http.Request) {
	row, ok := s.rowFromPath(w, r)
	if !ok {
		return
	}
	row.Hidden = !row.Hidden
	if err := s.Store.SetSignalHidden(r.Context(), who(r), row); err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "row", row)
}

func (s *Server) deleteSignal(w http.ResponseWriter, r *http.Request) {
	row, ok := s.rowFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteSignal(r.Context(), who(r), row); err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	// Пустой ответ: HTMX заменит строку «ничем», то есть уберёт её
}

// ---------- Помощники ----------

// rowFromPath достаёт сигнал по {id} из адреса; при ошибке сам отвечает клиенту
func (s *Server) rowFromPath(w http.ResponseWriter, r *http.Request) (store.SignalRow, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "неверный номер сигнала", http.StatusBadRequest)
		return store.SignalRow{}, false
	}
	row, err := s.Store.Signal(r.Context(), id)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return row, false
	}
	return row, true
}

// signalFromForm читает поля формы и проверяет их так же, как при запуске.
// Даже при ошибке возвращает введённые значения — чтобы не стирать их в форме.
// signalFromForm читает поля формы и проверяет их так же, как при запуске.
// Даже при ошибке возвращает введённые значения — чтобы не стирать их в форме.
func signalFromForm(r *http.Request) (store.SignalRow, error) {
	if err := r.ParseForm(); err != nil {
		return store.SignalRow{}, err
	}
	f := r.PostForm
	row := store.SignalRow{
		Name:    strings.TrimSpace(f.Get("name")),
		Addr:    strings.TrimSpace(f.Get("addr")),
		Type:    f.Get("type"),
		Unit:    strings.TrimSpace(f.Get("unit")),
		Kind:    f.Get("kind"),
		Message: strings.TrimSpace(f.Get("message")),
	}
	row.DictID, _ = strconv.ParseInt(f.Get("dict"), 10, 64) // нет поля или «—» — 0, без справочника
	if row.Kind == "" {
		row.Kind = store.KindTrend
	}
	if !store.ValidKind(row.Kind) {
		return row, errors.New("неизвестный тип сигнала")
	}

	var err error
	if row.PeriodMs, err = strconv.ParseInt(strings.TrimSpace(f.Get("period_ms")), 10, 64); err != nil {
		return row, errors.New("период: введите целое число миллисекунд")
	}
	if row.Scale, err = parseNum(f.Get("scale"), 1); err != nil {
		return row, errors.New("коэффициент: введите число, например 0.1")
	}
	if row.Bias, err = parseNum(f.Get("bias"), 0); err != nil {
		return row, errors.New("смещение: введите число, например -40")
	}
	err = checkSignal(&row)
	return row, err
}

// checkSignal — общие правила для сигнала: и для формы «Новый сигнал», и для массового
// добавления. Приводит адрес к обычной записи (db11.dbw90 → DB11.DBW90).
func checkSignal(row *store.SignalRow) error {
	// Аварии и предупреждения — дискретные, и человеку нужен понятный текст
	if row.Kind != store.KindTrend {
		// BOOL — бит аварии; целое число — номер ошибки (0 — норма). REAL — нет:
		// у дробного «не ноль» не означает аварию (0.0001 от шума — уже не 0)
		if row.Type == "REAL" {
			return errors.New("авария и предупреждение — BOOL (бит) или целое число с кодом ошибки (INT, WORD, DINT…), но не REAL")
		}
		if row.Message == "" {
			return errors.New("для аварии и предупреждения укажите текст сообщения")
		}
	}

	// Справочник «число → текст» — только у целых: у бита два значения, у REAL кодов не бывает
	if row.DictID != 0 && (row.Type == "BOOL" || row.Type == "REAL") {
		return errors.New("справочник — только для целых чисел (INT, WORD, DINT…)")
	}

	sig, err := plc.NewSignal(row.Name, row.Addr, plc.DataType(row.Type),
		time.Duration(row.PeriodMs)*time.Millisecond)
	if err != nil {
		return err
	}
	row.Addr = sig.Addr.String()
	return nil
}

// parseNum понимает и точку, и запятую: "0,1" и "0.1"
func parseNum(s string, def float64) (float64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return def, nil
	}
	return strconv.ParseFloat(s, 64)
}

// friendly переводит ошибки базы на понятный язык
func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return errors.New("сигнал не найден — возможно, его уже удалили")
	case strings.Contains(err.Error(), "UNIQUE"):
		return errors.New("сигнал с таким именем у этого ПЛК уже есть")
	default:
		return fmt.Errorf("ошибка базы: %w", err)
	}
}
