package web

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

type plcFormView struct {
	PLC store.PLC
	Err string
}

// ---------- Страница ----------

func (s *Server) plcsPage(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "plcs", map[string]any{
		"PLCs": list,
		"Form": plcFormView{PLC: store.PLC{Rack: 0, Slot: 2}},
	})
}

// ---------- Добавление ----------

func (s *Server) addPLC(w http.ResponseWriter, r *http.Request) {
	p, err := plcFromForm(r)
	if err == nil {
		err = s.checkPLC(r, 0, p)
	}
	if err == nil {
		_, err = s.Store.AddPLC(r.Context(), who(r), p)
		err = friendly(err)
	}
	if err != nil {
		render(w, "plcaddform", plcFormView{PLC: p, Err: err.Error()})
		return
	}
	w.Header().Set("HX-Refresh", "true")
}

// ---------- Строка: показать, изменить, сохранить ----------

func (s *Server) plcRow(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.plcFromPath(w, r); ok {
		render(w, "plcrow", p)
	}
}

func (s *Server) plcEditRow(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.plcFromPath(w, r); ok {
		render(w, "plceditrow", plcFormView{PLC: p})
	}
}

func (s *Server) updatePLC(w http.ResponseWriter, r *http.Request) {
	old, ok := s.plcFromPath(w, r)
	if !ok {
		return
	}
	p, err := plcFromForm(r)
	p.ID = old.ID

	connChanged := p.IP != old.IP || p.Rack != old.Rack || p.Slot != old.Slot
	if err == nil {
		err = s.checkPLC(r, old.ID, p)
	}
	if err == nil && p.Name != old.Name && connChanged {
		err = errors.New("меняйте название и параметры связи по отдельности: сначала одно — сохранить — потом другое")
	}
	if err == nil && p != old {
		err = friendly(s.Store.UpdatePLC(r.Context(), who(r), old, p))
	}
	if err != nil {
		render(w, "plceditrow", plcFormView{PLC: p, Err: err.Error()})
		return
	}
	render(w, "plcrow", p)
}

// ---------- Состояние ПЛК (обновляется каждые 2 с) ----------

func (s *Server) plcState(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	v := struct {
		Running            bool
		St                 plc.Status
		Rec                archive.Status
		Down, Max, RecSize string
	}{}
	if s.Units != nil {
		v.St, v.Rec, v.Running = s.Units(id)
	}
	v.Down = time.Since(v.St.ChangedAt).Round(time.Second).String()
	v.Max = v.St.MaxDur.Round(time.Millisecond).String()
	v.RecSize = fmtBytes(v.Rec.Bytes)
	render(w, "plcstate", v)
}

// ---------- Помощники ----------

func (s *Server) plcFromPath(w http.ResponseWriter, r *http.Request) (store.PLC, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "неверный номер ПЛК", http.StatusBadRequest)
		return store.PLC{}, false
	}
	p, err := s.Store.PLC(r.Context(), id)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return p, false
	}
	return p, true
}

// plcFromForm читает и проверяет поля ПЛК. При ошибке возвращает введённое.
func plcFromForm(r *http.Request) (store.PLC, error) {
	r.ParseForm()
	f := r.PostForm
	p := store.PLC{
		Name: strings.TrimSpace(f.Get("name")),
		IP:   strings.TrimSpace(f.Get("ip")),
	}
	var errRack, errSlot error
	p.Rack, errRack = strconv.Atoi(strings.TrimSpace(f.Get("rack")))
	p.Slot, errSlot = strconv.Atoi(strings.TrimSpace(f.Get("slot")))

	switch {
	case p.Name == "":
		return p, errors.New("укажите название")
	case utf8.RuneCountInString(p.Name) > 64:
		return p, errors.New("название длиннее 64 символов")
	case errRack != nil || p.Rack < 0 || p.Rack > 7:
		return p, errors.New("rack — число от 0 до 7 (обычно 0)")
	case errSlot != nil || p.Slot < 0 || p.Slot > 31:
		return p, errors.New("slot — число от 0 до 31 (S7-300: 2, S7-1200/1500: 1)")
	}

	ip := net.ParseIP(p.IP)
	if ip == nil || ip.To4() == nil {
		return p, errors.New("IP-адрес должен быть вида 192.168.0.10")
	}
	p.IP = ip.To4().String() // единый вид: убираем пробелы и лишние нули
	return p, nil
}

// checkPLC — имя не занято и этот контроллер ещё не добавлен (selfID — свой ПЛК, его пропускаем)
func (s *Server) checkPLC(r *http.Request, selfID int64, p store.PLC) error {
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.ID == selfID {
			continue
		}
		if strings.EqualFold(o.Name, p.Name) {
			return errors.New("такое название уже у другого ПЛК")
		}
		if o.IP == p.IP && o.Rack == p.Rack && o.Slot == p.Slot {
			return fmt.Errorf("этот контроллер уже добавлен под названием «%s»", o.Name)
		}
	}
	return nil
}
