package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/web"
)

// unit — всё, что работает для одного ПЛК: опрос, буфер Live и запись на диск.
// Внутренности (буферы, номер конфигурации) принадлежат горутине run.
// Снаружи блоком управляют только через notify и shutdown.
type unit struct {
	st       *store.Store
	poller   *plc.Poller
	rec      *archive.Recorder
	onConfig func(gen int, groups []web.Group) // показать на страницах (nil — не показывать)

	reload chan struct{}
	cancel context.CancelFunc
	done   chan struct{} // закрывается, когда блок полностью остановлен

	mu sync.Mutex
	pl store.PLC // текущие настройки ПЛК
}

func startUnit(parent context.Context, st *store.Store, pl store.PLC,
	onConfig func(int, []web.Group)) *unit {

	ctx, cancel := context.WithCancel(parent)
	u := &unit{
		st:       st,
		poller:   &plc.Poller{IP: pl.IP, Rack: pl.Rack, Slot: pl.Slot},
		rec:      archive.NewRecorder(dataRoot, pl.Name, keepDays),
		onConfig: onConfig,
		reload:   make(chan struct{}, 1),
		cancel:   cancel,
		done:     make(chan struct{}),
		pl:       pl,
	}
	go u.run(ctx)
	return u
}

// notify — «перечитай настройки». Не блокирует.
func (u *unit) notify() {
	select {
	case u.reload <- struct{}{}:
	default:
	}
}

// shutdown останавливает блок и ждёт, пока он закроет соединение и допишет данные
func (u *unit) shutdown() {
	u.cancel()
	<-u.done
}

func (u *unit) settings() store.PLC {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pl
}

// connChanged — изменились параметры связи, значит, опрос нужно перезапустить
func (u *unit) connChanged(pl store.PLC) bool {
	cur := u.settings()
	return cur.IP != pl.IP || cur.Rack != pl.Rack || cur.Slot != pl.Slot
}

func (u *unit) run(ctx context.Context) {
	defer close(u.done)

	recDone := make(chan struct{})
	go func() {
		u.rec.Run(ctx)
		close(recDone)
	}()

	samples := make(chan plc.Sample, 100)
	pollDone := make(chan struct{})
	go func() {
		u.poller.Run(ctx, samples)
		close(pollDone)
	}()

	gen := 0
	var rings []*buffer.Ring
	var lastKey string // «отпечаток» настроек: не изменились — не перестраиваем

	apply := func() {
		pl := u.settings()
		fresh, err := u.st.PLC(ctx, pl.ID)
		if err != nil {
			log.Printf("[%s] не удалось прочитать ПЛК: %v", pl.Name, err)
			return
		}
		rows, err := u.st.Signals(ctx, pl.ID)
		if err != nil {
			log.Printf("[%s] не удалось прочитать сигналы: %v", pl.Name, err)
			return
		}

		// Менялись сигналы ДРУГОГО ПЛК — у нас ничего не трогаем
		key := fmt.Sprintf("%s|%v", fresh.Name, rows)
		if key == lastKey {
			return
		}
		lastKey = key

		if fresh.Name != pl.Name {
			log.Printf("ПЛК переименован: %s → %s", pl.Name, fresh.Name)
			u.rec.Rename(fresh.Name)
		}
		u.mu.Lock()
		u.pl = fresh
		u.mu.Unlock()

		groups := buildGroups(fresh, rows)
		gen++
		rings = make([]*buffer.Ring, len(groups))
		webGroups := make([]web.Group, len(groups))
		for gi, g := range groups {
			rings[gi] = buffer.New(int(window / g.Period))
			names := make([]string, len(g.Signals))
			for i, s := range g.Signals {
				names[i] = s.Name
			}
			webGroups[gi] = web.Group{Period: g.Period, Names: names, Ring: rings[gi]}
		}
		u.rec.SetGroups(gen, groupInfos(groups))
		u.poller.SetGroups(groups, gen)
		if u.onConfig != nil {
			u.onConfig(gen, webGroups)
		}
		logGroups(fresh, gen, groups)
	}
	apply()

	for {
		select {
		case <-ctx.Done():
			<-pollDone // соединение с ПЛК закрыто
			<-recDone  // данные дописаны на диск
			return

		case s := <-samples:
			if s.Err == nil && s.Gen == gen {
				rings[s.Group].Add(buffer.Point{T: s.Time.UnixMilli(), V: s.Values})
				u.rec.Add(s.Gen, s.Group, s.Time, s.Values)
			}

		case <-u.reload:
			apply()
		}
	}
}

// ---------- Помощники (раньше жили в main.go) ----------

func buildGroups(pl store.PLC, rows []store.SignalRow) []plc.Group {
	var sigs []plc.Signal
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		s, err := toSignal(r)
		if err != nil {
			log.Printf("[%s] сигнал пропущен: %v", pl.Name, err)
			continue
		}
		sigs = append(sigs, s)
	}
	return plc.BuildGroups(sigs, plc.DefaultMaxGap, plc.DefaultMaxSize)
}

func toSignal(r store.SignalRow) (plc.Signal, error) {
	s, err := plc.NewSignal(r.Name, r.Addr, plc.DataType(r.Type),
		time.Duration(r.PeriodMs)*time.Millisecond)
	if err != nil {
		return plc.Signal{}, err
	}
	s.Scale, s.Bias, s.Unit = r.Scale, r.Bias, r.Unit
	return s, nil
}

func groupInfos(groups []plc.Group) []archive.GroupInfo {
	out := make([]archive.GroupInfo, len(groups))
	for gi, g := range groups {
		out[gi].PeriodMs = g.Period.Milliseconds()
		for _, s := range g.Signals {
			out[gi].Signals = append(out[gi].Signals, archive.SignalInfo{
				Name: s.Name, Addr: s.Addr.String(), Type: string(s.Type),
				Scale: s.Scale, Bias: s.Bias, Unit: s.Unit,
			})
		}
	}
	return out
}

func logGroups(pl store.PLC, gen int, groups []plc.Group) {
	log.Printf("ПЛК %s (%s), конфигурация %d: групп %d", pl.Name, pl.IP, gen, len(groups))
	if len(groups) == 0 {
		log.Printf("   [%s] нет включённых сигналов — опрос ждёт", pl.Name)
	}
	for gi, g := range groups {
		log.Printf("   группа %d: каждые %v, сигналов %d", gi, g.Period, len(g.Signals))
		for _, b := range g.Blocks {
			log.Printf("      блок DB%d байты %d..%d, сигналов: %d",
				b.DB, b.Start, b.Start+b.Size-1, len(b.Signals))
		}
	}
}
