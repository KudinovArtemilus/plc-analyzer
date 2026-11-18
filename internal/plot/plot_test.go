package plot

import (
	"bytes"
	"image/png"
	"math"
	"os"
	"testing"
	"time"
)

// demo — 10 минут, точка раз в 100 мс: давление с коротким скачком, бит, разрыв связи
func demo() ([]Series, int64, int64) {
	from := time.Date(2026, 11, 18, 11, 42, 0, 0, time.UTC).UnixMilli()
	to := from + 10*60*1000
	var p, b Series
	b.Bool = true
	for t := from; t <= to; t += 100 {
		if t > from+6*60*1000 && t < from+7*60*1000 {
			continue // минута без связи
		}
		k := float64(t-from) / 1000
		v := 180 + 8*math.Sin(k/30)
		if t >= from+3*60*1000 && t < from+3*60*1000+300 {
			v = 230 // скачок 0,3 с — должен быть виден
		}
		p.T, p.V = append(p.T, t), append(p.V, v)
		bit := 0.0
		if int(k/40)%2 == 0 {
			bit = 1
		}
		b.T, b.V = append(b.T, t), append(b.V, bit)
	}
	return []Series{p, b}, from, to
}

func TestPNG(t *testing.T) {
	ss, from, to := demo()
	raw, err := PNG(ss, from, to, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != W || b.Dy() != padT+2*trackH+gapH+axisH {
		t.Errorf("размер %v", b)
	}
	if len(raw) > 200_000 {
		t.Errorf("картинка %d байт — тяжело для телефона", len(raw))
	}
	// Скачок 0,3 с виден: в его столбце цвет дорожки доходит почти до верха шкалы
	col := padL + int(float64(3*60*1000)/float64(to-from)*float64(W-padL-padR-1))
	found := false
	for y := padT; y < padT+30; y++ {
		for dx := -1; dx <= 2; dx++ {
			if c := img.At(col+dx, y); c == Color(0) {
				found = true
			}
		}
	}
	if !found {
		t.Error("короткий скачок потерялся")
	}
	if os.Getenv("PLOT_OUT") != "" {
		os.WriteFile(os.Getenv("PLOT_OUT"), raw, 0o644)
	}
}

func TestLabel(t *testing.T) {
	for _, c := range []struct {
		v, span float64
		want    string
	}{{182.44, 40, "182"}, {182.44, 10, "182,4"}, {0.123, 0.5, "0,12"}, {-3, 30, "-3"}} {
		if got := label(c.v, c.span); got != c.want {
			t.Errorf("%v: %s", c, got)
		}
	}
}
