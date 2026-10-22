package plc

import (
	"cmp"
	"slices"
	"time"
)

const (
	DefaultMaxGap  = 64  // байт: дыру меньше этой читаем вместе с блоком
	DefaultMaxSize = 200 // байт: максимальный размер одного блока
)

// BuildGroups делит сигналы на группы по частоте,
// а внутри группы — на блоки для чтения одним запросом.
func BuildGroups(sigs []Signal, maxGap, maxSize int) []Group {
	// 1. Разложить сигналы по частоте
	byPeriod := map[time.Duration][]Signal{}
	for _, s := range sigs {
		byPeriod[s.Period] = append(byPeriod[s.Period], s)
	}

	// Частоты по возрастанию: быстрые группы первыми
	periods := make([]time.Duration, 0, len(byPeriod))
	for p := range byPeriod {
		periods = append(periods, p)
	}
	slices.Sort(periods)

	var groups []Group
	for _, p := range periods {
		list := byPeriod[p]

		// 2. Сортировка по DB, затем по номеру байта
		slices.SortFunc(list, func(a, b Signal) int {
			if a.Addr.DB != b.Addr.DB {
				return cmp.Compare(a.Addr.DB, b.Addr.DB)
			}
			return cmp.Compare(a.Addr.Byte, b.Addr.Byte)
		})

		// 3. Склеивание соседних сигналов в блоки
		g := Group{Period: p}
		var cur *Block
		for _, s := range list {
			end := s.Addr.Byte + s.Addr.Size()
			newBlock := cur == nil ||
				s.Addr.DB != cur.DB || // другой DB
				s.Addr.Byte-(cur.Start+cur.Size) > maxGap || // слишком большая дыра
				end-cur.Start > maxSize // блок стал бы слишком большим

			if newBlock {
				g.Blocks = append(g.Blocks, Block{DB: s.Addr.DB, Start: s.Addr.Byte})
				cur = &g.Blocks[len(g.Blocks)-1] // указатель берём ЗАНОВО после append
			}
			cur.Size = max(cur.Size, end-cur.Start)
			cur.Signals = append(cur.Signals, s)
			g.Signals = append(g.Signals, s)
		}
		groups = append(groups, g)
	}
	return groups
}
