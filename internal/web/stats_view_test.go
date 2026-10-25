package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
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

func TestLiveStats(t *testing.T) {
	// Буфер Live: группа 100 мс, сигналы «Давление» и бит «Клещи»
	ring := buffer.New(100)
	pts := []struct {
		p, b float64
	}{{4, 0}, {6, 1}, {5, 1}, {9, 0}, {2, 1}}
	for i, x := range pts {
		ring.Add(buffer.Point{T: int64(1000 + 100*i), V: []float64{x.p, x.b}})
	}
	ring.Add(buffer.Point{T: 1600, V: []float64{1}}) // точка от старой конфигурации — пропускается

	s := &Server{}
	s.SetConfig(1, 1, []Group{{
		Period: 100 * time.Millisecond, Names: []string{"Давление", "Клещи"},
		Kinds: []string{"trend", "trend"}, Bits: []bool{false, true}, Units: []string{"бар", ""}, Ring: ring,
	}})

	// Участок 1100…1400: давление 6 5 9 2, бит 1 1 0 1
	w := httptest.NewRecorder()
	q := url.Values{"plc": {"1"}, "from": {"1100"}, "to": {"1400"}, "s": {"Давление", "Клещи"}}
	s.liveStats(w, httptest.NewRequest("GET", "/ui/live/stats?"+q.Encode(), nil)) // Encode — русские имена в %XX
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{"буфера Live", "Давление", "бар", ">2<", ">9<", ">5.5<", ">7<", // мин, макс, среднее, размах
		"Клещи"} {
		if !strings.Contains(body, want) {
			t.Errorf("нет %q в ответе:\n%s", want, body)
		}
	}
	// Первая точка участка уже «1» — не включение; включение одно: 0→1 в 1400
	if !strings.Contains(body, `<td class="num">1</td>`) {
		t.Errorf("ожидалось одно включение:\n%s", body)
	}

	// Неверный участок
	w = httptest.NewRecorder()
	s.liveStats(w, httptest.NewRequest("GET", "/ui/live/stats?plc=1&from=5&to=5", nil))
	if w.Code != 400 {
		t.Fatalf("пустой участок: код %d, ожидался 400", w.Code)
	}
}
