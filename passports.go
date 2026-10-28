package main

import (
	"context"
	"log"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/passport"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Паспорта циклов в фоне (34a-3) ----------
//
// Раз в 10 минут: для каждого ПЛК, у которого на странице «Шаги» сохранена настройка
// паспорта, посчитать паспорта законченных смен, у которых их ещё нет.
// При первом запуске так посчитаются все смены, что уже лежат на диске (до 30 дней), —
// история признаков появляется сразу, а не через месяц.

const (
	passportFirst = time.Minute      // первый проход — через минуту: запуск службы не нагружаем
	passportEvery = 10 * time.Minute // дальше — так часто
)

// runPassports — фоновая работа до остановки ctx
func runPassports(ctx context.Context, st *store.Store, root string) {
	t := time.NewTimer(passportFirst)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		updatePassports(ctx, st, root)
		t.Reset(passportEvery)
	}
}

// updatePassports — один проход по всем настроенным ПЛК
func updatePassports(ctx context.Context, st *store.Store, root string) {
	ps, err := st.Passports(ctx)
	if err != nil {
		log.Printf("паспорта: %v", err)
		return
	}
	if len(ps) == 0 {
		return // ни у одного ПЛК не настроен
	}
	plcs, err := st.PLCs(ctx)
	if err != nil {
		log.Printf("паспорта: %v", err)
		return
	}
	names := make(map[int64]string, len(plcs))
	for _, p := range plcs {
		names[p.ID] = p.Name
	}

	for _, p := range ps {
		name, ok := names[p.PLCID]
		if !ok {
			continue
		}
		cfg := passport.Config{Step: p.Step, Mode: p.Mode, ModeVal: p.ModeVal, Home: p.Home}
		t0 := time.Now()
		n, err := passport.Update(ctx, root, name, cfg, p.Saved)
		if n > 0 {
			log.Printf("паспорта [%s]: посчитано смен: %d за %v", name, n, time.Since(t0).Round(time.Millisecond))
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("паспорта [%s]: %v", name, err)
		}
	}
}
