package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ---------- Справочники «число → текст» ----------
//
// Коды ошибок привода, номера шагов цепочки… Справочники общие для всех ПЛК:
// один список ошибок привода годится для всех станков с таким приводом.
// Сигнал (целое число) ссылается на справочник через signals.dict_id.

// Dict — справочник
type Dict struct {
	ID    int64
	Name  string
	Items int // сколько строк (для списка справочников)
	Used  int // скольким сигналам назначен
}

// DictItem — строка справочника
type DictItem struct {
	Value int64
	Text  string
}

// dictsSchema — миграция 6.
// dict_items удаляются вместе со справочником (CASCADE); у сигналов ссылка
// при удалении справочника обнуляется (SET NULL) — сигнал остаётся, просто без текстов.
const dictsSchema = `CREATE TABLE dicts (
	id   INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE
 );
 CREATE TABLE dict_items (
	dict_id INTEGER NOT NULL REFERENCES dicts(id) ON DELETE CASCADE,
	value   INTEGER NOT NULL,
	text    TEXT    NOT NULL,
	PRIMARY KEY (dict_id, value)
 );
 ALTER TABLE signals ADD COLUMN dict_id INTEGER REFERENCES dicts(id) ON DELETE SET NULL;`

// ErrDictName — пустое или занятое имя справочника
var ErrDictName = errors.New("имя справочника пустое или уже занято")

// Dicts — все справочники по имени, с числом строк и сигналов
func (s *Store) Dicts(ctx context.Context) ([]Dict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name,
		       (SELECT COUNT(*) FROM dict_items i WHERE i.dict_id = d.id),
		       (SELECT COUNT(*) FROM signals g WHERE g.dict_id = d.id)
		FROM dicts d ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dict
	for rows.Next() {
		var d Dict
		if err := rows.Scan(&d.ID, &d.Name, &d.Items, &d.Used); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDict — новый пустой справочник
func (s *Store) AddDict(ctx context.Context, who, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, ErrDictName
	}
	id, err := s.change(ctx, who, "добавлен справочник", name,
		`INSERT INTO dicts (name) VALUES (?)`, name)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return 0, ErrDictName
	}
	return id, err
}

// DeleteDict — удалить справочник со всеми строками; у сигналов ссылка обнулится сама
func (s *Store) DeleteDict(ctx context.Context, who string, d Dict) error {
	_, err := s.change(ctx, who, "удалён справочник", d.Name,
		`DELETE FROM dicts WHERE id = ?`, d.ID)
	return err
}

// DictItems — строки справочника по возрастанию числа
func (s *Store) DictItems(ctx context.Context, dictID int64) ([]DictItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT value, text FROM dict_items WHERE dict_id = ? ORDER BY value`, dictID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DictItem
	for rows.Next() {
		var it DictItem
		if err := rows.Scan(&it.Value, &it.Text); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetDictItem — добавить строку или заменить текст у существующего числа (UPSERT)
func (s *Store) SetDictItem(ctx context.Context, who string, dictID int64, it DictItem) error {
	it.Text = strings.TrimSpace(it.Text)
	if it.Text == "" {
		return errors.New("текст пустой")
	}
	_, err := s.change(ctx, who, "справочник: строка",
		fmt.Sprintf("№%d: %d — %s", dictID, it.Value, it.Text), `
		INSERT INTO dict_items (dict_id, value, text) VALUES (?, ?, ?)
		ON CONFLICT (dict_id, value) DO UPDATE SET text = excluded.text`,
		dictID, it.Value, it.Text)
	return err
}

// DeleteDictItem — убрать строку справочника
func (s *Store) DeleteDictItem(ctx context.Context, who string, dictID, value int64) error {
	_, err := s.change(ctx, who, "справочник: строка удалена",
		fmt.Sprintf("№%d: %d", dictID, value),
		`DELETE FROM dict_items WHERE dict_id = ? AND value = ?`, dictID, value)
	return err
}

// DictTexts — тексты нескольких справочников разом: номер справочника → число → текст.
// Для опроса (сообщения аварий) и графиков (подписи шагов).
func (s *Store) DictTexts(ctx context.Context, ids []int64) (map[int64]map[int64]string, error) {
	out := map[int64]map[int64]string{}
	for _, id := range ids {
		if _, done := out[id]; done || id == 0 {
			continue
		}
		items, err := s.DictItems(ctx, id)
		if err != nil {
			return nil, err
		}
		m := make(map[int64]string, len(items))
		for _, it := range items {
			m[it.Value] = it.Text
		}
		out[id] = m
	}
	return out, nil
}
