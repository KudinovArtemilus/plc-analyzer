package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/KudinovArtemilus/plc-analyzer/internal/events"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Тексты из справочников на страницах ----------
//
// Текст подставляется при показе, а не при записи события: описали код — и он сразу
// виден у висящей аварии и у старых строк журнала. И новая строка справочника
// не перестраивает опрос.

// dictLookup — справочники сигналов одного ПЛК: имя сигнала → справочник → число → текст
type dictLookup struct {
	plcID int64
	dict  map[string]int64
	texts map[int64]map[int64]string
}

// lookup — справочники сигналов ПЛК. Ошибка базы — пустой lookup: страница покажется
// без текстов, это лучше, чем без аварий.
func (s *Server) lookup(ctx context.Context, plcID int64) dictLookup {
	l := dictLookup{plcID: plcID, dict: map[string]int64{}}
	rows, err := s.Store.Signals(ctx, plcID)
	if err != nil {
		return l
	}
	var ids []int64
	for _, r := range rows {
		if r.DictID != 0 {
			l.dict[r.Name] = r.DictID
			ids = append(ids, r.DictID)
		}
	}
	l.texts, _ = s.Store.DictTexts(ctx, ids)
	return l
}

// text — текст числа v у сигнала; dictID — справочник сигнала (0 — справочника нет)
func (l dictLookup) text(signal string, v int64) (text string, dictID int64) {
	dictID = l.dict[signal]
	return l.texts[dictID][v], dictID // из nil-карты Go читает спокойно: пустая строка
}

// applyCode — у аварии с кодом ошибки: текст кода или ссылка «описать»
func (l dictLookup) applyCode(v *eventView) {
	code, ok := events.CodeOf(v.Message)
	if !ok {
		return
	}
	text, dictID := l.text(v.Signal, code)
	switch {
	case text != "":
		v.CodeText = text
	case dictID != 0:
		v.Describe = dictURL(dictID, l.plcID, strconv.FormatInt(code, 10))
	}
}

// dictTextsJSON — GET /api/dicts?plc=1: тексты для подписей значений на графиках.
// {"Step_table": {"12": "Зажим закрыт"}, …} — ключи JSON всегда строки.
func (s *Server) dictTextsJSON(w http.ResponseWriter, r *http.Request) {
	l := s.lookup(r.Context(), s.reqPLCID(r))
	out := map[string]map[string]string{}
	for sig, id := range l.dict {
		m := map[string]string{}
		for v, t := range l.texts[id] {
			m[strconv.FormatInt(v, 10)] = t
		}
		out[sig] = m
	}
	writeJSON(w, out)
}

// lookups — справочники по ПЛК для журнала, где строки разных ПЛК: каждый грузится один раз
type lookups struct {
	s   *Server
	ctx context.Context
	m   map[int64]dictLookup
}

func (ls *lookups) get(plcID int64) dictLookup {
	l, ok := ls.m[plcID]
	if !ok {
		l = ls.s.lookup(ls.ctx, plcID)
		ls.m[plcID] = l
	}
	return l
}

// для журнала и панели — store.Event знает свой ПЛК
func (ls *lookups) apply(v *eventView, e store.Event) { ls.get(e.PLCID).applyCode(v) }
