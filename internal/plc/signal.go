package plc

import (
	"fmt"
	"time"
)

// Signal — один сигнал: где лежит, какого типа, как часто читать
type Signal struct {
	Name   string
	Addr   Address
	Type   DataType
	Period time.Duration // частота опроса
	Scale  float64       // коэффициент (0 означает 1)
	Bias   float64       // смещение
	Unit   string        // единицы: "°C", "бар", "%"
}

// NewSignal создаёт сигнал из строки адреса и проверяет, что всё корректно
func NewSignal(name, addr string, typ DataType, period time.Duration) (Signal, error) {
	if name == "" {
		return Signal{}, fmt.Errorf("пустое имя сигнала")
	}
	a, err := ParseAddress(addr)
	if err != nil {
		return Signal{}, fmt.Errorf("сигнал %q: %w", name, err)
	}
	if err := CheckType(a, typ); err != nil {
		return Signal{}, fmt.Errorf("сигнал %q: %w", name, err)
	}
	if period < time.Millisecond {
		return Signal{}, fmt.Errorf("сигнал %q: период %v слишком мал", name, period)
	}
	return Signal{Name: name, Addr: a, Type: typ, Period: period}, nil
}

// Apply переводит «сырое» значение из ПЛК в физические единицы
func (s Signal) Apply(raw float64) float64 {
	k := s.Scale
	if k == 0 {
		k = 1
	}
	return raw*k + s.Bias
}

// Block — непрерывный диапазон одного DB, читаемый одним запросом
type Block struct {
	DB      int
	Start   int // первый байт
	Size    int // сколько байт
	Signals []Signal
}

// Group — сигналы одной частоты, разбитые на блоки
type Group struct {
	Period  time.Duration
	Blocks  []Block
	Signals []Signal // все сигналы группы в порядке блоков
}
