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
//   - лавина: по станку не чаще раза в минуту; всё, что пришло за это время, — одним сообщением.
package tgnotify

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Note — одно сообщение
type Note struct {
	Text   string
	Silent bool // без звука: в сообщении только «ушла»
}

// Cooldown — по одному станку не чаще одного сообщения за это время
const Cooldown = time.Minute

// ListMax — сколько аварий перечислять в одном сообщении; остальные — «и ещё N»
const ListMax = 10

type pending struct {
	came []store.Event
	went []store.Event
}

// Watcher — помнит, что уже объявлено. Один на программу; вызывается из горутины бота.
type Watcher struct {
	st        *store.Store
	lastID    int64               // последняя запись журнала, которую уже видели
	announced map[int64]int64     // объявленные активные аварии: номер записи → ПЛК
	pending   map[int64]*pending  // ждут конца паузы по станку
	lastSent  map[int64]time.Time // когда по станку было последнее сообщение
	started   bool
}

func New(st *store.Store) *Watcher {
	return &Watcher{st: st, announced: map[int64]int64{}, pending: map[int64]*pending{}, lastSent: map[int64]time.Time{}}
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
	for _, id := range plcs {
		p := w.pending[id]
		if now.Sub(w.lastSent[id]) < Cooldown {
			continue // ещё пауза — копим
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
		out = append(out, Format(name, p.came, p.went))
		w.lastSent[id] = now
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

// Format — текст сообщения:
//
//	🔴 Шлифовка: Дверь открыта — 09:58
//
//	🔴 Шлифовка: аварий — 3
//	■ Дверь открыта — 09:58
//	■ …
//	✅ ушла: Перегрев масла — длилась 12 мин
func Format(machine string, came, went []store.Event) Note {
	var b strings.Builder
	switch {
	case len(came) == 1:
		fmt.Fprintf(&b, "🔴 %s: %s — %s\n", machine, came[0].Message, came[0].Came.Format("15:04"))
	case len(came) > 1:
		fmt.Fprintf(&b, "🔴 %s: аварий — %d\n", machine, len(came))
		for i, e := range came {
			if i == ListMax {
				fmt.Fprintf(&b, "… и ещё %d\n", len(came)-ListMax)
				break
			}
			fmt.Fprintf(&b, "■ %s — %s\n", e.Message, e.Came.Format("15:04"))
		}
	default:
		fmt.Fprintf(&b, "%s\n", machine)
	}
	for i, e := range went {
		if i == ListMax {
			fmt.Fprintf(&b, "… и ещё ушло %d\n", len(went)-ListMax)
			break
		}
		fmt.Fprintf(&b, "✅ ушла: %s — длилась %s\n", e.Message, Duration(e.Went.Sub(e.Came)))
	}
	return Note{Text: strings.TrimRight(b.String(), "\n"), Silent: len(came) == 0}
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
