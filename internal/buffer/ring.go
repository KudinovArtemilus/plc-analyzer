package buffer

import "sync"

// Point — одна точка: время и значения всех сигналов блока
type Point struct {
	T int64     // время в мс (Unix)
	V []float64 // значения сигналов, в порядке их описания
}

// Ring — кольцевой буфер последних N точек, безопасен для горутин
type Ring struct {
	mu   sync.RWMutex
	data []Point
	next int  // куда писать следующую точку
	full bool // буфер хотя бы раз заполнился целиком
}

// New создаёт буфер на capacity точек
func New(capacity int) *Ring {
	return &Ring{data: make([]Point, capacity)}
}

// Add добавляет точку, затирая самую старую при заполнении
func (r *Ring) Add(p Point) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data[r.next] = p
	r.next++
	if r.next == len(r.data) {
		r.next = 0
		r.full = true
	}
}

// Len — сколько точек сейчас в буфере
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.full {
		return len(r.data)
	}
	return r.next
}

// Since возвращает точки новее t (мс), от старых к новым
func (r *Ring) Since(t int64) []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n, start := r.next, 0
	if r.full {
		n, start = len(r.data), r.next // самая старая — там, куда будем писать
	}

	out := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		p := r.data[(start+i)%len(r.data)]
		if p.T > t {
			out = append(out, p)
		}
	}
	return out
}
