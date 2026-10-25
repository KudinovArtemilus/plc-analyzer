package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// shelfHours — сроки шунта на выбор. Бессрочного нет (см. store.MaxShelf).
var shelfHours = []struct {
	Hours int
	Title string
}{
	{1, "1 час"}, {4, "4 часа"}, {12, "12 часов (смена)"}, {24, "сутки"}, {168, "7 дней"},
}

func validHours(h int) bool {
	for _, o := range shelfHours {
		if o.Hours == h {
			return true
		}
	}
	return false
}

// shelfForm — данные формы «зашунтировать»
type shelfForm struct {
	PLCID   int64
	Signal  string
	Message string
	Reason  string
	Hours   int
	Options any
	Err     string
}

// signalMessages — тексты аварий по имени сигнала (для формы и списка)
func (s *Server) signalMessages(r *http.Request, plcID int64) map[string]string {
	out := map[string]string{}
	if rows, err := s.Store.Signals(r.Context(), plcID); err == nil {
		for _, row := range rows {
			out[row.Name] = row.Message
		}
	}
	return out
}

// shelveForm — форма по кнопке «шунт» на панели аварий (GET /ui/shelve?plc=1&sig=AL1)
func (s *Server) shelveForm(w http.ResponseWriter, r *http.Request) {
	plcID := s.reqPLCID(r)
	sig := r.URL.Query().Get("sig")
	render(w, "shelveform", shelfForm{
		PLCID: plcID, Signal: sig, Message: s.signalMessages(r, plcID)[sig],
		Hours: 12, Options: shelfHours,
	})
}

// addShelf — POST /ui/shelves: поставить шунт
func (s *Server) addShelf(w http.ResponseWriter, r *http.Request) {
	plcID, _ := strconv.ParseInt(r.FormValue("plc"), 10, 64)
	hours, _ := strconv.Atoi(r.FormValue("hours"))
	f := shelfForm{
		PLCID: plcID, Signal: r.FormValue("sig"), Reason: strings.TrimSpace(r.FormValue("reason")),
		Hours: hours, Options: shelfHours,
	}
	f.Message = s.signalMessages(r, plcID)[f.Signal]

	if !validHours(hours) {
		f.Err = "выберите срок из списка"
		render(w, "shelveform", f)
		return
	}
	now := time.Now()
	_, err := s.Store.AddShelf(r.Context(), store.Shelf{
		PLCID: plcID, Signal: f.Signal, Reason: f.Reason, Who: who(r),
		From: now, Until: now.Add(time.Duration(hours) * time.Hour),
	})
	if err != nil {
		f.Err = err.Error()
		render(w, "shelveform", f)
		return
	}
	render(w, "shelvedone", map[string]any{
		"Signal": f.Signal, "Until": now.Add(time.Duration(hours) * time.Hour).Format("02.01 15:04"), "PLCID": plcID,
	})
}

// ---------- Список шунтов ----------

type shelfView struct {
	ID              int64
	Signal, Message string
	Reason, Who     string
	From, Until     string
	State           string // «действует · осталось 2 ч 10 мин» / «истёк» / «снят …»
	Active          bool
}

func (s *Server) shelfViews(r *http.Request, plcID int64) ([]shelfView, error) {
	now := time.Now()
	list, err := s.Store.RecentShelves(r.Context(), plcID, now.AddDate(0, 0, -30))
	if err != nil {
		return nil, err
	}
	msgs := s.signalMessages(r, plcID)
	out := make([]shelfView, 0, len(list))
	for _, sh := range list {
		v := shelfView{
			ID: sh.ID, Signal: sh.Signal, Message: msgs[sh.Signal], Reason: sh.Reason, Who: sh.Who,
			From: sh.From.Format("02.01 15:04"), Until: sh.Until.Format("02.01 15:04"),
			Active: sh.ActiveAt(now),
		}
		switch {
		case v.Active:
			v.State = "действует · осталось " + fmtDurMs(sh.Until.Sub(now).Truncate(time.Minute).Milliseconds())
		case !sh.Removed.IsZero():
			v.State = "снят " + sh.Removed.Format("02.01 15:04") + " (" + sh.RemovedBy + ")"
		default:
			v.State = "истёк — авария вернулась на панель"
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) shelvesPage(w http.ResponseWriter, r *http.Request) {
	pl, err := s.Store.PLC(r.Context(), s.reqPLCID(r))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	plcs, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	list, err := s.shelfViews(r, pl.ID)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "shelves", map[string]any{
		"PLC": pl, "PLCs": plcs,
		"Table": map[string]any{"List": list, "PLCID": pl.ID, "Err": ""}, // то же, что отдаёт removeShelf
	})
}

// removeShelf — POST /ui/shelves/{id}/remove: снять шунт; в ответ — обновлённая таблица
func (s *Server) removeShelf(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	plcID := s.reqPLCID(r)
	errText := ""
	if err := s.Store.RemoveShelf(r.Context(), who(r), id, time.Now()); err != nil {
		errText = err.Error()
	}
	list, err := s.shelfViews(r, plcID)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "shelflist", map[string]any{"List": list, "PLCID": plcID, "Err": errText})
}
