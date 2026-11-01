package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ---------- Пароль для просмотра: хранение (37) ----------
//
// В базе — не сам пароль, а «отпечаток»: PBKDF2-SHA256 с солью и 600 000 повторами.
//   - Соль — случайные байты у каждого пароля: два одинаковых пароля дают разные отпечатки,
//     и готовые таблицы «пароль → хеш» не помогут.
//   - 600 000 повторов — проверка одного пароля занимает доли секунды: человеку незаметно,
//     а перебор миллионов вариантов становится очень долгим.
// Строка: pbkdf2-sha256$600000$<соль base64>$<отпечаток base64>

const (
	pwKey  = "view_password" // ключ в таблице settings
	pwIter = 600_000
	pwLen  = 32
	pwMin  = 6 // короче — слишком легко угадать
)

// ErrShortPassword — пароль короче pwMin символов
var ErrShortPassword = fmt.Errorf("пароль должен быть не короче %d символов", pwMin)

// HashPassword — отпечаток пароля для хранения в базе
func HashPassword(pw string) (string, error) {
	if len([]rune(pw)) < pwMin {
		return "", ErrShortPassword
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pwIter, pwLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return "pbkdf2-sha256$" + strconv.Itoa(pwIter) + "$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(key), nil
}

// checkPassword — подходит ли пароль к отпечатку. Сравнение — за постоянное время
// (subtle.ConstantTimeCompare): по времени ответа нельзя угадать, сколько байт совпало.
func checkPassword(stored, pw string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false, errors.New("неизвестный формат пароля в базе")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, errors.New("неверное число повторов в пароле")
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false, errors.New("испорченный пароль в базе")
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// PasswordKey — ключ настройки (для команды plc-analyzer.exe password)
func PasswordKey() string { return pwKey }
