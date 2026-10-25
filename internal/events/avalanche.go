package events

import "time"

// Window — участок для автоматического слепка: от «за Before до первой аварии»
// до «через After после последней», и какие аварии в него попали.
type Window struct {
	From, To time.Time
	Alarms   []Edge
}

// Avalanche собирает аварии в «лавины»: пока окно последнего слепка ещё открыто,
// новая авария не делает свой слепок, а продлевает текущий. Так при каскаде
// из 30 аварий за минуту получается ОДИН слепок, а не тридцать.
//
// Как и Detector, не потокобезопасен: им владеет одна горутина (блок ПЛК).
type Avalanche struct {
	Before time.Duration // сколько записать ДО первой аварии (5 мин)
	After  time.Duration // и ПОСЛЕ последней (2 мин)
	Max    time.Duration // слепок не длиннее: столько данных держит буфер Live

	cur *Window
}

// Add — пришла авария. Возвращает готовый слепок, если текущий пришлось закрыть
// досрочно (лавина стала бы длиннее Max) — тогда новая авария начинает следующий.
func (a *Avalanche) Add(e Edge) *Window {
	end := e.At.Add(a.After)
	if a.cur != nil && !e.At.After(a.cur.To) && end.Sub(a.cur.From) <= a.Max {
		// Окно ещё открыто и не станет слишком длинным — продлеваем
		if end.After(a.cur.To) {
			a.cur.To = end
		}
		a.cur.Alarms = append(a.cur.Alarms, e)
		return nil
	}

	var done *Window
	if a.cur != nil { // окно открыто, но продлить нельзя — закрываем на текущем моменте
		done = a.cur
		if e.At.Before(done.To) {
			done.To = e.At
		}
	}
	a.cur = &Window{From: e.At.Add(-a.Before), To: end, Alarms: []Edge{e}}
	return done
}

// Due — пора ли записывать слепок: время «после» прошло (с запасом settle,
// чтобы последние точки успели дойти до буфера). Возвращает окно один раз.
func (a *Avalanche) Due(now time.Time, settle time.Duration) *Window {
	if a.cur == nil || now.Before(a.cur.To.Add(settle)) {
		return nil
	}
	w := a.cur
	a.cur = nil
	return w
}
