// Package plot — картинка графика PNG для бота Telegram (шаг 45f).
//
// Только стандартная библиотека: без шрифтов и сторонних пакетов. Поэтому на картинке
// только цифры (шкала, время) — нарисованы своим маленьким шрифтом; названия сигналов
// и статистика идут текстом в подписи к фото. Каждый сигнал — своя дорожка со своей шкалой
// (как на Live). На дорожку — полоса «мин–макс» в каждом столбце пикселей: короткий скачок
// не теряется, даже если в столбец попали тысячи точек.
package plot

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
	"time"
)

// Series — один сигнал: время (мс Unix) и значения; Bool — рисовать ступенькой 0/1
type Series struct {
	T    []int64
	V    []float64
	Bool bool
}

// Размеры картинки: Telegram показывает фото до 1280 по ширине, 1000 × N — чётко и на телефоне
const (
	W      = 1000
	trackH = 200
	padL   = 96 // место под цифры шкалы
	padR   = 24
	padT   = 16
	gapH   = 18
	axisH  = 40
)

var (
	bg     = color.RGBA{255, 255, 255, 255}
	frame  = color.RGBA{200, 205, 211, 255}
	grid   = color.RGBA{232, 235, 238, 255}
	ink    = color.RGBA{60, 70, 80, 255}
	colors = []color.RGBA{{47, 74, 104, 255}, {194, 103, 27, 255}, {63, 125, 78, 255}, {107, 78, 155, 255}}
)

// Color — цвет дорожки i (тот же, что кружок в подписи)
func Color(i int) color.RGBA { return colors[i%len(colors)] }

// PNG — картинка: дорожки сигналов друг под другом за [from, to] (мс Unix), время — в loc
func PNG(series []Series, from, to int64, loc *time.Location) ([]byte, error) {
	n := max(len(series), 1)
	h := padT + n*trackH + (n-1)*gapH + axisH
	img := image.NewRGBA(image.Rect(0, 0, W, h))
	fill(img, img.Bounds(), bg)
	plotW := W - padL - padR
	x := func(t int64) int { return padL + int(float64(t-from)/float64(max(to-from, 1))*float64(plotW-1)) }

	for i, s := range series {
		top := padT + i*(trackH+gapH)
		r := image.Rect(padL, top, padL+plotW, top+trackH)
		lo, hi := bounds(s)
		if s.Bool {
			lo, hi = 0, 1
		}
		pad := (hi - lo) * 0.08
		if pad == 0 {
			pad = math.Max(math.Abs(hi)*0.05, 1)
		}
		lo, hi = lo-pad, hi+pad
		y := func(v float64) int { return r.Max.Y - 1 - int((v-lo)/(hi-lo)*float64(trackH-1)) }

		// Сетка и шкала: 5 делений
		for k := 0; k <= 4; k++ {
			v := lo + (hi-lo)*float64(k)/4
			yy := y(v)
			hline(img, r.Min.X, r.Max.X, yy, grid)
			if !s.Bool {
				text(img, label(v, hi-lo), padL-8, yy-7, ink, true)
			}
		}
		if s.Bool {
			text(img, "1", padL-8, y(1)-7, ink, true)
			text(img, "0", padL-8, y(0)-7, ink, true)
		}
		rect(img, r, frame)
		text(img, strconv.Itoa(i+1), r.Min.X+6, r.Min.Y+6, Color(i), false) // номер дорожки = номер в подписи

		// Столбцы пикселей: мин и макс каждого; между столбцами — соединение
		c := Color(i)
		prevX, prevY := -1, 0
		for col := 0; col < plotW; col++ {
			t0 := from + int64(float64(col)/float64(plotW)*float64(to-from))
			t1 := from + int64(float64(col+1)/float64(plotW)*float64(to-from))
			mn, mx, last, ok := colRange(s, t0, t1)
			if !ok {
				prevX = -1 // нет точек (обрыв связи) — разрыв на графике
				continue
			}
			xx := padL + col
			if prevX >= 0 && xx-prevX <= 3 {
				vline(img, xx, prevY, y(firstIn(s, t0, t1)), c)
			}
			vline(img, xx, y(mn), y(mx), c)
			vline(img, xx+1, y(mn), y(mx), c) // толщина 2 px — видно на телефоне
			if s.Bool && mx >= 0.5 {
				vfill(img, xx, y(1), r.Max.Y-1, color.RGBA{c.R, c.G, c.B, 60})
			}
			prevX, prevY = xx, y(last)
		}
	}

	// Ось времени: 6 подписей ЧЧ:ММ (или ЧЧ:ММ:СС, если участок короче 3 минут)
	ay := padT + n*trackH + (n-1)*gapH + 12
	layout := "15:04"
	if to-from < 3*60*1000 {
		layout = "15:04:05"
	}
	for k := 0; k <= 5; k++ {
		t := from + (to-from)*int64(k)/5
		xx := x(t)
		vline(img, xx, ay-10, ay-4, ink)
		s := time.UnixMilli(t).In(loc).Format(layout)
		w := textW(s)
		tx := min(max(xx-w/2, padL), W-padR-w)
		text(img, s, tx, ay, ink, false)
	}

	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}

