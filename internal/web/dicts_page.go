package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Страница «Справочники»: «число → текст» (коды ошибок, шаги) ----------
//
// Обычные формы с переходом на страницу (POST → redirect → GET), без HTMX:
// строки добавляются по одной, глядя на ПЛК онлайн, — тут важна простота, а не плавность.
// ?plc= в адресе — только чтобы меню вело к тому же ПЛК; справочники общие для всех.

type dictsView struct {
	PLC   int64
	List  []store.Dict
	Name  string // что ввели в форму «новый справочник» (при ошибке не стираем)
	Err   string
	Dict  store.Dict       // открытый справочник (страница строк)
	Items []store.DictItem // его строки
	Value string           // форма строки: число…
	Text  string           // …и текст
}

// dictsPage — GET /dicts: список справочников и форма «новый»
func (s *Server) dictsPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.dictsView(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "dicts", v)
}

func (s *Server) dictsView(r *http.Request) (dictsView, error) {
	list, err := s.Store.Dicts(r.Context())
	return dictsView{PLC: s.reqPLCID(r), List: list}, err
}

// dictAdd — POST /dicts: новый справочник → сразу на его страницу
func (s *Server) dictAdd(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	id, err := s.Store.AddDict(r.Context(), who(r), name)
	if err != nil {
		v, _ := s.dictsView(r)
		v.Name, v.Err = name, err.Error()
		render(w, "dicts", v)
		return
	}
	http.Redirect(w, r, dictURL(id, s.reqPLCID(r), ""), http.StatusSeeOther)
}

// dictPage — GET /dicts/{id}?v=7501: строки справочника; v — подставить число в форму
// (так открывается ссылка «описать» с панели аварий, а ещё «изменить» у строки)
func (s *Server) dictPage(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dictFromPath(w, r)
	if !ok {
		return
	}
	v.Value = r.URL.Query().Get("v")
	// «изменить»: число уже есть — подставляем и его текст
	for _, it := range v.Items {
		if strconv.FormatInt(it.Value, 10) == v.Value {
			v.Text = it.Text
		}
	}
	render(w, "dict", v)
}

// dictItemSet — POST /dicts/{id}/items: добавить строку или заменить текст
func (s *Server) dictItemSet(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dictFromPath(w, r)
	if !ok {
		return
	}
	v.Value, v.Text = strings.TrimSpace(r.FormValue("value")), r.FormValue("text")
	n, err := strconv.ParseInt(v.Value, 10, 64)
	if err != nil {
		err = errors.New("число: целое, например 7501")
	} else {
		err = s.Store.SetDictItem(r.Context(), who(r), v.Dict.ID, store.DictItem{Value: n, Text: v.Text})
	}
	if err != nil {
		v.Err = err.Error()
		render(w, "dict", v)
		return
	}
	http.Redirect(w, r, dictURL(v.Dict.ID, v.PLC, ""), http.StatusSeeOther)
}

// dictItemDelete — POST /dicts/{id}/items/{v}/delete
func (s *Server) dictItemDelete(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dictFromPath(w, r)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("v"), 10, 64)
	if err == nil {
		err = s.Store.DeleteDictItem(r.Context(), who(r), v.Dict.ID, n)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) { // уже удалена (две вкладки) — не ошибка
		v.Err = err.Error()
		render(w, "dict", v)
		return
	}
	http.Redirect(w, r, dictURL(v.Dict.ID, v.PLC, ""), http.StatusSeeOther)
}

// dictDelete — POST /dicts/{id}/delete: справочник целиком
func (s *Server) dictDelete(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dictFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteDict(r.Context(), who(r), v.Dict); err != nil {
		v.Err = err.Error()
		render(w, "dict", v)
		return
	}
	http.Redirect(w, r, "/dicts?plc="+strconv.FormatInt(v.PLC, 10), http.StatusSeeOther)
}

// dictFromPath — справочник по {id} и его строки; при ошибке сам отвечает клиенту
func (s *Server) dictFromPath(w http.ResponseWriter, r *http.Request) (dictsView, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	v, err := s.dictsView(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return v, false
	}
	for _, d := range v.List {
		if d.ID == id {
			v.Dict = d
		}
	}
	if v.Dict.ID == 0 {
		http.Error(w, "справочник не найден — возможно, его удалили", http.StatusNotFound)
		return v, false
	}
	if v.Items, err = s.Store.DictItems(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return v, false
	}
	return v, true
}

// dictURL — адрес страницы справочника; value — подставить число в форму
func dictURL(id, plc int64, value string) string {
	u := "/dicts/" + strconv.FormatInt(id, 10) + "?plc=" + strconv.FormatInt(plc, 10)
	if value != "" {
		u += "&v=" + value
	}
	return u
}
