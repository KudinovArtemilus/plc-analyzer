package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/events"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Автоматический слепок по аварии ----------
//
// Слепок собирается из буфера Live (последние 10 минут всех сигналов ПЛК),
// а не из файлов смены: так не важно, попала ли авария на границу смены или файлов.

const (
	snapBefore = 5 * time.Minute      // записать до первой аварии лавины
	snapAfter  = 2 * time.Minute      // и после последней
	snapMax    = window - time.Minute // не длиннее буфера Live (с запасом в минуту)
	snapSettle = time.Second          // подождать, пока последние точки дойдут до буфера
)

// ringChunks — точки буферов Live за [from, to] в виде порций для файла .pla.
// nsig[g] — сколько сигналов в группе g (точки другой длины — от старой конфигурации, пропускаем).
func ringChunks(rings []*buffer.Ring, nsig []int, from, to time.Time) []archive.Chunk {
	f, t := from.UnixMilli(), to.UnixMilli()
	var out []archive.Chunk
	for gi, r := range rings {
		c := archive.Chunk{Group: gi, Values: make([][]float64, nsig[gi])}
		for _, p := range r.Since(f - 1) { // Since отдаёт точки НОВЕЕ момента, поэтому f-1
			if p.T > t {
				break // точки идут от старых к новым — дальше только позже
			}
			if len(p.V) != nsig[gi] {
				continue
			}
			c.Times = append(c.Times, p.T)
			for s, v := range p.V {
				c.Values[s] = append(c.Values[s], v)
			}
		}
		if len(c.Times) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// snapshotText — название и комментарий слепка: что за аварии и когда пришли
func snapshotText(w *events.Window) (name, comment string) {
	first := w.Alarms[0].Bit
	name = "Авария: " + first.Message
	if first.Message == "" {
		name = "Авария: " + first.Signal
	}
	if n := len(w.Alarms); n > 1 {
		name += fmt.Sprintf(" (+%d)", n-1)
	}
	lines := make([]string, len(w.Alarms))
	for i, e := range w.Alarms {
		lines[i] = e.At.Format("15:04:05.000") + "  " + e.Bit.Signal + " — " + e.Bit.Message
	}
	return name, strings.Join(lines, "\n")
}

// saveAlarmSnapshot пишет слепок на диск и в базу. Запускается в отдельной горутине:
// запись файла не должна задерживать опрос ПЛК.
func saveAlarmSnapshot(ctx context.Context, st *store.Store, pl store.PLC, gen int,
	groups []archive.GroupInfo, chunks []archive.Chunk, w *events.Window) {

	sh := archive.ShiftOf(w.From)
	h := archive.Header{
		PLC: pl.Name, ShiftDate: sh.Date.Format("2006-01-02"), Shift: sh.Name,
		Gen: gen, Created: time.Now(), Groups: groups,
	}
	// A_… — «авария», чтобы отличать от ручных S_…; миллисекунды — чтобы имена не совпали
	rel := path.Join("snapshots", "A_"+w.Alarms[0].At.Format("20060102-150405.000")+".pla")
	full := filepath.Join(dataRoot, archive.SafeName(pl.Name), filepath.FromSlash(rel))

	if err := writePla(full, h, chunks); err != nil {
		os.Remove(full) // недописанный файл не оставляем
		log.Printf("[%s] слепок по аварии: не удалось записать файл: %v", pl.Name, err)
		return
	}

	var names []string
	for _, g := range groups {
		for _, s := range g.Signals {
			names = append(names, s.Name)
		}
	}
	name, comment := snapshotText(w)
	_, err := st.AddSnapshot(ctx, "система", store.Snapshot{
		PLCID: pl.ID, Name: name, Category: "авария", Comment: comment,
		File: rel, FromMs: w.From.UnixMilli(), ToMs: w.To.UnixMilli(), Signals: names,
	})
	if err != nil {
		os.Remove(full) // без записи в базе файл никто не найдёт
		log.Printf("[%s] слепок по аварии: не удалось записать в базу: %v", pl.Name, err)
		return
	}
	log.Printf("[%s] слепок по аварии: %s, %s — %s", pl.Name, name,
		w.From.Format("15:04:05"), w.To.Format("15:04:05"))
}

func writePla(path string, h archive.Header, chunks []archive.Chunk) error {
	wr, err := archive.Create(path, h)
	if err != nil {
		return err
	}
	for _, c := range chunks {
		if err := wr.WriteChunk(c); err != nil {
			wr.Close()
			return err
		}
	}
	return wr.Close()
}
