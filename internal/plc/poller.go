package plc

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Sample — результат одного опроса группы
type Sample struct {
	Gen    int // номер конфигурации, по которой прочитано
	Group  int
	Time   time.Time
	Values []float64
	Dur    time.Duration
	Err    error
}

type Status struct {
	IP        string
	Connected bool
	ChangedAt time.Time
	Rate      int
	MaxDur    time.Duration
	Errors    int
	Outages   int
}

// Poller — опрос всех групп сигналов одного ПЛК через одно соединение.
// Группы задаются через SetGroups и могут меняться на ходу.
type Poller struct {
	IP   string
	Rack int
	Slot int

	mu      sync.Mutex
	st      Status
	groups  []Group // по возрастанию частоты (как возвращает BuildGroups)
	gen     int
	changed bool // есть новые группы, которые опрос ещё не применил

	secStart time.Time
	secCount int
	secMax   time.Duration
}

const maxErrors = 3

var backoff = []time.Duration{
	1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second,
}

// SetGroups задаёт новый состав групп. Опрос применит его на ближайшем тике.
func (p *Poller) SetGroups(groups []Group, gen int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.groups, p.gen, p.changed = groups, gen, true
}

// takeGroups возвращает текущие группы и снимает флаг «изменилось»
func (p *Poller) takeGroups() ([]Group, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.changed = false
	return p.groups, p.gen
}

func (p *Poller) hasChanged() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func (p *Poller) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.st
	s.IP = p.IP
	return s
}

func (p *Poller) setConnected(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.Connected = on
	p.st.ChangedAt = time.Now()
	if !on {
		p.st.Outages++
		p.st.Rate, p.st.MaxDur = 0, 0
	}
}

func (p *Poller) countRead(d time.Duration, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.st.Errors++
	} else {
		p.secCount++
		p.secMax = max(p.secMax, d)
	}
	if time.Since(p.secStart) >= time.Second {
		p.st.Rate, p.st.MaxDur = p.secCount, p.secMax
		p.secStart = time.Now()
		p.secCount, p.secMax = 0, 0
	}
}

func (p *Poller) Run(ctx context.Context, out chan<- Sample) {
	p.mu.Lock()
	p.st.ChangedAt = time.Now()
	p.mu.Unlock()

	attempt := 0
	var lostAt time.Time

	for {
		c, err := Connect(p.IP, p.Rack, p.Slot)
		if err != nil {
			wait := backoff[min(attempt, len(backoff)-1)]
			log.Printf("[%s] нет связи: %v — повтор через %v", p.IP, err, wait)
			attempt++
			if !sleep(ctx, wait) {
				return
			}
			continue
		}

		if lostAt.IsZero() {
			log.Printf("[%s] подключено", p.IP)
		} else {
			log.Printf("[%s] связь восстановлена, простой %v",
				p.IP, time.Since(lostAt).Round(time.Second))
		}
		p.setConnected(true)
		attempt = 0

		lostAt = p.poll(ctx, c, out)
		c.Close()

		if ctx.Err() != nil {
			return
		}
		p.setConnected(false)
		log.Printf("[%s] связь потеряна в %s", p.IP, lostAt.Format("15:04:05"))
	}
}

func (p *Poller) poll(ctx context.Context, c *Client, out chan<- Sample) time.Time {
	p.mu.Lock()
	p.secStart, p.secCount, p.secMax = time.Now(), 0, 0
	p.mu.Unlock()

	var (
		groups []Group
		gen    int
		bufs   [][][]byte
		next   []time.Time
		base   time.Duration
	)

	// setup готовит буферы и расписание под текущий состав групп
	setup := func() {
		groups, gen = p.takeGroups()
		base = time.Second // если сигналов нет — просто ждём изменений
		if len(groups) > 0 {
			base = groups[0].Period
		}
		bufs = make([][][]byte, len(groups))
		next = make([]time.Time, len(groups))
		now := time.Now()
		for gi, g := range groups {
			bufs[gi] = make([][]byte, len(g.Blocks))
			for bi, b := range g.Blocks {
				bufs[gi][bi] = make([]byte, b.Size)
			}
			next[gi] = now
		}
	}
	setup()

	ticker := time.NewTicker(base)
	defer ticker.Stop()

	errsInRow := 0
	var firstErr time.Time

	for {
		select {
		case <-ctx.Done():
			return time.Time{}

		case tick := <-ticker.C:
			// Пришли новые группы? Перестраиваемся на том же соединении
			if p.hasChanged() {
				setup()
				ticker.Reset(base)
				log.Printf("[%s] опрос перестроен (конфигурация %d): групп %d", p.IP, gen, len(groups))
				continue
			}

			for gi, g := range groups {
				if tick.Add(base / 2).Before(next[gi]) {
					continue
				}
				next[gi] = next[gi].Add(g.Period)
				if next[gi].Before(tick) {
					next[gi] = tick.Add(g.Period)
				}

				t0 := time.Now()
				vals, err := readGroup(c, g, bufs[gi])
				d := time.Since(t0)
				p.countRead(d, err)
				select {
				case out <- Sample{Gen: gen, Group: gi, Time: t0, Values: vals, Dur: d, Err: err}:
				case <-ctx.Done(): // нас останавливают, а читать уже некому — не ждём
					return time.Time{}
				}
				if err == nil {
					errsInRow = 0
					continue
				}
				if errsInRow == 0 {
					firstErr = t0
				}
				errsInRow++
				if errsInRow >= maxErrors {
					return firstErr
				}
			}
		}
	}
}

func readGroup(c *Client, g Group, bufs [][]byte) ([]float64, error) {
	vals := make([]float64, 0, len(g.Signals))
	for i, b := range g.Blocks {
		if err := c.Read(b, bufs[i]); err != nil {
			return nil, fmt.Errorf("DB%d байты %d..%d: %w", b.DB, b.Start, b.Start+b.Size-1, err)
		}
		for _, s := range b.Signals {
			raw, err := Decode(bufs[i], b.Start, s.Addr, s.Type)
			if err != nil {
				return nil, err
			}
			vals = append(vals, s.Apply(raw))
		}
	}
	return vals, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
