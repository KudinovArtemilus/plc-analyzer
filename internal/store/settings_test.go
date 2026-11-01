package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, err := st.Setting(ctx, "k"); v != "" || err != nil {
		t.Fatalf("нет настройки: %q, %v", v, err)
	}
	st.SetSetting(ctx, "тест", "k", "один", "задано")
	st.SetSetting(ctx, "тест", "k", "два", "изменено")
	if v, _ := st.Setting(ctx, "k"); v != "два" {
		t.Errorf("после замены: %q", v)
	}
	if err := st.SetSetting(ctx, "тест", "k", "", "снято"); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Setting(ctx, "k"); v != "" {
		t.Errorf("после удаления: %q", v)
	}
	// Удалить то, чего нет, — не ошибка
	if err := st.SetSetting(ctx, "тест", "нет", "", "снято"); err != nil {
		t.Errorf("удаление несуществующей: %v", err)
	}
}
