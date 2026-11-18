package web

import (
	"strings"
	"testing"
	"time"
)

// Итог смены А 17.11: запись 08:00–09:30 (Автомат 08:00–09:00, Ручной 09:00–09:30),
// авария «Перегрев масла» 08:30–08:45 и «Дверь открыта» с 09:58 до конца смены
func TestTGShiftReport(t *testing.T) {
	s, st, ctx, now, _, _ := tgSummaryServer(t)
	end := time.Date(now.Year(), now.Month(), now.Day(), 20, 0, 0, 0, time.Local)

	// Выключено — ничего
	if text, _ := s.TGReport(ctx, end.Add(2*time.Minute)); text != "" {
		t.Fatalf("выключено, а итог есть: %s", text)
	}
	st.SetTGReport(ctx, "тест", true)
	// Только что включили — итог не задним числом, а в конце следующей смены
	if text, _ := s.TGReport(ctx, end.Add(2*time.Minute)); text != "" {
		t.Fatalf("сразу после включения: %s", text)
	}
	// Кнопка «прислать сейчас»
	st.RequestTGReport(ctx, "тест")
	text, err := s.TGReport(ctx, end.Add(3*time.Minute))
	want := `📋 Итог: смена А 17.11 (08:00–20:00)

Мехобработка
Шлифовка: Автомат 6,3% · Ручной 4,2% · Авария 2,1% · Нет связи 87,5%
   аварий 2, всего 10 ч 17 мин; больше всего: Дверь открыта ×1 (10 ч 2 мин), Перегрев масла ×1 (15 мин)`
	if err != nil || text != want {
		t.Fatalf("итог:\n%s\nждали:\n%s\n%v", text, want, err)
	}
	if strings.Contains(text, "Пресс") || strings.Contains(text, "Мало смазки") {
		t.Error("выключенный станок или предупреждение в итоге")
	}
	// Второй раз за ту же смену — нет
	if text, _ := s.TGReport(ctx, end.Add(4*time.Minute)); text != "" {
		t.Errorf("повтор: %s", text)
	}
	// Конец ночной смены 08:00: первую минуту ждём, потом — итог смены Б
	next := end.Add(12 * time.Hour)
	if text, _ := s.TGReport(ctx, next.Add(30*time.Second)); text != "" {
		t.Errorf("раньше минуты: %s", text)
	}
	text, _ = s.TGReport(ctx, next.Add(70*time.Second))
	// «Дверь открыта» висит с дневной смены — в ночной она тоже есть: все 12 часов
	if !strings.HasPrefix(text, "📋 Итог: смена Б 17.11 (20:00–08:00)") ||
		!strings.Contains(text, "аварий 1, всего 12 ч; больше всего: Дверь открыта ×1 (12 ч)") {
		t.Errorf("итог ночной смены:\n%s", text)
	}
}

// Страница: галочка и кнопка
func TestTGReportPage(t *testing.T) {
	s, st, ctx, _, _, _ := tgSummaryServer(t)
	h := s.Handler()
	if w := tgDo(h, "POST", "/telegram/report", map[string][]string{"on": {"on"}}, false); w.Code != 303 {
		t.Fatalf("галочка: %d", w.Code)
	}
	if on, _, _ := st.TGReport(ctx); !on {
		t.Error("не включилось")
	}
	b := tgDo(h, "GET", "/telegram", nil, false).Body.String()
	if !strings.Contains(b, "Итог смены в 08:00 и 20:00") || !strings.Contains(b, "Прислать итог прошлой смены сейчас") {
		t.Errorf("страница:\n%s", b)
	}
	tgDo(h, "POST", "/telegram/report/now", map[string][]string{}, false)
	if _, last, _ := st.TGReport(ctx); last != "now" {
		t.Errorf("кнопка: %q", last)
	}
	if w := tgDo(h, "POST", "/telegram/report/now", map[string][]string{}, true); w.Code != 403 {
		t.Errorf("с чужого ПК: %d", w.Code)
	}
}
