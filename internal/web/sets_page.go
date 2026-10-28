package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Страница «Наборы»: именованные наборы сигналов («окна графиков») ----------
//
// Как «Справочники»: обычные формы, POST → redirect → GET. Набор относится к ПЛК (?plc=).

type setsView struct {
	PLC     store.PLC
	PLCs    []store.PLC
	List    []store.SignalSet
	Name    string // форма «новый набор» (при ошибке не стираем введённое)
	Err     string
	Set     store.SignalSet   // открытый набор
	Signals []store.SignalRow // все сигналы ПЛК — галочки
	In      map[int64]bool    // какие отмечены
}

// setsData — общее для обеих страниц: ПЛК, список ПЛК, наборы
func (s *Server) setsData(r *http.Request) (setsView, error) {
	var v setsView
	var err error
	if v.PLC, err = s.pagePLC(r); err != nil {
		return v, errors.New("ПЛК не найден")
	}
	if v.PLCs, err = s.Store.PLCs(r.Context()); err != nil {
		return v, err
	}
	v.List, err = s.Store.Sets(r.Context(), v.PLC.ID)
	return v, err
}

// setsPage — GET /sets?plc=: наборы ПЛК и форма «новый набор»
func (s *Server) setsPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.setsData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	render(w, "sets", v)
}

// setAdd — POST /sets?plc=: новый набор → сразу на его страницу, отмечать сигналы
func (s *Server) setAdd(w http.ResponseWriter, r *http.Request) {
	v, err := s.setsData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	name := r.FormValue("name")
	id, err := s.Store.AddSet(r.Context(), who(r), v.PLC.ID, name)
	if err != nil {
		v.Name, v.Err = name, err.Error()
		render(w, "sets", v)
		return
	}
	http.Redirect(w, r, setURL(id, v.PLC.ID), http.StatusSeeOther)
}

// setPage — GET /sets/{id}?plc=: имя и галочки сигналов
func (s *Server) setPage(w http.ResponseWriter, r *http.Request) {
	v, ok := s.setFromPath(w, r)
	if !ok {
		return
	}
	render(w, "set", v)
}

// setSave — POST /sets/{id}?plc=: сохранить имя и отмеченные сигналы
func (s *Server) setSave(w http.ResponseWriter, r *http.Request) {
	v, ok := s.setFromPath(w, r)
	if !ok {
		return
	}
	r.ParseForm()
	v.Set.Name = r.PostForm.Get("name")
	v.Set.Signals = nil
	v.In = map[int64]bool{}
	for _, str := range r.PostForm["sig"] { // у галочек одно имя — значения приходят списком
		if id, err := strconv.ParseInt(str, 10, 64); err == nil {
			v.Set.Signals = append(v.Set.Signals, id)
			v.In[id] = true
		}
	}
	if err := s.Store.SaveSet(r.Context(), who(r), v.Set); err != nil {
		v.Err = err.Error()
		render(w, "set", v) // отмеченное не теряется: форма покажет то, что прислали
		return
	}
	http.Redirect(w, r, "/sets?plc="+strconv.FormatInt(v.PLC.ID, 10), http.StatusSeeOther)
}

// setDelete — POST /sets/{id}/delete
func (s *Server) setDelete(w http.ResponseWriter, r *http.Request) {
	v, ok := s.setFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteSet(r.Context(), who(r), v.Set); err != nil && !errors.Is(err, store.ErrNotFound) {
		v.Err = err.Error()
		render(w, "set", v)
		return
	}
	http.Redirect(w, r, "/sets?plc="+strconv.FormatInt(v.PLC.ID, 10), http.StatusSeeOther)
}

// setFromPath — набор по {id}, сигналы его ПЛК и отметки; при ошибке сам отвечает клиенту
func (s *Server) setFromPath(w http.ResponseWriter, r *http.Request) (setsView, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	set, err := s.Store.Set(r.Context(), id)
	if err != nil {
		http.Error(w, "набор не найден — возможно, его удалили", http.StatusNotFound)
		return setsView{}, false
	}
	// ПЛК страницы — тот, которому принадлежит набор, что бы ни стояло в ?plc=
	q := r.URL.Query()
	q.Set("plc", strconv.FormatInt(set.PLCID, 10))
	r.URL.RawQuery = q.Encode()

	v, err := s.setsData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return v, false
	}
	v.Set = set
	if v.Signals, err = s.Store.Signals(r.Context(), set.PLCID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return v, false
	}
	v.In = make(map[int64]bool, len(set.Signals))
	for _, sid := range set.Signals {
		v.In[sid] = true
	}
	return v, true
}

func setURL(id, plc int64) string {
	return "/sets/" + strconv.FormatInt(id, 10) + "?plc=" + strconv.FormatInt(plc, 10)
}

// setsJSON — GET /api/sets?plc=: наборы для Live, «Истории» и слепков.
// Только имена сигналов: на графиках и в файлах записи сигнал — это имя.
func (s *Server) setsJSON(w http.ResponseWriter, r *http.Request) {
	type setJSON struct {
		ID    int64    `json:"id"`
		Name  string   `json:"name"`
		Names []string `json:"names"`
	}
	list, err := s.Store.Sets(r.Context(), s.reqPLCID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]setJSON, 0, len(list)) // пустой список — [] в JSON, а не null
	for _, set := range list {
		names := set.Names
		if names == nil {
			names = []string{}
		}
		out = append(out, setJSON{ID: set.ID, Name: set.Name, Names: names})
	}
	writeJSON(w, out)
}
