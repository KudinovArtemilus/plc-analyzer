// Package ask — вопросы к боту обычными словами, без ИИ (шаг 45f).
//
// «давление шлифовка 10 мин», «график давления на прессе за час», «статистика масла за смену» —
// из фразы достаём станок, сигналы, период и что нужно (график, статистика). Простой разбор:
// без регистра, ё = е, падежи и опечатки в одну букву прощаются. Без интернета и без ИИ:
// фраза никуда не уходит, всё считается на анализаторе.
//
// Здесь только разбор текста — ни базы, ни файлов. Что спрашивать можно (станки и сигналы),
// передаёт вызывающий: это станки с «показывать» и сигналы с их карточки на странице «Telegram».
package ask

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Signal — сигнал, о котором можно спросить
type Signal struct {
	ID    int64
	Name  string // имя в анализаторе
	Label string // подпись на телефоне
	Type  string // BOOL, INT, REAL…
}

// Machine — станок, о котором можно спросить: несколько имён (подпись, имя ПЛК, участок)
type Machine struct {
	PLCID   int64
	Title   string   // как называть в ответе
	Names   []string // по чему узнавать в тексте
	Signals []Signal
}

// Query — что спросили
type Query struct {
	Machine *Machine
	Signals []Signal
	Minutes int  // за сколько минут до «сейчас»
	Shift   bool // за текущую смену (Minutes тогда не важен)
	Chart   bool
	Stats   bool
}

// Result — разбор: либо вопрос понятен, либо Problem и варианты для подсказки
type Result struct {
	Query
	Problem string   // пусто — всё понятно
	Options []string // что можно выбрать (станки или сигналы)
}

// DefaultMinutes — период, если в вопросе его нет
const DefaultMinutes = 60

// MaxMinutes — дальше смены не смотрим (12 ч): записи бот читает из файлов смены
const MaxMinutes = 12 * 60

// MaxSignals — больше сигналов в одном ответе не даём (иначе график нечитаемый)
const MaxSignals = 4

// служебные слова: не станок и не сигнал
var stop = map[string]bool{
	"дай": true, "дайте": true, "покажи": true, "покажите": true, "пришли": true, "скинь": true, "какой": true,
	"какая": true, "какое": true, "какие": true, "что": true, "как": true, "было": true, "был": true, "была": true,
	"за": true, "на": true, "по": true, "в": true, "во": true, "с": true, "со": true, "и": true, "а": true, "у": true,
	"последние": true, "последний": true, "последнюю": true, "последних": true, "прошлые": true, "мне": true,
	"пожалуйста": true, "плиз": true, "сейчас": true, "текущую": true, "текущая": true, "эту": true, "эта": true,
	"график": true, "графики": true, "графика": true, "статистика": true, "статистику": true, "статистикой": true,
	"стат": true, "данные": true, "значения": true, "станок": true, "станка": true, "станке": true,
	"сигнал": true, "сигналы": true, "сигнала": true,
}

// Parse — разобрать вопрос
func Parse(text string, machines []Machine) Result {
	words := tokens(text)
	r := Result{Query: Query{Minutes: DefaultMinutes}}

	// Что нужно: график, статистика; ничего не сказано — и то и другое
	for _, w := range words {
		if strings.HasPrefix(w, "граф") {
			r.Chart = true
		}
		if strings.HasPrefix(w, "стат") {
			r.Stats = true
		}
	}
	if !r.Chart && !r.Stats {
		r.Chart, r.Stats = true, true
	}

	// Период; слова периода дальше в поиске станка и сигнала не участвуют
	rest := r.period(words)

	// Станок
	best, bestScore, tie := -1, 0, false
	for i := range machines {
		sc := 0
		for _, n := range machines[i].Names {
			sc = max(sc, score(rest, n))
		}
		switch {
		case sc > bestScore:
			best, bestScore, tie = i, sc, false
		case sc == bestScore && sc > 0:
			tie = true
		}
	}
	switch {
	case len(machines) == 0:
		r.Problem = "Ни один станок не отмечен «показывать» на странице «Telegram» анализатора."
		return r
	case bestScore == 0 && len(machines) == 1:
		best = 0 // станок один — можно не называть
	case bestScore == 0 || tie:
		r.Problem = "Не понял, какой станок."
		for _, m := range machines {
			r.Options = append(r.Options, m.Title)
		}
		return r
	}
	r.Machine = &machines[best]

	// Сигналы: у кого больше слов совпало; равные — все (не больше MaxSignals)
	type cand struct {
		s  Signal
		sc int
	}
	var cs []cand
	top := 0
	for _, s := range r.Machine.Signals {
		sc := max(score(rest, s.Label), score(rest, s.Name))
		if sc > 0 {
			cs = append(cs, cand{s, sc})
			top = max(top, sc)
		}
	}
	for _, c := range cs {
		if c.sc == top && len(r.Signals) < MaxSignals {
			r.Signals = append(r.Signals, c.s)
		}
	}
	if len(r.Signals) == 0 {
		if len(r.Machine.Signals) == 1 {
			r.Signals = r.Machine.Signals // сигнал один — можно не называть
			return r
		}
		r.Problem = "Не понял, какой сигнал на «" + r.Machine.Title + "»."
		for _, s := range r.Machine.Signals {
			r.Options = append(r.Options, s.Label)
		}
	}
	return r
}

