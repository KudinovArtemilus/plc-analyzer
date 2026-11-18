// Package tgnotify — уведомления об авариях в Telegram (шаг 45d).
//
// Смотрит в журнал аварий (таблица events — туда их пишет детектор, как и раньше) и готовит
// тексты сообщений: «авария пришла» — со звуком, «авария ушла» — тихо. Отправляет их бот (tgbot).
// Журнал — единственный источник: ни опрос, ни детектор ради уведомлений не меняются.
//
// Правила:
//   - только аварии (не предупреждения), только станки с «показывать» и «уведомлять»;
//   - зашунтированная авария молчит (шунт и ставят, чтобы она не мешала);
//   - после запуска — только новые: старые аварии из журнала не присылаются;
//   - сообщения уходят сразу (проверка журнала — каждые 2 с); всё, что пришло между проверками,
//     — одним сообщением;
//   - лавина: по станку не больше Burst сообщений в минуту, дальше копится и уходит одним.
package tgnotify

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Note — одно сообщение
type Note struct {
	Text   string
	Silent bool // без звука: в сообщении только «ушла»
}

// Burst — сколько сообщений по одному станку можно за Window; дальше — копим до конца окна.
// Обычная авария (пришла, ушла) — 2 сообщения, обе приходят сразу. Раньше была пауза
// в минуту после каждого сообщения, и «ушла» на пробах опаздывала до минуты.
const (
	Burst  = 3
	Window = time.Minute
)

// ListMax — сколько аварий перечислять в одном сообщении; остальные — «и ещё N»
const ListMax = 10

type pending struct {
	came []store.Event
	went []store.Event
}

// Watcher — помнит, что уже объявлено. Один на программу; вызывается из горутины бота.
type Watcher struct {
	st        *store.Store
	lastID    int64                 // последняя запись журнала, которую уже видели
	announced map[int64]int64       // объявленные активные аварии: номер записи → ПЛК
	pending   map[int64]*pending    // ждут конца паузы по станку
	lastSent  map[int64][]time.Time // когда по станку были сообщения за последнюю минуту
	started   bool
}

func New(st *store.Store) *Watcher {
	return &Watcher{st: st, announced: map[int64]int64{}, pending: map[int64]*pending{}, lastSent: map[int64][]time.Time{}}
}

// Notes — что отправить сейчас
func (w *Watcher) Notes(ctx context.Context, now time.Time) ([]Note, error) {
	if !w.started { // первый вызов — запомнить, где журнал сейчас; старое не присылаем
		id, err := w.st.LastEventID(ctx)
		if err != nil {
			return nil, err
		}
		w.lastID, w.started = id, true
		return nil, nil
	}

	cfg := map[int64]store.TGMachine{} // настройки станков — один раз за вызов
	notify := func(plcID int64) (store.TGMachine, bool) {
		m, ok := cfg[plcID]
		if !ok {
			m, _ = w.st.TGMachine(ctx, plcID)
			cfg[plcID] = m
		}
		return m, m.Enabled && m.Notify
	}

	// Новые аварии
	evs, err := w.st.EventsAfter(ctx, w.lastID, 1000)
	if err != nil {
		return nil, err
	}
	shelved := map[int64]map[string]bool{}
	for _, e := range evs {
		w.lastID = e.ID
		if e.Kind != store.KindAlarm {
			continue
		}
		if _, ok := notify(e.PLCID); !ok {
			continue
		}
		if shelved[e.PLCID] == nil {
			shelved[e.PLCID] = map[string]bool{}
			shs, _ := w.st.ActiveShelves(ctx, e.PLCID, now)
			for _, sh := range shs {
				shelved[e.PLCID][sh.Signal] = true
			}
		}
		if shelved[e.PLCID][e.Signal] {
			continue
		}
		w.at(e.PLCID).came = append(w.at(e.PLCID).came, e)
		if e.Active() {
			w.announced[e.ID] = e.PLCID
		} else {
			w.at(e.PLCID).went = append(w.at(e.PLCID).went, e) // пришла и ушла между проверками
		}
	}

	// Ушли ли объявленные
	ids := make([]int64, 0, len(w.announced))
	for id := range w.announced {
		ids = append(ids, id)
	}
	gone, err := w.st.EventsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	found := map[int64]bool{}
	for _, e := range gone {
		found[e.ID] = true
		if !e.Active() {
			delete(w.announced, e.ID)
			if _, ok := notify(e.PLCID); ok {
				w.at(e.PLCID).went = append(w.at(e.PLCID).went, e)
			}
		}
	}
	for id := range w.announced {
		if !found[id] {
			delete(w.announced, id) // записи нет — ПЛК удалили вместе с журналом
		}
	}

	// Сообщения по станкам, у которых кончилась пауза
	plcs := make([]int64, 0, len(w.pending))
	for id := range w.pending {
		plcs = append(plcs, id)
	}
	slices.Sort(plcs)
	var out []Note
	text := w.codeTexts(ctx)
	for _, id := range plcs {
		p := w.pending[id]
		recent := w.lastSent[id][:0:0]
		for _, t := range w.lastSent[id] {
			if now.Sub(t) < Window {
				recent = append(recent, t)
			}
		}
		w.lastSent[id] = recent
		if len(recent) >= Burst {
			continue // лавина — копим до конца окна
		}
		m, ok := notify(id)
		if !ok { // галочку сняли, пока копили
			delete(w.pending, id)
			continue
		}
		name := m.Label
		if name == "" {
			if pl, err := w.st.PLC(ctx, id); err == nil {
				name = pl.Name
			}
		}
		out = append(out, Format(name, p.came, p.went, text))
		w.lastSent[id] = append(w.lastSent[id], now)
		delete(w.pending, id)
	}
	return out, nil
}

