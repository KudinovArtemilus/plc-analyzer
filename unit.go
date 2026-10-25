package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/events"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/web"
)

// unit — всё, что работает для одного ПЛК: опрос, буфер Live, запись на диск и аварии.
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
	var dets []*events.Detector   // по детектору аварий на группу опроса
	var infos []archive.GroupInfo // описание групп для файлов .pla (и слепков по аварии)
	snap := &events.Avalanche{Before: snapBefore, After: snapAfter, Max: snapMax}
	var snapWG sync.WaitGroup // записи слепков, которые ещё идут (дождаться при остановке)

	// writeSnap — собрать слепок из буфера Live сейчас (в горутине run, где живут буферы),
	// а записать на диск — в отдельной горутине, чтобы не задерживать опрос
	writeSnap := func(w *events.Window) {
		nsig := make([]int, len(infos))
		for gi, g := range infos {
			nsig[gi] = len(g.Signals)
		}
		chunks := ringChunks(rings, nsig, w.From, w.To)
		if len(chunks) == 0 {
			return
		}
		pl, g, gi := u.settings(), gen, infos
		snapWG.Add(1)
		go func() {
			defer snapWG.Done()
			// WithoutCancel: при остановке программы слепок всё равно дописывается до конца
			saveAlarmSnapshot(context.WithoutCancel(ctx), u.st, pl, g, gi, chunks, w)
		}()
	}
	snapTick := time.NewTicker(time.Second)
	defer snapTick.Stop()
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
		dets = u.buildDetectors(ctx, fresh, rows, groups)
		kindOf := make(map[string]string, len(rows)) // имя сигнала → тип (тренд / авария / предупреждение)
		for _, r := range rows {
			kindOf[r.Name] = r.Kind
		}
		rings = make([]*buffer.Ring, len(groups))
		webGroups := make([]web.Group, len(groups))
		for gi, g := range groups {
			rings[gi] = buffer.New(int(window / g.Period))
			names := make([]string, len(g.Signals))
			kinds := make([]string, len(g.Signals))
			for i, s := range g.Signals {
				names[i] = s.Name
				kinds[i] = kindOf[s.Name]
			}
			webGroups[gi] = web.Group{Period: g.Period, Names: names, Kinds: kinds, Ring: rings[gi]}
		}
		infos = groupInfos(groups)
		u.rec.SetGroups(gen, infos)
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
			<-pollDone    // соединение с ПЛК закрыто
			<-recDone     // данные дописаны на диск
			snapWG.Wait() // слепки по аварии дописаны
			return

		case s := <-samples:
			if s.Err == nil && s.Gen == gen {
				rings[s.Group].Add(buffer.Point{T: s.Time.UnixMilli(), V: s.Values})
				u.rec.Add(s.Gen, s.Group, s.Time, s.Values)
				if edges := dets[s.Group].Update(s.Time, s.Values); len(edges) > 0 {
					u.saveEdges(ctx, edges)
					for _, w := range u.snapEdges(ctx, snap, edges) {
						writeSnap(w)
					}
				}
			}
			// При ошибке связи детекторы НЕ трогаем: значения неизвестны, а не «0».
			// Связь вернётся — сравним с тем, что было до обрыва.

		case <-u.reload:
			apply()

		case now := <-snapTick.C:
			if w := snap.Due(now, snapSettle); w != nil {
				writeSnap(w)
			}
		}
	}
}

// ---------- Аварии ----------

// buildDetectors — детекторы аварий для новой конфигурации групп.
// Заодно закрывает в базе активные аварии по сигналам, которые больше не аварии
// (выключены, удалены, переименованы или сменили тип).
func (u *unit) buildDetectors(ctx context.Context, pl store.PLC, rows []store.SignalRow,
	groups []plc.Group) []*events.Detector {

	byName := make(map[string]store.SignalRow, len(rows))
	for _, r := range rows {
		byName[r.Name] = r
	}

	// Аварии, открытые в базе: программу перезапустили или поменяли настройки во время аварии
	active := map[string]bool{}
	if open, err := u.st.ActiveEvents(ctx, pl.ID); err != nil {
		log.Printf("[%s] аварии: не удалось прочитать активные: %v", pl.Name, err)
	} else {
		for _, e := range open {
			active[e.Signal] = true
		}
	}

	dets := make([]*events.Detector, len(groups))
	var watched []string
	for gi, g := range groups {
		var bits []events.Bit
		for i, s := range g.Signals {
			r := byName[s.Name]
			if r.Kind != store.KindAlarm && r.Kind != store.KindWarning {
				continue
			}
			bits = append(bits, events.Bit{Index: i, Signal: s.Name, Kind: r.Kind, Message: r.Message})
			watched = append(watched, s.Name)
		}
		dets[gi] = events.NewDetector(bits, active)
	}

	if n, err := u.st.CloseEventsExcept(ctx, pl.ID, watched, time.Now()); err != nil {
		log.Printf("[%s] аварии: не удалось закрыть снятые с контроля: %v", pl.Name, err)
	} else if n > 0 {
		log.Printf("[%s] аварии: закрыто %d (сигналы сняты с контроля)", pl.Name, n)
	}
	if len(watched) > 0 {
		log.Printf("[%s] аварии: под наблюдением %d битов", pl.Name, len(watched))
	}
	return dets
}

// saveEdges — записать фронты в журнал и в лог программы
func (u *unit) saveEdges(ctx context.Context, edges []events.Edge) {
	pl := u.settings()
	rows := make([]store.EventEdge, len(edges))
	for i, e := range edges {
		rows[i] = store.EventEdge{
			Signal: e.Bit.Signal, Kind: e.Bit.Kind, Message: e.Bit.Message, On: e.On, At: e.At,
		}
		what := "АВАРИЯ"
		if e.Bit.Kind == store.KindWarning {
			what = "предупреждение"
		}
		state := "пришла"
		if !e.On {
			state = "ушла"
		}
		log.Printf("[%s] %s %s: %s — %s", pl.Name, what, state, e.Bit.Signal, e.Bit.Message)
	}
	if err := u.st.SaveEdges(ctx, pl.ID, rows); err != nil {
		log.Printf("[%s] аварии: не удалось записать в журнал: %v", pl.Name, err)
	}
}

// snapEdges — какие из фронтов запускают слепок по аварии: только «пришла», только аварии
// (не предупреждения), не зашунтированные и не «висевшие при запуске». Возвращает слепки,
// которые пришлось закрыть досрочно (лавина стала длиннее snapMax).
func (u *unit) snapEdges(ctx context.Context, snap *events.Avalanche, edges []events.Edge) []*events.Window {
	var cand []events.Edge
	for _, e := range edges {
		if e.On && !e.Initial && e.Bit.Kind == store.KindAlarm {
			cand = append(cand, e)
		}
	}
	if len(cand) == 0 {
		return nil
	}
	shelved := map[string]bool{}
	if list, err := u.st.ActiveShelves(ctx, u.settings().ID, time.Now()); err == nil {
		for _, sh := range list {
			shelved[sh.Signal] = true
		}
	}
	var done []*events.Window
	for _, e := range cand {
		if shelved[e.Bit.Signal] {
			continue
		}
		if w := snap.Add(e); w != nil {
			done = append(done, w)
		}
	}
	return done
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
