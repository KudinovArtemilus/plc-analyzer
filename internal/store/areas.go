package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- Участки (шаг 45a-1) ----------
//
// Участок — группа ПЛК: «Мехобработка», «Прессовый». Один ПЛК — в одном участке
// (или ни в каком), в участке — сколько угодно ПЛК. Нужен Telegram-табло (станки по участкам),
// потом — меню, Live и итоги смены.

// areasSchema — миграция 12. ON DELETE SET NULL: удалили участок — его ПЛК остаются,
// просто «без участка». Опрос и записи участок не трогает.
const areasSchema = `CREATE TABLE areas (
	id   INTEGER PRIMARY KEY,
	name TEXT    NOT NULL UNIQUE,
	pos  INTEGER NOT NULL DEFAULT 0
 );
 ALTER TABLE plcs ADD COLUMN area_id INTEGER REFERENCES areas(id) ON DELETE SET NULL;`

// Area — участок; PLCs — сколько ПЛК в нём (для списка и вопроса перед удалением)
type Area struct {
	ID   int64
	Name string
	Pos  int
	PLCs int
}

// AreaNameMax — длина названия: оно на телефоне заголовком, длинное не поместится
const AreaNameMax = 40

// ErrAreaName — пустое или слишком длинное название
var ErrAreaName = fmt.Errorf("название участка — от 1 до %d символов", AreaNameMax)

func cleanAreaName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > AreaNameMax {
		return name, ErrAreaName
	}
	return name, nil
}

// Areas — участки по порядку, с числом ПЛК в каждом
func (s *Store) Areas(ctx context.Context) ([]Area, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.name, a.pos, COUNT(p.id)
		   FROM areas a LEFT JOIN plcs p ON p.area_id = a.id
		  GROUP BY a.id ORDER BY a.pos, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Area
	for rows.Next() {
		var a Area
		if err := rows.Scan(&a.ID, &a.Name, &a.Pos, &a.PLCs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddArea — новый участок в конец списка
func (s *Store) AddArea(ctx context.Context, who, name string) (int64, error) {
	name, err := cleanAreaName(name)
	if err != nil {
		return 0, err
	}
	return s.change(ctx, who, "добавлен участок", name,
		`INSERT INTO areas (name, pos) VALUES (?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM areas))`, name)
}

// RenameArea — переименовать участок
func (s *Store) RenameArea(ctx context.Context, who string, old Area, name string) error {
	name, err := cleanAreaName(name)
	if err != nil {
		return err
	}
	if name == old.Name {
		return nil
	}
	_, err = s.change(ctx, who, "переименован участок", old.Name+" → "+name,
		`UPDATE areas SET name = ? WHERE id = ?`, name, old.ID)
	return err
}

// DeleteArea — удалить участок; его ПЛК становятся «без участка» (ON DELETE SET NULL)
func (s *Store) DeleteArea(ctx context.Context, who string, a Area) error {
	_, err := s.change(ctx, who, "удалён участок", a.Name, `DELETE FROM areas WHERE id = ?`, a.ID)
	return err
}

// MoveArea — поменять участок местами с соседом: d = -1 — выше, +1 — ниже.
// Крайний участок дальше не двигается — это не ошибка.
func (s *Store) MoveArea(ctx context.Context, who string, id int64, d int) error {
	list, err := s.Areas(ctx)
	if err != nil {
		return err
	}
	i := -1
	for k, a := range list {
		if a.ID == id {
			i = k
		}
	}
	if i < 0 {
		return ErrNotFound
	}
	j := i + d
	if j < 0 || j >= len(list) {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Номера — заново по новому порядку: старые pos могли совпадать (участки с одним pos)
	list[i], list[j] = list[j], list[i]
	for k, a := range list {
		if _, err := tx.ExecContext(ctx, `UPDATE areas SET pos = ? WHERE id = ?`, k+1, a.ID); err != nil {
			return err
		}
	}
	dir := "выше"
	if d > 0 {
		dir = "ниже"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "участок "+dir, list[j].Name); err != nil {
		return err
	}
	return tx.Commit()
}

// Area — участок по номеру
func (s *Store) Area(ctx context.Context, id int64) (Area, error) {
	var a Area
	err := s.db.QueryRowContext(ctx,
		`SELECT a.id, a.name, a.pos, (SELECT COUNT(*) FROM plcs WHERE area_id = a.id)
		   FROM areas a WHERE a.id = ?`, id).Scan(&a.ID, &a.Name, &a.Pos, &a.PLCs)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// areaRef — номер участка для базы: 0 — «без участка» (NULL)
func areaRef(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