// bounds — мин и макс значений серии (без NaN); пусто — 0..1
func bounds(s Series) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range s.V {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if math.IsInf(lo, 1) {
		return 0, 1
	}
	return lo, hi
}

// colRange — мин, макс и последнее значение точек с временем в [t0, t1)
func colRange(s Series, t0, t1 int64) (mn, mx, last float64, ok bool) {
	i := lower(s.T, t0)
	for ; i < len(s.T) && s.T[i] < t1; i++ {
		v := s.V[i]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if !ok {
			mn, mx = v, v
		}
		mn, mx, last, ok = math.Min(mn, v), math.Max(mx, v), v, true
	}
	return
}

func firstIn(s Series, t0, t1 int64) float64 {
	for i := lower(s.T, t0); i < len(s.T) && s.T[i] < t1; i++ {
		if v := s.V[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v
		}
	}
	return 0
}

// lower — первый индекс с t >= x (двоичный поиск)
func lower(t []int64, x int64) int {
	lo, hi := 0, len(t)
	for lo < hi {
		m := (lo + hi) / 2
		if t[m] < x {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// label — число для шкалы: знаков после запятой столько, сколько нужно при этом размахе
func label(v, span float64) string {
	d := 0
	switch {
	case span < 0.1:
		d = 3
	case span < 1:
		d = 2
	case span < 20:
		d = 1
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', d, 64), ".", ",", 1)
}

// ---------- Рисование ----------

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func hline(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	for x := x0; x < x1; x++ {
		img.SetRGBA(x, y, c)
	}
}

func vline(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		img.SetRGBA(x, y, c)
	}
}

// vfill — полупрозрачная заливка столбца (бит в «1»)
func vfill(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	a := float64(c.A) / 255
	for y := y0; y <= y1; y++ {
		o := img.RGBAAt(x, y)
		img.SetRGBA(x, y, color.RGBA{
			uint8(float64(o.R)*(1-a) + float64(c.R)*a), uint8(float64(o.G)*(1-a) + float64(c.G)*a),
			uint8(float64(o.B)*(1-a) + float64(c.B)*a), 255})
	}
}

func rect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	hline(img, r.Min.X, r.Max.X, r.Min.Y, c)
	hline(img, r.Min.X, r.Max.X, r.Max.Y-1, c)
	vline(img, r.Min.X, r.Min.Y, r.Max.Y-1, c)
	vline(img, r.Max.X-1, r.Min.Y, r.Max.Y-1, c)
}

// ---------- Свой шрифт: цифры и знаки 5 × 7, увеличенные вдвое ----------

const scale = 2

var glyphs = map[rune][7]string{
	'0': {".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."},
	'1': {"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."},
	'2': {".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'3': {"####.", "....#", "....#", ".###.", "....#", "....#", "####."},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	'5': {"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	'6': {".###.", "#....", "#....", "####.", "#...#", "#...#", ".###."},
	'7': {"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	'8': {".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	'9': {".###.", "#...#", "#...#", ".####", "....#", "....#", ".###."},
	':': {".....", "..#..", "..#..", ".....", "..#..", "..#..", "....."},
	',': {".....", ".....", ".....", ".....", ".....", "..#..", ".#..."},
	'.': {".....", ".....", ".....", ".....", ".....", ".##..", ".##.."},
	'-': {".....", ".....", ".....", ".###.", ".....", ".....", "....."},
	'+': {".....", "..#..", "..#..", "#####", "..#..", "..#..", "....."},
	'e': {".....", ".....", ".###.", "#...#", "#####", "#....", ".###."},
}

func textW(s string) int { return len([]rune(s)) * 6 * scale }

// text — строка своим шрифтом; right — x — правый край
func text(img *image.RGBA, s string, x, y int, c color.RGBA, right bool) {
	if right {
		x -= textW(s)
	}
	for _, ch := range s {
		g, ok := glyphs[ch]
		if ok {
			for gy, row := range g {
				for gx, p := range row {
					if p == '#' {
						fill(img, image.Rect(x+gx*scale, y+gy*scale, x+(gx+1)*scale, y+(gy+1)*scale), c)
					}
				}
			}
		}
		x += 6 * scale
	}
}
