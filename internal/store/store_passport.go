package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ---------- Настройка паспорта циклов (34a) ----------
//
// Паспорт — признаки каждого цикла смены (пакет passport). Чтобы служба сама считала его
// по каждой законченной смене, ей нужно знать то, что на странице «Шаги» человек выбирает
// руками: сигнал шага, режим «автомат» и исходный шаг. Это одна строка на ПЛК.
//
// Сигналы храним ПО ИМЕНИ, а не по номеру: файлы записей знают сигналы только по имени,
// и паспорт считается и по старым файлам — за все 30 дней, что лежат на диске.

// Passport — как искать циклы ПЛК
type Passport struct {
	PLCID   int64
	Step    string // сигнал номера шага
	Mode    string // сигнал режима работы; "" — режим не учитывать
	ModeVal int64  // значение режима, в котором считать циклы
	Home    int64  // исходный шаг — граница цикла
	Saved   time.Time
}

// passportSchema — миграция 8. Удалили ПЛК — удаляется и его настройка (CASCADE).
// saved_ms — когда сохранили: паспорта смен, посчитанные раньше, — по старой настройке,
// служба пересчитает их сама.
const passportSchema = `CREATE TABLE passports (
	plc_id   INTEGER PRIMARY KEY REFERENCES plcs(id) ON DELETE CASCADE,
	step_sig TEXT    NOT NULL,
	mode_sig TEXT    NOT NULL DEFAULT '',
	mode_val INTEGER NOT NULL DEFAULT 0,
	home     INTEGER NOT NULL,
	saved_ms INTEGER NOT NULL
 );`

// ErrPassport — не выбран сигнал шага
var ErrPassport = errors.New("для паспорта нужен сигнал номера шага")

const passportCols = `plc_id, step_sig, mode_sig, mode_val, home, saved_ms`

func scanPassport(sc interface{ Scan(...any) error }) (Passport, error) {
	var p Passport
	var ms int64
	err := sc.Scan(&p.PLCID, &p.Step, &p.Mode, &p.ModeVal, &p.Home, &ms)
	p.Saved = time.UnixMilli(ms)
	return p, err
}

// Passport — настройка ПЛК; ErrNotFound — паспорт для этого ПЛК не настроен
func (s *Store) Passport(ctx context.Context, plcID int64) (Passport, error) {
	p, err := scanPassport(s.db.QueryRowContext(ctx,
		`SELECT `+passportCols+` FROM passports WHERE plc_id = ?`, plcID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Passports — все настроенные ПЛК: по ним служба считает паспорта смен
func (s *Store) Passports(ctx context.Context) ([]Passport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+passportCols+` FROM passports ORDER BY plc_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passport
	for rows.Next() {
		p, err := scanPassport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePassport — сохранить (или заменить) настройку ПЛК; запись в журнал изменений.
// UPSERT: строки нет — вставить, есть — обновить. Одним запросом, без «сначала проверь».
func (s *Store) SavePassport(ctx context.Context, who string, p Passport) error {
	p.Step, p.Mode = strings.TrimSpace(p.Step), strings.TrimSpace(p.Mode)
	if p.Step == "" {
		return ErrPassport
	}
	what := "шаг «" + p.Step + "», исходный " + strconv.FormatInt(p.Home, 10)
	if p.Mode != "" {
		what += ", режим «" + p.Mode + "» = " + strconv.FormatInt(p.ModeVal, 10)
	}
	_, err := s.change(ctx, who, "паспорт циклов", what,
		`INSERT INTO passports (`+passportCols+`) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (plc_id) DO UPDATE SET step_sig = excluded.step_sig, mode_sig = excluded.mode_sig,
		   mode_val = excluded.mode_val, home = excluded.home, saved_ms = excluded.saved_ms`,
		p.PLCID, p.Step, p.Mode, p.ModeVal, p.Home, time.Now().UnixMilli())
	return err
}
