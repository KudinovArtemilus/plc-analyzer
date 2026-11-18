package summary

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- Сводка текстом — для ответа бота на /status (шаг 45b) ----------

// colorDot — кружок цвета состояния: в обычном сообщении Telegram цвет текста не задать
var colorDot = map[string]string{
	"blue": "🔵", "steel": "⚪", "green": "🟢", "orange": "🟠", "purple": "🟣",
	AlarmColor: "🔴", OfflineColor: "⚫",
}

// TextMax — предел длины сообщения Telegram (4096 символов) с запасом
const TextMax = 4000

// Text — сводка для сообщения:
//
//	Мехобработка
//	🔴 Шлифовка — Авария
//	   шаг 12 · Зажим закрыт
//	   смена А с 08:00: Автомат 37.5% · Ручной 25% · Авария 12.5% · Нет связи 25%
//	   Давление, бар: 182.4 (170–190)
//	   ■ Дверь открыта — с 09:58
func Text(s Summary) string {
	var b strings.Builder
	if len(s.Areas) == 0 {
		b.WriteString("Ни один станок не отмечен «показывать» на странице «Telegram» анализатора.\n")
	}
	for _, a := range s.Areas {
		b.WriteString(a.Name + "\n")
		for _, m := range a.Machines {
			dot, state := "▫️", ""
			if m.State != nil {
				dot, state = colorDot[m.State.Color], " — "+m.State.Name
				if dot == "" {
					dot = "▫️"
				}
			} else if !m.Online {
				state = " — нет связи"
			}
			b.WriteString(dot + " " + m.Name + state + "\n")
			if m.Step != "" {
				b.WriteString("   шаг " + m.Step + "\n")
			}
			if m.Shift != nil && len(m.Shift.Shares) > 0 {
				parts := make([]string, len(m.Shift.Shares))
				for i, sh := range m.Shift.Shares {
					parts[i] = sh.Name + " " + num(sh.Pct) + "%"
				}
				b.WriteString("   смена " + m.Shift.Name + " с " + m.Shift.From + ": " + strings.Join(parts, " · ") + "\n")
			}
			for _, g := range m.Signals {
				b.WriteString("   " + g.Label + ": " + signalText(g) + "\n")
			}
			for _, al := range m.Alarms {
				b.WriteString("   ■ " + al.Text + " — с " + al.Since + "\n")
			}
		}
		b.WriteString("\n")
	}
	if t, err := time.Parse(time.RFC3339, s.At); err == nil {
		b.WriteString("на " + t.Format("15:04:05"))
	}
	out := b.String()
	if utf8.RuneCountInString(out) > TextMax {
		r := []rune(out)
		out = string(r[:TextMax-20]) + "\n… (не поместилось)"
	}
	return out
}

func signalText(g Signal) string {
	switch {
	case g.Text != "":
		return g.Text
	case g.Value == nil:
		return "—"
	case g.Min != nil && g.Max != nil:
		return num(*g.Value) + " (" + num(*g.Min) + "–" + num(*g.Max) + ")"
	}
	return num(*g.Value)
}

func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
