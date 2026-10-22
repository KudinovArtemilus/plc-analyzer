package web

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

// historyCSV отдаёт все точки выбранных сигналов за промежуток [from, to] в CSV под русский Excel
func (s *Server) historyCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := s.openFile(r, q.Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to, ok := fileRange(f)
	if !ok {
		http.Error(w, "в файле нет данных", http.StatusNotFound)
		return
	}
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
		from = v
	}
	if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
		to = v
	}
	refs := pickSignals(f, q["s"])
	if len(refs) == 0 {
		http.Error(w, "не выбраны сигналы", http.StatusBadRequest)
		return
	}

	// Для каждой участвующей группы — где в ней начинается и кончается промежуток
	type cursor struct{ g, pos, end int }
	var curs []cursor
	groupCur := map[int]int{} // номер группы → индекс в curs
	for _, ref := range refs {
		if _, seen := groupCur[ref.g]; seen {
			continue
		}
		t := f.Groups[ref.g].Times
		lo := sort.Search(len(t), func(i int) bool { return t[i] >= from })
		hi := sort.Search(len(t), func(i int) bool { return t[i] > to })
		groupCur[ref.g] = len(curs)
		curs = append(curs, cursor{g: ref.g, pos: lo, end: hi})
	}

	name := fmt.Sprintf("%s_%s-%s.csv", archive.SafeName(f.Header.PLC),
		time.UnixMilli(from).Format("20060102_150405"), time.UnixMilli(to).Format("150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", attachment(name))

	bw := bufio.NewWriter(w)
	defer bw.Flush()
	bw.WriteString("\xEF\xBB\xBF") // метка UTF-8: Excel покажет русские буквы правильно

	cw := csv.NewWriter(bw)
	cw.Comma = ';'     // русский Excel ждёт точку с запятой
	cw.UseCRLF = true  // переводы строк как в Windows

	head := []string{"Время"}
	for _, ref := range refs {
		h := ref.Name
		if ref.Unit != "" {
			h += ", " + ref.Unit
		}
		head = append(head, h)
	}
	cw.Write(head)

	// Слияние по времени: каждый раз берём самое раннее время среди всех групп.
	// Сигналы, у которых в этот момент нет точки, получают пустую ячейку.
	row := make([]string, len(refs)+1)
	for {
		next := int64(math.MaxInt64)
		for _, c := range curs {
			if c.pos < c.end {
				next = min(next, f.Groups[c.g].Times[c.pos])
			}
		}
		if next == math.MaxInt64 {
			break // все группы кончились
		}

		row[0] = time.UnixMilli(next).Format("02.01.2006 15:04:05.000")
		for k, ref := range refs {
			c := curs[groupCur[ref.g]]
			row[k+1] = ""
			if c.pos < c.end && f.Groups[ref.g].Times[c.pos] == next {
				row[k+1] = csvNum(f.Groups[ref.g].Values[ref.i][c.pos])
			}
		}
		if err := cw.Write(row); err != nil {
			return // пользователь отменил скачивание
		}

		for i := range curs {
			c := &curs[i]
			if c.pos < c.end && f.Groups[c.g].Times[c.pos] == next {
				c.pos++
			}
		}
	}
	cw.Flush()
}

// csvNum: 85.3 → "85,3" (запятая — для русского Excel)
func csvNum(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// attachment — заголовок «скачать как файл» с русским именем (RFC 5987)
func attachment(name string) string {
	return `attachment; filename="export.csv"; filename*=UTF-8''` + url.PathEscape(name)
}
