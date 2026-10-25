package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestPLCTemplates(t *testing.T) {
	p := store.PLC{ID: 3, Name: "1518F <цех>", IP: "192.168.0.10", Slot: 1}
	cases := []struct {
		name string
		data any
		want []string
	}{
		// Кнопка удаления с подтверждением; имя пользователя экранируется
		{"plcrow", p, []string{`hx-delete="/ui/plcs/3"`, "hx-confirm=", "1518F &lt;цех&gt;", "Файлы записей на диске останутся"}},
		{"plcdelerr", plcFormView{PLC: p, Err: store.ErrLastPLC.Error()},
			[]string{"Не удалён", "единственный ПЛК", `hx-get="/ui/plcs/3"`}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		render(w, c.name, c.data)
		body := w.Body.String()
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("шаблон %s: нет %q в\n%s", c.name, s, body)
			}
		}
	}
}
