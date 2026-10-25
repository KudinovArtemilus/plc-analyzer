package web

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

type plcView struct {
	PLC store.PLC
	Err string
}

func (s *Server) showPLC(w http.ResponseWriter, r *http.Request) {
	pl, err := s.Store.PLC(r.Context(), s.defaultPLCID(r.Context()))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "plcname", pl)
}

func (s *Server) editPLC(w http.ResponseWriter, r *http.Request) {
	pl, err := s.Store.PLC(r.Context(), s.defaultPLCID(r.Context()))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "plcedit", plcView{PLC: pl})
}

func (s *Server) savePLC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pl, err := s.Store.PLC(ctx, s.defaultPLCID(ctx))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	r.ParseForm()
	newName := strings.TrimSpace(r.PostForm.Get("name"))

	err = s.checkPLCName(r, pl, newName)
	if err == nil && newName != pl.Name {
		err = friendly(s.Store.RenamePLC(ctx, who(r), pl.ID, pl.Name, newName))
	}
	if err != nil {
		render(w, "plcedit", plcView{PLC: store.PLC{ID: pl.ID, Name: newName, IP: pl.IP}, Err: err.Error()})
		return
	}
	pl.Name = newName
	render(w, "plcname", pl)
}

// checkPLCName — имя не пустое, не длинное и не занято другим ПЛК
func (s *Server) checkPLCName(r *http.Request, pl store.PLC, name string) error {
	if name == "" {
		return errors.New("имя не может быть пустым")
	}
	if utf8.RuneCountInString(name) > 64 {
		return errors.New("имя длиннее 64 символов")
	}
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.ID != pl.ID && strings.EqualFold(other.Name, name) {
			return errors.New("такое имя уже у другого ПЛК — их данные попали бы в одну папку")
		}
	}
	return nil
}
