package web

import (
	"cmp"
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
	Free               int    // сколько байт можно добавить в конец запроса, не создавая новый
	FreeFrom           int    // с какого байта свободно (сразу за последним адресом)
	FreeHint           string // «или 5 REAL/DINT, или 10 INT/WORD, или 160 BOOL»
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
	PDU            int    // размер PDU из настройки ПЛК (от него MaxSize)
	Hint           string // ПЛК при подключении дал другой PDU — что поменять
	Groups         []planGroup
	Dups           []planDup
	Bad            []string // сигналы, которые опрос пропустит: адрес или тип не разбираются
	Rate, MaxDur   string   // как опрос идёт на самом деле (из состояния опроса)
}

// pollPlan — план опроса по строкам базы (как его строит служба: только включённые сигналы).
// pdu — из настройки ПЛК: от него размер одного запроса (plc.MaxBlock).
func pollPlan(rows []store.SignalRow, pdu int) planView {
	v := planView{Total: len(rows), PDU: cmp.Or(pdu, store.DefaultPDU), MaxSize: plc.MaxBlock(pdu)}
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
	for _, g := range plc.BuildGroups(sigs, plc.DefaultMaxGap, v.MaxSize) {
		pg := planGroup{Period: fmtPeriod(g.Period), Signals: len(g.Signals)}
		for i, b := range g.Blocks {
			pb := planBlock{DB: b.DB, From: b.Start, To: b.Start + b.Size - 1, Size: b.Size, Signals: len(b.Signals)}
			if i > 0 {
				pb.Why = splitReason(g.Blocks[i-1], b, v.MaxSize)
			}
			// Запрос может вырасти до предела — но не дальше начала следующего запроса в том же DB
			limit := b.Start + v.MaxSize
			if i+1 < len(g.Blocks) && g.Blocks[i+1].DB == b.DB {
				limit = min(limit, g.Blocks[i+1].Start)
			}
			pb.FreeFrom = b.Start + b.Size
			pb.Free, pb.FreeHint = freeSpace(pb.FreeFrom, limit)
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
func splitReason(prev, b plc.Block, maxSize int) string {
	if prev.DB != b.DB {
		return "другой DB"
	}
	if gap := b.Start - (prev.Start + prev.Size); gap > plc.DefaultMaxGap {
		return fmt.Sprintf("пропуск %d байт между адресами (склеиваем до %d)", gap, plc.DefaultMaxGap)
	}
	return fmt.Sprintf("вместе было бы больше %d байт — предел одного запроса", maxSize)
}

// pduHint — сравнить PDU из настройки (set) с тем, что ПЛК дал при подключении (got).
// Пусто — всё сходится или ПЛК ещё не подключался.
func pduHint(set, got int) string {
	if got <= 0 {
		return ""
	}
	set = cmp.Or(set, store.DefaultPDU)
	switch {
	case plc.MaxBlock(got) < plc.MaxBlock(set):
		// Опрос работает: gos7 сама разрежет большой блок, — но обменов больше, чем показано здесь
		return fmt.Sprintf("ПЛК при подключении дал PDU %d байт, а в настройке ПЛК указано %d. "+
			"Опрос работает (библиотека сама дорежет запросы), но обменов с ПЛК больше, чем в этой таблице. "+
			"Поставьте PDU %d на странице «ПЛК».", got, set, got)
	case plc.MaxBlock(got) > plc.MaxBlock(set):
		return fmt.Sprintf("ПЛК при подключении дал PDU %d байт, а в настройке указано %d. "+
			"Можно поставить %d на странице «ПЛК»: запросы станут крупнее, а их — меньше.",
			got, set, min(got, plc.PDUMax))
	}
	return ""
}

// freeSpace — что ещё влезет в запрос: байты с from (первый свободный) до limit (не включая).
// В неоптимизированном DB INT, DINT и REAL стоят только с чётного байта: если первый свободный
// нечётный, для них один байт пропадает (TIA вставит пустой). Биты — в любой байт, по 8 в байте.
func freeSpace(from, limit int) (int, string) {
	free := limit - from
	if free <= 0 {
		return 0, ""
	}
	even := limit - (from + from%2) // свободно с чётного байта — для INT, DINT, REAL
	return free, fmt.Sprintf("или %d REAL/DINT, или %d INT/WORD, или %d BOOL", even/4, even/2, free*8)
}

func fmtPeriod(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d мс", d.Milliseconds())
	}
	return strings.Replace(fmt.Sprintf("%g с", d.Seconds()), ".", ",", 1)
}
