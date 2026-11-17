package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Участки на странице «ПЛК» (шаг 45a-1) ----------
//
// Участок — группа ПЛК. Список участков — блок над таблицей ПЛК; участок у ПЛК
// выбирается в его строке («изменить»). Менять можно только с ПК анализатора —
// все запросы здесь не GET, их и так пропускает только guard (access.go).

// areaBlock — блок участков: список и ошибка последнего действия
type areaBlock struct {
	Areas []store.Area
	Err   string
	Name  string // введённое в «новый участок» — не стирать при ошибке
	Edit  int64  // участок, который сейчас переименовывают (строка с полем ввода)
}

// areasBlock — GET /ui/areas[?edit=id]: блок участков; с edit — строка участка с полем «название»
func (s *Server) areasBlock(w http.ResponseWriter, r *http.Request) {
	edit, _ := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64)
	render(w, "areas", areaBlock{Areas: s.areas(r), Edit: edit})
}

// areas — список участков для форм; не прочитался — форма просто без выбора
func (s *Server) areas(r *http.Request) []store.Area {
	list, _ := s.Store.Areas(r.Context())
	return list
}

// areaDone — после удачного действия перерисовать страницу (названия участков есть и в строках ПЛК),
// после неудачного — только блок с причиной
func (s *Server) areaDone(w http.ResponseWriter, r *http.Request, err error, name string, edit int64) {
	if err == nil {
		w.Header().Set("HX-Refresh", "true")
		return
	}
	if strings.Contains(err.Error(), "UNIQUE") {
		err = errors.New("участок с таким названием уже есть")
	} else if !errors.Is(err, store.ErrAreaName) {
		err = friendly(err)
	}
	render(w, "areas", areaBlock{Areas: s.areas(r), Err: err.Error(), Name: name, Edit: edit})
}

// addArea — POST /ui/areas (name)
func (s *Server) addArea(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	_, err := s.Store.AddArea(r.Context(), who(r), name)
	s.areaDone(w, r, err, name, 0)
}

// renameArea — PUT /ui/areas/{id} (name). Не через hx-prompt: он шлёт текст в заголовке
// HTTP, а кириллицу в заголовок браузер не пропускает.
func (s *Server) renameArea(w http.ResponseWriter, r *http.Request) {
	a, ok := s.areaFromPath(w, r)
	if !ok {
		return
	}
	// Ошибка — строка остаётся в режиме правки: не надо заново жать «переименовать»
	s.areaDone(w, r, s.Store.RenameArea(r.Context(), who(r), a, r.FormValue("name")), "", a.ID)
}

// moveArea — POST /ui/areas/{id}/move?d=-1|1
func (s *Server) moveArea(w http.ResponseWriter, r *http.Request) {
	a, ok := s.areaFromPath(w, r)
	if !ok {
		return
	}
	d := 1
	if r.URL.Query().Get("d") == "-1" {
		d = -1
	}
	s.areaDone(w, r, s.Store.MoveArea(r.Context(), who(r), a.ID, d), "", 0)
}

// deleteArea — DELETE /ui/areas/{id}; ПЛК участка становятся «без участка»
func (s *Server) deleteArea(w http.ResponseWriter, r *http.Request) {
	a, ok := s.areaFromPath(w, r)
	if !ok {
		return
	}
	s.areaDone(w, r, s.Store.DeleteArea(r.Context(), who(r), a), "", 0)
}

func (s *Server) areaFromPath(w http.ResponseWriter, r *http.Request) (store.Area, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "неверный номер участка", http.StatusBadRequest)
		return store.Area{}, false
	}
	a, err := s.Store.Area(r.Context(), id)
	if err != nil {
		s.areaDone(w, r, errors.New("участок не найден — возможно, его уже удалили"), "", 0)
		return a, false
	}
	return a, true
}
