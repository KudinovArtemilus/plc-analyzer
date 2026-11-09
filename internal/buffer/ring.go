package buffer

import (
	"math"
	"strconv"
	"sync"
)

// Point — одна точка: время и значения всех сигналов блока
type Point struct {
	T int64     // время в мс (Unix)
	V []float64 // значения сигналов, в порядке их описания
}

// MarshalJSON — точка для потока Live: {"T":…,"V":[…]}. Сам encoding/json на NaN выдаёт ошибку
// (в JSON нет «не числа»), и тогда весь поток Live обрывался. Здесь «не число» и бесконечность
// уходят как null — браузер рисует на этом месте разрыв, как при обрыве связи.
func (p Point) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 16+12*len(p.V))
	b = append(b, `{"T":`...)
	b = strconv.AppendInt(b, p.T, 10)
	b = append(b, `,"V":[`...)
	for i, x := range p.V {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b = append(b, "null"...)
			continue
		}
		// Как пишет encoding/json: обычная запись, а очень большие и очень малые — через e
		f := byte('f')
		if a := math.Abs(x); a != 0 && (a < 1e-6 || a >= 1e21) {
			f = 'e'
		}
		b = strconv.AppendFloat(b, x, f, -1, 64)
	}
	return append(b, "]}"...), nil
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
