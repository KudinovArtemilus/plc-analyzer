package archive

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	magic    = "PLCA"   // первые 4 байта любого нашего файла
	Version  = 1        // версия формата
	maxBlock = 64 << 20 // защита от мусора: блок не больше 64 МБ
)

// ErrDamaged — дальше файл читать нельзя (оборван или испорчен)
var ErrDamaged = errors.New("файл повреждён")

// ---------- Заголовок: файл описывает себя сам ----------

type SignalInfo struct {
	Name  string  `json:"name"`
	Addr  string  `json:"addr"`
	Type  string  `json:"type"`
	Scale float64 `json:"scale"`
	Bias  float64 `json:"bias"`
	Unit  string  `json:"unit"`
}

type GroupInfo struct {
	PeriodMs int64        `json:"period_ms"`
	Signals  []SignalInfo `json:"signals"`
}

type Header struct {
	Version   int         `json:"version"`
	PLC       string      `json:"plc"`
	ShiftDate string      `json:"shift_date"` // "2026-10-21"
	Shift     string      `json:"shift"`      // "A" или "B"
	Gen       int         `json:"gen"`        // номер конфигурации сигналов
	Created   time.Time   `json:"created"`
	Groups    []GroupInfo `json:"groups"`
}

// Chunk — порция данных одной группы
type Chunk struct {
	Group  int
	Times  []int64     // время точек, мс Unix
	Values [][]float64 // Values[сигнал][точка]
}

// ---------- Запись ----------

type Writer struct {
	f   *os.File
	bw  *bufio.Writer
	hdr Header
}

// Create создаёт новый файл (и папки к нему) и пишет заголовок.
// Существующий файл никогда не перезаписывается.
func Create(path string, h Header) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	h.Version = Version
	js, err := json.Marshal(h)
	if err != nil {
		f.Close()
		return nil, err
	}

	bw := bufio.NewWriter(f)
	bw.WriteString(magic)
	bw.Write(binary.BigEndian.AppendUint32(nil, uint32(len(js))))
	bw.Write(js)
	if err := bw.Flush(); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, bw: bw, hdr: h}, nil
}

// WriteChunk сжимает порцию и дописывает её в файл: [длина][CRC32][данные]
func (w *Writer) WriteChunk(c Chunk) error {
	if c.Group < 0 || c.Group >= len(w.hdr.Groups) {
		return fmt.Errorf("в заголовке нет группы %d", c.Group)
	}
	if len(c.Values) != len(w.hdr.Groups[c.Group].Signals) {
		return fmt.Errorf("группа %d: столбцов %d, сигналов в заголовке %d",
			c.Group, len(c.Values), len(w.hdr.Groups[c.Group].Signals))
	}
	for _, col := range c.Values {
		if len(col) != len(c.Times) {
			return fmt.Errorf("группа %d: длины столбцов не совпадают", c.Group)
		}
	}
	if len(c.Times) == 0 {
		return nil
	}

	payload, err := encodeChunk(c)
	if err != nil {
		return err
	}
	head := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	head = binary.BigEndian.AppendUint32(head, crc32.ChecksumIEEE(payload))
	if _, err := w.bw.Write(head); err != nil {
		return err
	}
	if _, err := w.bw.Write(payload); err != nil {
		return err
	}
	return w.bw.Flush() // сразу отдать операционной системе
}

