package about

import (
	"strings"
	"testing"
)

func TestBuildText(t *testing.T) {
	cases := []struct {
		rev, at string
		dirty   bool
		want    string
	}{
		{"", "", false, "без git"},
		{"9404bc7a1b2c3d4e", "2026-11-02T05:53:00Z", false, "9404bc7 от 02.11.2026"},
		{"9404bc7a1b2c3d4e", "2026-11-02T05:53:00Z", true, "9404bc7 от 02.11.2026 +правки"},
		{"abc", "", false, "abc"},
	}
	for _, c := range cases {
		if got := buildText(c.rev, c.at, c.dirty); got != c.want {
			t.Errorf("buildText(%q, %q, %v) = %q, ждали %q", c.rev, c.at, c.dirty, got, c.want)
		}
	}
}

func TestLine(t *testing.T) {
	l := Line()
	for _, want := range []string{"PLC Analyzer", "сборка ", "© 2026 Артём Кудинов"} {
		if !strings.Contains(l, want) {
			t.Errorf("Line() = %q: нет %q", l, want)
		}
	}
}
