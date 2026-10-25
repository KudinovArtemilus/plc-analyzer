package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFmtNum(t *testing.T) {
	cases := []struct {
		x    float64
		prec int
		want string
	}{
		{12.5, 3, "12.5"},
		{100, 3, "100"},
		{100, 0, "100"}, // без точки нули справа — значащие
		{0.1234, 3, "0.123"},
		{-0.0001, 3, "0"},
		{-7.25, 1, "-7.2"},
	}
	for _, c := range cases {
		if got := fmtNum(c.x, c.prec); got != c.want {
			t.Errorf("fmtNum(%v, %d) = %q, ожидалось %q", c.x, c.prec, got, c.want)
		}
	}
}

func TestFmtStatDur(t *testing.T) {
	cases := map[int64]string{
		850:       "850 мс",
		12300:     "12,3 с",
		5000:      "5 с",
		245000:    "4 мин 05 с",
		7_800_000: "2 ч 10 мин",
	}
	for ms, want := range cases {
		if got := fmtStatDur(ms); got != want {
			t.Errorf("fmtStatDur(%d) = %q, ожидалось %q", ms, got, want)
		}
	}
}

func TestStatsTemplate(t *testing.T) {
	v := statsView{
		From: "08:00:00.000", To: "08:05:00.000", Dur: "5 мин 00 с",
		Analog: []statRow{
			{Name: "P<1>", Unit: "бар", Min: "1.5", Max: "7", Mean: "4.2", Span: "5.5", MinAt: "08:01:00.000", MaxAt: "08:02:00.000"},
			{Name: "T2", Empty: true},
		},
		Bits: []statRow{{Name: "Клещи", Bit: true, Rises: 3, On: "12,3 с", OnPct: "4.1 %", Longest: "8 с", LongestCut: true, Gaps: true}},
	}
	w := httptest.NewRecorder()
	render(w, "stats", v)
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, s := range []string{"5 мин 00 с", "P&lt;1&gt;", "бар", "08:02:00.000", "нет точек на участке",
		"Клещи", "≥ 8 с", "4.1 %", "разрыв связи", "stats-close"} {
		if !strings.Contains(body, s) {
			t.Errorf("в шаблоне нет %q", s)
		}
	}
}
