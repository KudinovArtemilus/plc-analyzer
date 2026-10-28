package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ---------- Наборы сигналов («окна графиков») ----------
//
// Набор — именованный список сигналов одного ПЛК: «Гидравлика», «Стол», «Робот».
// На Live, в «Истории» и в слепках выбираешь набор — и видишь только его сигналы.
// Наборы хранятся в базе и общие для всех: настроил на ПК анализатора —
// у дежурных на всех ПК те же наборы.

// SignalSet — набор сигналов
type SignalSet struct {
	ID      int64
	PLCID   int64
	Name    string
	Signals []int64  // номера сигналов — в порядке страницы «Сигналы»
	Names   []string // их имена: так сигналы называются на графиках и в файлах записи
}

// setsSchema — миграция 7.
// Удалили ПЛК — его наборы удаляются (CASCADE); удалили сигнал — он исчезает из наборов,
// а сам набор остаётся. Один сигнал может быть в нескольких наборах.
const setsSchema = `CREATE TABLE signal_sets (
	id     INTEGER PRIMARY KEY,
	plc_id INTEGER NOT NULL REFERENCES plcs(id) ON DELETE CASCADE,
	name   TEXT    NOT NULL,
	UNIQUE (plc_id, name)
 );
 CREATE TABLE signal_set_items (
	set_id    INTEGER NOT NULL REFERENCES signal_sets(id) ON DELETE CASCADE,
	signal_id INTEGER NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
	PRIMARY KEY (set_id, signal_id)
 );`

// ErrSetName — пустое или уже занятое (у этого ПЛК) имя набора
var ErrSetName = errors.New("имя набора пустое или уже занято у этого ПЛК")

// Sets — наборы ПЛК по имени, у каждого — его сигналы.
// Одним запросом: LEFT JOIN даёт строку на каждую пару «набор — сигнал»
// (и одну строку с NULL у пустого набора), а в Go собираем их обратно по наборам.
func (s *Store) Sets(ctx context.Context, plcID int64) ([]SignalSet, error) {
	return s.querySets(ctx, `WHERE ss.plc_id = ?`, plcID)
}

// Set — один набор по номеру
func (s *Store) Set(ctx context.Context, id int64) (SignalSet, error) {
	list, err := s.querySets(ctx, `WHERE ss.id = ?`, id)
	if err != nil {
		return SignalSet{}, err
	}
	if len(list) == 0 {
		return SignalSet{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) querySets(ctx context.Context, where string, arg any) ([]SignalSet, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ss.id, ss.plc_id, ss.name, sg.id, sg.name
		FROM signal_sets ss
		LEFT JOIN signal_set_items it ON it.set_id = ss.id
		LEFT JOIN signals sg ON sg.id = it.signal_id
		`+where+`
		ORDER BY ss.name, ss.id, sg.id`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SignalSet
	for rows.Next() {
		var set SignalSet
		var sigID *int64 // NULL у пустого набора — поэтому указатель
		var sigName *string
		if err := rows.Scan(&set.ID, &set.PLCID, &set.Name, &sigID, &sigName); err != nil {
			return nil, err
		}
		// Строки идут по наборам подряд (ORDER BY): новый номер — новый набор
		if n := len(out); n == 0 || out[n-1].ID != set.ID {
			out = append(out, set)
		}
		if sigID != nil {
			cur := &out[len(out)-1]
			cur.Signals = append(cur.Signals, *sigID)
			cur.Names = append(cur.Names, *sigName)
		}
	}
	return out, rows.Err()
}

// AddSet — новый пустой набор
func (s *Store) AddSet(ctx context.Context, who string, plcID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, ErrSetName
	}
	id, err := s.change(ctx, who, "добавлен набор сигналов", name,
		`INSERT INTO signal_sets (plc_id, name) VALUES (?, ?)`, plcID, name)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return 0, ErrSetName
	}
	return id, err
}

// SaveSet — новое имя и новый состав набора, одной транзакцией:
// либо сохранилось всё, либо ничего (не будет набора «наполовину»).
// Сигналы другого ПЛК в набор не попадут, даже если их номер прислали вручную.
func (s *Store) SaveSet(ctx context.Context, who string, set SignalSet) error {
	set.Name = strings.TrimSpace(set.Name)
	if set.Name == "" {
		return ErrSetName
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // после Commit ничего не делает

	res, err := tx.ExecContext(ctx,
		`UPDATE signal_sets SET name = ? WHERE id = ? AND plc_id = ?`, set.Name, set.ID, set.PLCID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrSetName
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM signal_set_items WHERE set_id = ?`, set.ID); err != nil {
		return err
	}
	added := 0
	for _, sig := range set.Signals {
		// INSERT … SELECT: строка вставится, только если такой сигнал есть у этого ПЛК
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO signal_set_items (set_id, signal_id)
			SELECT ?, id FROM signals WHERE id = ? AND plc_id = ?`, set.ID, sig, set.PLCID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		added += int(n)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "изменён набор сигналов",
		fmt.Sprintf("%s: сигналов %d", set.Name, added)); err != nil {
		return err
	}
	return tx.Commit()
	// OnChange не вызываем: наборы — только про показ, опрос они не меняют
}

// DeleteSet — удалить набор; сами сигналы остаются
func (s *Store) DeleteSet(ctx context.Context, who string, set SignalSet) error {
	_, err := s.change(ctx, who, "удалён набор сигналов", set.Name,
		`DELETE FROM signal_sets WHERE id = ?`, set.ID)
	return err
}
