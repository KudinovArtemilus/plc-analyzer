package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestSignalRowHide(t *testing.T) {
	render1 := func(row store.SignalRow) string {
		w := httptest.NewRecorder()
		render(w, "row", row)
		return strings.Join(strings.Fields(w.Body.String()), " ")
	}
	trend := store.SignalRow{ID: 5, Name: "P", Kind: store.KindTrend, Enabled: true}

	// Видимый тренд: кнопка «скрыть», пометки нет
	if b := render1(trend); !strings.Contains(b, "/ui/signals/5/hide") || !strings.Contains(b, "скрыть</button>") ||
		strings.Contains(b, "· скрыт") {
		t.Errorf("видимый тренд:\n%s", b)
	}
	// Скрытый: пометка «· скрыт» и кнопка «показать»
	trend.Hidden = true
	if b := render1(trend); !strings.Contains(b, "· скрыт") || !strings.Contains(b, "показать</button>") {
		t.Errorf("скрытый тренд:\n%s", b)
	}
	// У аварии кнопки нет: на Live аварии и так показываются только по выбору
	alarm := store.SignalRow{ID: 6, Name: "AL", Kind: store.KindAlarm, Enabled: true}
	if b := render1(alarm); strings.Contains(b, "/hide") {
		t.Errorf("у аварии не должно быть кнопки «скрыть»:\n%s", b)
	}
}