func (w *Writer) Close() error {
	err := w.bw.Flush()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ---------- Кодирование порции ----------

const (
	colInt   = 0 // все значения целые: храним разности как varint
	colFloat = 1 // есть дробные: храним 8 байт как есть
)

func encodeChunk(c Chunk) ([]byte, error) {
	raw := binary.AppendUvarint(nil, uint64(c.Group))
	raw = binary.AppendUvarint(raw, uint64(len(c.Times)))

	// Время: первая отметка, дальше только разности (обычно 100, 100, 100…)
	var prev int64
	for _, t := range c.Times {
		raw = binary.AppendVarint(raw, t-prev)
		prev = t
	}
	// Значения: столбец за столбцом
	for _, col := range c.Values {
		raw = appendColumn(raw, col)
	}

	// Второй этап сжатия — flate (как в ZIP)
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func appendColumn(raw []byte, col []float64) []byte {
	if allInts(col) {
		raw = append(raw, colInt)
		var prev int64
		for _, v := range col {
			n := int64(v)
			raw = binary.AppendVarint(raw, n-prev)
			prev = n
		}
		return raw
	}
	raw = append(raw, colFloat)
	for _, v := range col {
		raw = binary.BigEndian.AppendUint64(raw, math.Float64bits(v))
	}
	return raw
}

// allInts — все значения целые и помещаются в float64 без потерь
func allInts(col []float64) bool {
	for _, v := range col {
		if v != math.Trunc(v) || math.Abs(v) > 1<<53 {
			return false
		}
	}
	return true
}

// ---------- Чтение ----------

type Reader struct {
	f   *os.File
	br  *bufio.Reader
	hdr Header
}

func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(why string) (*Reader, error) {
		f.Close()
		return nil, fmt.Errorf("%s: %w: %s", path, ErrDamaged, why)
	}

	br := bufio.NewReader(f)
	start := make([]byte, 8)
	if _, err := io.ReadFull(br, start); err != nil {
		return fail("нет заголовка")
	}
	if string(start[:4]) != magic {
		f.Close()
		return nil, fmt.Errorf("%s: это не файл аналайзера", path)
	}
	n := binary.BigEndian.Uint32(start[4:])
	if n > maxBlock {
		return fail("неверная длина заголовка")
	}
	js := make([]byte, n)
	if _, err := io.ReadFull(br, js); err != nil {
		return fail("заголовок оборван")
	}
	var h Header
	if err := json.Unmarshal(js, &h); err != nil {
		return fail("заголовок не читается")
	}
	if h.Version != Version {
		f.Close()
		return nil, fmt.Errorf("%s: версия формата %d не поддерживается", path, h.Version)
	}
	return &Reader{f: f, br: br, hdr: h}, nil
}

func (r *Reader) Header() Header { return r.hdr }
func (r *Reader) Close() error   { return r.f.Close() }

// Next читает следующую порцию.
// io.EOF — файл кончился нормально; ErrDamaged — хвост оборван или испорчен.
func (r *Reader) Next() (Chunk, error) {
	head := make([]byte, 8)
	if _, err := io.ReadFull(r.br, head); err != nil {
		if err == io.EOF {
			return Chunk{}, io.EOF
		}
		return Chunk{}, fmt.Errorf("%w: оборванная порция", ErrDamaged)
	}
	n := binary.BigEndian.Uint32(head[:4])
	sum := binary.BigEndian.Uint32(head[4:])
	if n > maxBlock {
		return Chunk{}, fmt.Errorf("%w: неверная длина порции", ErrDamaged)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r.br, payload); err != nil {
		return Chunk{}, fmt.Errorf("%w: оборванная порция", ErrDamaged)
	}
	if crc32.ChecksumIEEE(payload) != sum {
		return Chunk{}, fmt.Errorf("%w: контрольная сумма не совпала", ErrDamaged)
	}
	return r.decodeChunk(payload)
}

func (r *Reader) decodeChunk(payload []byte) (Chunk, error) {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(payload)))
	if err != nil {
		return Chunk{}, fmt.Errorf("%w: не распаковывается", ErrDamaged)
	}

	d := decoder{b: raw}
	g := int(d.uvarint())
	n := int(d.uvarint())
	if d.err != nil || g >= len(r.hdr.Groups) || n > 10_000_000 {
		return Chunk{}, fmt.Errorf("%w: неверное начало порции", ErrDamaged)
	}

	c := Chunk{Group: g, Times: make([]int64, n)}
	var prev int64
	for i := range c.Times {
		prev += d.varint()
		c.Times[i] = prev
	}
	c.Values = make([][]float64, len(r.hdr.Groups[g].Signals))
	for s := range c.Values {
		c.Values[s] = d.column(n)
	}
	if d.err != nil {
		return Chunk{}, fmt.Errorf("%w: данные порции не сходятся", ErrDamaged)
	}
	return c, nil
}

// decoder читает числа подряд. Первая ошибка «залипает»:
// дальше все чтения возвращают 0, а проверить ошибку можно один раз в конце.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, k := binary.Uvarint(d.b)
	if k <= 0 {
		d.err = ErrDamaged
		return 0
	}
	d.b = d.b[k:]
	return v
}

func (d *decoder) varint() int64 {
	if d.err != nil {
		return 0
	}
	v, k := binary.Varint(d.b)
	if k <= 0 {
		d.err = ErrDamaged
		return 0
	}
	d.b = d.b[k:]
	return v
}

func (d *decoder) column(n int) []float64 {
	out := make([]float64, n)
	if d.err != nil {
		return out
	}
	if len(d.b) == 0 {
		d.err = ErrDamaged
		return out
	}
	mode := d.b[0]
	d.b = d.b[1:]

	switch mode {
	case colInt:
		var prev int64
		for i := range out {
			prev += d.varint()
			out[i] = float64(prev)
		}
	case colFloat:
		if len(d.b) < 8*n {
			d.err = ErrDamaged
			return out
		}
		for i := range out {
			out[i] = math.Float64frombits(binary.BigEndian.Uint64(d.b[i*8:]))
		}
		d.b = d.b[8*n:]
	default:
		d.err = ErrDamaged
	}
	return out
}
