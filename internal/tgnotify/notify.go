// Package tgnotify — уведомления об авариях в Telegram (шаг 45d).
//
// Смотрит в журнал аварий (таблица events — туда их пишет детектор, как и раньше) и готовит
// тексты сообщений: «авария пришла» — со звуком, «авария ушла» — тихо. Отправляет их бот (tgbot).
// Журнал — единственный источник: ни опрос, ни детектор ради уведомлений не меняются.
//
// Правила:
//   - только аварии (не предупреждения), только станки с «показывать» и «уведомлять»;
//   - зашунтированная авария молчит (шунт и ставят, чтобы она не мешала);
//   - о каждой активной аварии — один раз, даже если она висела до запуска уведомлений;
//     что уже сообщено, помнит база: перезапуск службы ничего не повторяет;
//   - сообщения уходят сразу (проверка журнала — каждые 2 с); всё, что пришло между проверками,
//     — одним сообщением;
//   - лавина: по станку не больше Burst сообщений в минуту, дальше копится и уходит одним.
package tgnotify

import (
	"context"
	"fmt"
	"log"
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
	told      map[string]bool // о чём уже написали в лог (чтобы не повторять на каждой аварии)
}

// logOnce — строка в лог один раз за работу службы: почему авария не ушла в Telegram
func (w *Watcher) logOnce(key, format string, args ...any) {
	if !w.told[key] {
		w.told[key] = true
		log.Printf("telegram: "+format, args...)
	}
}

func (w *Watcher) plcName(ctx context.Context, id int64) string {
	if pl, err := w.st.PLC(ctx, id); err == nil {
		return pl.Name
	}
	return fmt.Sprint("ПЛК ", id)
}

func New(st *store.Store) *Watcher {
	return &Watcher{st: st, announced: map[int64]int64{}, pending: map[int64]*pending{}, lastSent: map[int64][]time.Time{}, told: map[string]bool{}}
}

// Notes — что отправить сейчас.
//
// Два источника, чтобы не потерять ни одной аварии:
//  1. активные аварии сейчас: есть, а о ней ещё не сообщали — «пришла»; сообщали, а её уже нет — «ушла».
//     Так приходят и аварии, которые висели ДО запуска уведомлений или до того, как у станка
//     поставили галочку «уведомлять», — один раз;
//  2. новые записи журнала: авария пришла и ушла между проверками (короче 2 с) — тоже сообщаем.
//
// Что уже объявлено, хранится в базе (закладка): перезапуск службы не повторяет старое.
func (w *Watcher) Notes(ctx context.Context, now time.Time) ([]Note, error) {
	if !w.started {
		last, active, ok, err := w.st.TGNotifyState(ctx)
		if err != nil {
			return nil, err
		}
		if !ok { // уведомления включились впервые: журнал — с текущего места, активные — сообщим
			if last, err = w.st.LastEventID(ctx); err != nil {
				return nil, err
			}
		}
		w.lastID, w.started = last, true
		for _, id := range active {
			w.announced[id] = 0 // ПЛК узнаем, когда авария уйдёт
		}
		log.Printf("telegram: уведомления об авариях: журнал с записи №%d, уже сообщено о %d активных", last, len(active))
	}
	before := fmt.Sprint(w.lastID, w.announcedIDs())

	cfg := map[int64]store.TGMachine{} // настройки станков — один раз за вызов
	notify := func(plcID int64) (store.TGMachine, bool) {
		m, ok := cfg[plcID]
		if !ok {
			m, _ = w.st.TGMachine(ctx, plcID)
			cfg[plcID] = m
		}
		return m, m.Enabled && m.Notify
	}
	shelved := map[int64]map[string]bool{}
	isShelved := func(e store.Event) bool {
		if shelved[e.PLCID] == nil {
			shelved[e.PLCID] = map[string]bool{}
			shs, _ := w.st.ActiveShelves(ctx, e.PLCID, now)
			for _, sh := range shs {
				shelved[e.PLCID][sh.Signal] = true
			}
		}
		return shelved[e.PLCID][e.Signal]
	}

	// 1. Активные аварии станков с «показывать» и «уведомлять»
	plcs, err := w.st.PLCs(ctx)
	if err != nil {
		return nil, err
	}
	active := map[int64]bool{}
	for _, pl := range plcs {
		if _, ok := notify(pl.ID); !ok {
			continue
		}
		evs, err := w.st.ActiveEvents(ctx, pl.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.Kind != store.KindAlarm || isShelved(e) {
				continue
			}
			active[e.ID] = true
			if _, done := w.announced[e.ID]; !done {
				w.announced[e.ID] = e.PLCID
				w.at(e.PLCID).came = append(w.at(e.PLCID).came, e)
			}
		}
	}

	// 2. Новые записи журнала: короткие аварии и объяснения в лог, почему не сообщаем
	evs, err := w.st.EventsAfter(ctx, w.lastID, 1000)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		w.lastID = e.ID
		if e.Kind != store.KindAlarm {
			w.logOnce(fmt.Sprint("warn", e.PLCID), "%s: предупреждения в Telegram не присылаются, только аварии (первое: «%s»)",
				w.plcName(ctx, e.PLCID), e.Message)
			continue
		}
		if m, ok := notify(e.PLCID); !ok {
			w.logOnce(fmt.Sprint("off", e.PLCID), "%s: авария «%s» не отправлена — на странице «Telegram» у станка не стоит %s",
				w.plcName(ctx, e.PLCID), e.Message, missing(m))
			continue
		}
		if isShelved(e) {
			log.Printf("telegram: %s: авария «%s» зашунтирована — не отправляю", w.plcName(ctx, e.PLCID), e.Message)
			continue
		}
		if _, done := w.announced[e.ID]; !done && !e.Active() { // пришла и ушла между проверками
			p := w.at(e.PLCID)
			p.came, p.went = append(p.came, e), append(p.went, e)
		}
	}

	// Объявленные, которых среди активных больше нет, — ушли (или их зашунтировали, сняли галочку)
	var gone []int64
	for id := range w.announced {
		if !active[id] {
			gone = append(gone, id)
		}
	}
	recs, err := w.st.EventsByID(ctx, gone)
	if err != nil {
		return nil, err
	}
	for _, e := range recs {
		if _, ok := notify(e.PLCID); ok && !e.Active() && !isShelved(e) {
			w.at(e.PLCID).went = append(w.at(e.PLCID).went, e)
		}
	}
	for _, id := range gone {
		delete(w.announced, id)
	}
	if after := fmt.Sprint(w.lastID, w.announcedIDs()); after != before {
		if err := w.st.SaveTGNotifyState(ctx, w.lastID, w.announcedIDs()); err != nil {
			return nil, err
		}
	}

	// Сообщения по станкам, у которых кончилась пауза
	ready := make([]int64, 0, len(w.pending))
	for id := range w.pending {
		ready = append(ready, id)
	}
	slices.Sort(ready)
	var out []Note
	text := w.codeTexts(ctx)
	for _, id := range ready {
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

// missing — каких галочек не хватает
func missing(m store.TGMachine) string {
	switch {
	case !m.Enabled && !m.Notify:
		return "«показывать» и «уведомлять об авариях»"
	case !m.Enabled:
		return "«показывать»"
	}
	return "«уведомлять об авариях»"
}

// announcedIDs — номера объявленных аварий по порядку (для закладки в базе)
func (w *Watcher) announcedIDs() []int64 {
	ids := make([]int64, 0, len(w.announced))
	for id := range w.announced {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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