// period — достать период из слов; вернёт оставшиеся слова (без чисел, единиц и служебных)
func (r *Result) period(words []string) []string {
	var rest []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if n, err := strconv.Atoi(w); err == nil {
			if i+1 < len(words) {
				if k := unitSeconds(words[i+1]); k > 0 {
					r.Minutes = clamp(n*k/60, 1, MaxMinutes)
					i++
					continue
				}
			}
			r.Minutes = clamp(n, 1, MaxMinutes) // число без единицы — минуты
			continue
		}
		switch {
		case strings.HasPrefix(w, "смен"):
			r.Shift = true
		case w == "полчаса":
			r.Minutes = 30
		case unitSeconds(w) > 0: // «час», «минуту» без числа
			r.Minutes = max(1, unitSeconds(w)/60)
		case !stop[w]:
			rest = append(rest, w)
		}
	}
	return rest
}

// unitSeconds — сколько секунд в единице («мин» → 60); 0 — это не единица времени
func unitSeconds(w string) int {
	switch {
	case w == "с" || w == "сек" || strings.HasPrefix(w, "секунд"):
		return 1
	case w == "м" || w == "мин" || strings.HasPrefix(w, "минут"):
		return 60
	case w == "ч" || strings.HasPrefix(w, "час"):
		return 3600
	}
	return 0
}

func clamp(x, lo, hi int) int { return min(max(x, lo), hi) }

// tokens — слова в нижнем регистре, ё = е; «10мин» → «10», «мин»
func tokens(s string) []string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	var out []string
	var cur []rune
	digit := false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, c := range s {
		switch {
		case unicode.IsDigit(c):
			if !digit {
				flush()
			}
			digit = true
			cur = append(cur, c)
		case unicode.IsLetter(c):
			if digit {
				flush()
			}
			digit = false
			cur = append(cur, c)
		default:
			flush()
			digit = false
		}
	}
	flush()
	return out
}

// score — сколько слов имени нашлось в вопросе (слова короче 3 букв и числа не считаются)
func score(words []string, name string) int {
	n := 0
	for _, nw := range tokens(name) {
		if len([]rune(nw)) < 3 {
			continue
		}
		if _, err := strconv.Atoi(nw); err == nil {
			continue
		}
		for _, w := range words {
			if same(w, nw) {
				n++
				break
			}
		}
	}
	return n
}

// same — одно и то же слово: целиком; с другим окончанием (общие первые 5 букв, «давлением» = «давление»);
// с одной опечаткой (слова от 5 букв)
func same(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 3 || len(rb) < 3 {
		return false
	}
	if k := min(len(ra), len(rb), 5); k >= 4 && string(ra[:k]) == string(rb[:k]) {
		return true
	}
	if len(ra) >= 5 && len(rb) >= 5 {
		k := min(len(ra), len(rb), 7)
		return lev(ra[:min(len(ra), k+1)], rb[:min(len(rb), k+1)]) <= 1
	}
	return false
}

// lev — расстояние Левенштейна (сколько букв заменить, вставить или удалить)
func lev(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Help — подсказка, что можно спросить (станки и их сигналы)
func Help(machines []Machine) string {
	var b strings.Builder
	b.WriteString("Спросите обычными словами, например:\n«давление шлифовка 10 мин», «график масла за час», «статистика за смену».\n")
	ms := append([]Machine(nil), machines...)
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Title < ms[j].Title })
	for _, m := range ms {
		var ls []string
		for _, s := range m.Signals {
			ls = append(ls, s.Label)
		}
		if len(ls) == 0 {
			ls = []string{"(сигналы для Telegram не выбраны)"}
		}
		b.WriteString("\n" + m.Title + ": " + strings.Join(ls, ", "))
	}
	return b.String()
}
