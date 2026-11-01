package web

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- План опроса: как сигналы ПЛК читаются запросами (38) ----------
//
// На странице «Сигналы» видно: сколько сигналов опрашивается, сколько запросов к ПЛК
// это даёт и почему сигналы разделились на несколько запросов. План считается той же
// функцией plc.BuildGroups, что и настоящий опрос, — расхождений быть не может.
//
// Заодно — сигналы с одинаковым адресом: это почти всегда опечатка (два сигнала читают
// одно и то же место, а то, что должно было читаться вторым, не видно совсем).

type planBlock struct {
	DB, From, To, Size int
	Signals            int
	Why                string // почему это отдельный запрос, а не часть предыдущего
}

type planGroup struct {
	Period  string // «100 мс»
	Signals int
	Blocks  []planBlock
}

type planDup struct {
	Addr  string
	Names string
}

type planView struct {
	Total, Enabled int
	Requests       int    // запросов за один круг опроса (все группы)
	PerSec         string // запросов к ПЛК в секунду
	MaxSize        int    // больше этого блок не делаем — поэтому большой буфер делится
	Groups         []planGroup
	Dups           []planDup
	Bad            []string // сигналы, которые опрос пропустит: адрес или тип не разбираются
	Rate, MaxDur   string   // как опрос идёт на самом деле (из состояния опроса)
}

// pollPlan — план опроса по строкам базы (как его строит служба: только включённые сигналы)
func pollPlan(rows []store.SignalRow) planView {
	v := planView{Total: len(rows), MaxSize: plc.DefaultMaxSize}
	var sigs []plc.Signal
	byAddr := map[string][]string{}
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		v.Enabled++
		s, err := plc.NewSignal(r.Name, r.Addr, plc.DataType(r.Type), time.Duration(r.PeriodMs)*time.Millisecond)
		if err != nil {
			v.Bad = append(v.Bad, r.Name+": "+err.Error())
			continue
		}
		sigs = append(sigs, s)
		a := s.Addr.String()
		byAddr[a] = append(byAddr[a], r.Name)
	}

	perSec := 0.0
	for _, g := range plc.BuildGroups(sigs, plc.DefaultMaxGap, plc.DefaultMaxSize) {
		pg := planGroup{Period: fmtPeriod(g.Period), Signals: len(g.Signals)}
		for i, b := range g.Blocks {
			pb := planBlock{DB: b.DB, From: b.Start, To: b.Start + b.Size - 1, Size: b.Size, Signals: len(b.Signals)}
			if i > 0 {
				pb.Why = splitReason(g.Blocks[i-1], b)
			}
			pg.Blocks = append(pg.Blocks, pb)
		}
		v.Requests += len(g.Blocks)
		if g.Period > 0 {
			perSec += float64(len(g.Blocks)) * float64(time.Second) / float64(g.Period)
		}
		v.Groups = append(v.Groups, pg)
	}
	v.PerSec = strings.Replace(fmt.Sprintf("%.4g", perSec), ".", ",", 1)

	for a, names := range byAddr {
		if len(names) > 1 {
			v.Dups = append(v.Dups, planDup{Addr: a, Names: strings.Join(names, ", ")})
		}
	}
	sort.Slice(v.Dups, func(i, j int) bool { return v.Dups[i].Addr < v.Dups[j].Addr })
	return v
}

// splitReason — почему блок b не склеился с предыдущим (те же правила, что в plc.BuildGroups)
func splitReason(prev, b plc.Block) string {
	if prev.DB != b.DB {
		return "другой DB"
	}
	if gap := b.Start - (prev.Start + prev.Size); gap > plc.DefaultMaxGap {
		return fmt.Sprintf("пропуск %d байт между адресами (склеиваем до %d)", gap, plc.DefaultMaxGap)
	}
	return fmt.Sprintf("вместе было бы больше %d байт — предел одного запроса", plc.DefaultMaxSize)
}

func fmtPeriod(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d мс", d.Milliseconds())
	}
	return strings.Replace(fmt.Sprintf("%g с", d.Seconds()), ".", ",", 1)
}