func (w *Watcher) at(plcID int64) *pending {
	p := w.pending[plcID]
	if p == nil {
		p = &pending{}
		w.pending[plcID] = p
	}
	return p
}

// Format — текст сообщения: каждая строка — «Оборудование — ошибка».
//
//	🔴 Шлифовка — Дверь открыта
//	🔴 Шлифовка — Ошибка привода: Перегрев двигателя (код 7501)
//	✅ Шлифовка — Дверь открыта: ушла, длилась 3 мин
//
// Время не пишем: оно и так у сообщения в Telegram (уходит через 2–3 с после аварии).
// text — текст ошибки для записи журнала (с подписью кода из справочника); nil — как в журнале.
func Format(machine string, came, went []store.Event, text func(store.Event) string) Note {
	if text == nil {
		text = func(e store.Event) string { return e.Message }
	}
	var lines []string
	add := func(line string) bool {
		if len(lines) == ListMax {
			return false
		}
		lines = append(lines, line)
		return true
	}
	shown := 0
	for _, e := range came {
		if !add("🔴 " + machine + " — " + text(e)) {
			break
		}
		shown++
	}
	for _, e := range went {
		if !add("✅ " + machine + " — " + text(e) + ": ушла, длилась " + Duration(e.Went.Sub(e.Came))) {
			break
		}
		shown++
	}
	if rest := len(came) + len(went) - shown; rest > 0 {
		lines = append(lines, fmt.Sprintf("… и ещё %d", rest))
	}
	return Note{Text: strings.Join(lines, "\n"), Silent: len(came) == 0}
}

// codeSep — так детектор дописывает код к тексту аварии: «Ошибка привода: код 7501» (events/detector.go)
const codeSep = ": код "

// codeTexts — подпись кода из справочника сигнала: «Ошибка привода: Перегрев двигателя (код 7501)».
// Справочника или строки в нём нет — текст как в журнале.
func (w *Watcher) codeTexts(ctx context.Context) func(store.Event) string {
	dicts := map[int64]map[string]int64{} // ПЛК → сигнал → справочник
	items := map[int64]map[int64]string{} // справочник → код → текст
	return func(e store.Event) string {
		i := strings.LastIndex(e.Message, codeSep)
		if i < 0 {
			return e.Message
		}
		code, err := strconv.ParseInt(e.Message[i+len(codeSep):], 10, 64)
		if err != nil {
			return e.Message
		}
		if dicts[e.PLCID] == nil {
			dicts[e.PLCID] = map[string]int64{}
			sigs, _ := w.st.Signals(ctx, e.PLCID)
			for _, sg := range sigs {
				dicts[e.PLCID][sg.Name] = sg.DictID
			}
		}
		d := dicts[e.PLCID][e.Signal]
		if d == 0 {
			return e.Message
		}
		if items[d] == nil {
			items[d] = map[int64]string{}
			list, _ := w.st.DictItems(ctx, d)
			for _, it := range list {
				items[d][it.Value] = it.Text
			}
		}
		if t := items[d][code]; t != "" {
			return fmt.Sprintf("%s: %s (код %d)", e.Message[:i], t, code)
		}
		return e.Message
	}
}

// Duration — «меньше минуты», «12 мин», «2 ч 5 мин»
func Duration(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case d < time.Minute:
		return "меньше минуты"
	case m < 60:
		return fmt.Sprintf("%d мин", m)
	case m%60 == 0:
		return fmt.Sprintf("%d ч", m/60)
	}
	return fmt.Sprintf("%d ч %d мин", m/60, m%60)
}
