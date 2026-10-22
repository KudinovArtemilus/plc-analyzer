package plc

import (
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// DataType — тип данных сигнала в ПЛК
type DataType string

const (
	Bool  DataType = "BOOL"
	Byte  DataType = "BYTE"
	Int   DataType = "INT"
	Word  DataType = "WORD"
	DInt  DataType = "DINT"
	DWord DataType = "DWORD"
	Real  DataType = "REAL"
)

// Address — разобранный адрес в DB
type Address struct {
	DB   int
	Byte int  // номер байта
	Bit  int  // номер бита 0..7 (только для X)
	Kind byte // 'X' бит, 'B' байт, 'W' слово, 'D' двойное слово
}

// DB11.DBW90, DB11.DBD100, DB11.DBB4, DB11.DBX4.3
var addrRe = regexp.MustCompile(`^DB(\d+)\.DB([XBWD])(\d+)(?:\.(\d+))?$`)

// ParseAddress разбирает адрес вида DB11.DBW90 (регистр и пробелы не важны)
func ParseAddress(s string) (Address, error) {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	m := addrRe.FindStringSubmatch(clean)
	if m == nil {
		return Address{}, fmt.Errorf("неверный адрес %q: ожидается DB11.DBW90, DB11.DBD100, DB11.DBB4 или DB11.DBX4.3", s)
	}

	db, err := strconv.Atoi(m[1])
	if err != nil || db < 1 || db > 65535 {
		return Address{}, fmt.Errorf("адрес %q: номер DB должен быть от 1 до 65535", s)
	}
	byteN, err := strconv.Atoi(m[3])
	if err != nil || byteN > 65535 {
		return Address{}, fmt.Errorf("адрес %q: слишком большой номер байта", s)
	}

	a := Address{DB: db, Byte: byteN, Kind: m[2][0]}

	switch {
	case a.Kind == 'X' && m[4] == "":
		return Address{}, fmt.Errorf("адрес %q: для DBX нужен номер бита, например DB11.DBX4.3", s)
	case a.Kind == 'X':
		bit, err := strconv.Atoi(m[4])
		if err != nil || bit > 7 {
			return Address{}, fmt.Errorf("адрес %q: номер бита должен быть от 0 до 7", s)
		}
		a.Bit = bit
	case m[4] != "":
		return Address{}, fmt.Errorf("адрес %q: номер бита указывается только для DBX", s)
	}
	return a, nil
}

// Size — сколько байт занимает значение по адресу
func (a Address) Size() int {
	switch a.Kind {
	case 'W':
		return 2
	case 'D':
		return 4
	default: // 'X' и 'B'
		return 1
	}
}

// String возвращает адрес в обычной записи: DB11.DBW90
func (a Address) String() string {
	if a.Kind == 'X' {
		return fmt.Sprintf("DB%d.DBX%d.%d", a.DB, a.Byte, a.Bit)
	}
	return fmt.Sprintf("DB%d.DB%c%d", a.DB, a.Kind, a.Byte)
}

// kind — какой размер адреса нужен типу
func (t DataType) kind() (byte, bool) {
	switch t {
	case Bool:
		return 'X', true
	case Byte:
		return 'B', true
	case Int, Word:
		return 'W', true
	case DInt, DWord, Real:
		return 'D', true
	}
	return 0, false
}

// CheckType проверяет, что тип подходит к адресу (INT — только к DBW и т. д.)
func CheckType(a Address, t DataType) error {
	k, ok := t.kind()
	if !ok {
		return fmt.Errorf("неизвестный тип %q", t)
	}
	if k != a.Kind {
		return fmt.Errorf("тип %s не подходит к адресу %s: нужен адрес DB%c", t, a, k)
	}
	return nil
}

// Decode достаёт значение из буфера блока.
// start — номер байта DB, с которого начинается buf.
func Decode(buf []byte, start int, a Address, t DataType) (float64, error) {
	i := a.Byte - start
	if i < 0 || i+a.Size() > len(buf) {
		return 0, fmt.Errorf("адрес %s вне прочитанного блока", a)
	}
	b := buf[i:]

	switch t {
	case Bool:
		if b[0]&(1<<a.Bit) != 0 {
			return 1, nil
		}
		return 0, nil
	case Byte:
		return float64(b[0]), nil
	case Int:
		return float64(int16(binary.BigEndian.Uint16(b))), nil
	case Word:
		return float64(binary.BigEndian.Uint16(b)), nil
	case DInt:
		return float64(int32(binary.BigEndian.Uint32(b))), nil
	case DWord:
		return float64(binary.BigEndian.Uint32(b)), nil
	case Real:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	}
	return 0, fmt.Errorf("неизвестный тип %q", t)
}
