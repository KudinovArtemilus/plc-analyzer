package archive

import "sort"

const snapshotChunk = 1000 // точек в одной порции файла-слепка

// Extract вырезает из файла выбранные сигналы за промежуток [from, to].
// Возвращает новый заголовок (только нужные сигналы) и порции для записи.
func Extract(f *File, names []string, from, to int64) (Header, []Chunk) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	h := f.Header // копия структуры; группы соберём заново
	h.Groups = nil
	var chunks []Chunk

	for gi, gInfo := range f.Header.Groups {
		// Какие сигналы этой группы нужны
		var idx []int
		var sigs []SignalInfo
		for si, s := range gInfo.Signals {
			if want[s.Name] {
				idx = append(idx, si)
				sigs = append(sigs, s)
			}
		}
		if len(idx) == 0 {
			continue
		}
		newG := len(h.Groups)
		h.Groups = append(h.Groups, GroupInfo{PeriodMs: gInfo.PeriodMs, Signals: sigs})

		// Нужный кусок времени
		gd := f.Groups[gi]
		lo := sort.Search(len(gd.Times), func(i int) bool { return gd.Times[i] >= from })
		hi := sort.Search(len(gd.Times), func(i int) bool { return gd.Times[i] > to })

		for start := lo; start < hi; start += snapshotChunk {
			end := min(start+snapshotChunk, hi)
			c := Chunk{Group: newG, Times: gd.Times[start:end], Values: make([][]float64, len(idx))}
			for k, si := range idx {
				c.Values[k] = gd.Values[si][start:end]
			}
			chunks = append(chunks, c)
		}
	}
	return h, chunks
}
