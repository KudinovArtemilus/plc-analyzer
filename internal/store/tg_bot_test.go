package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestTGBotConfig(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b, _ := st.TGBotConfig(ctx)
	if b.Token != "" || len(b.Users) != 0 || b.Page != TGDefaultPage {
		t.Fatalf("по умолчанию: %+v", b)
	}
	token := "123456789:" + strings.Repeat("A", 35)
	if err := st.SetTGToken(ctx, "тест", "  "+token+"\r\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"123", "abc:" + strings.Repeat("A", 35), token + " лишнее"} {
		if err := st.SetTGToken(ctx, "тест", bad); !errors.Is(err, ErrTGToken) {
			t.Errorf("«%s»: %v", bad, err)
		}
	}
	users, err := ParseTGUsers("379479537, 123;123 456")
	if err != nil || fmt.Sprint(users) != "[379479537 123 456]" {
		t.Fatalf("ID: %v %v", users, err)
	}
	if _, err := ParseTGUsers("379479537, Артём"); !errors.Is(err, ErrTGUsers) {
		t.Errorf("буквы в ID: %v", err)
	}
	st.SetTGUsers(ctx, "тест", users)
	b, _ = st.TGBotConfig(ctx)
	if b.Token != token || fmt.Sprint(b.Users) != "[379479537 123 456]" {
		t.Errorf("после сохранения: %+v", b)
	}

	// Токена нет ни в журнале изменений, ни в его подробностях
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM changelog WHERE action LIKE ? OR details LIKE ?`, "%"+token+"%", "%"+token+"%").Scan(&n)
	if n != 0 {
		t.Error("токен попал в журнал изменений")
	}
	if err := st.SetTGPage(ctx, "тест", "http://example.com"); !errors.Is(err, ErrTGPage) {
		t.Errorf("http вместо https: %v", err)
	}
	st.SetTGToken(ctx, "тест", "")
	if b, _ = st.TGBotConfig(ctx); b.Token != "" {
		t.Error("токен не убран")
	}
}
