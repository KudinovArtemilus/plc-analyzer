package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- Настройки Telegram-табло (шаг 45a-2) ----------
//
// Здесь только ЧТО показывать на телефоне — по каждому ПЛК. Сама отправка (облако, бот)
// — шаги 45b–45c; пока по этим настройкам строится лишь предпросмотр сводки.
// По умолчанию ПЛК выключен: наружу не уходит ничего, пока его не включили.
//
// Состояние станка на телефоне («Автомат», «Ручной», «Наладка») задаёт человек:
//   - по коду: в состояние входят значения сигнала режима (tg_state_codes);
//     одно значение — только в одном состоянии станка (PRIMARY KEY (plc_id, code));
//   - по биту: состояние — когда этот BOOL-сигнал = 1 (bit_signal_id).
// «Авария» и «Нет связи» — встроенные, включаются галочками у станка.

// telegramSchema — миграция 13. Всё каскадно: удалили ПЛК или сигнал — его настройки
// Telegram уходят вместе с ним (состояние «по биту» — вместе со своим битом).
const telegramSchema = `CREATE TABLE tg_machines (
	plc_id         INTEGER PRIMARY KEY REFERENCES plcs(id) ON DELETE CASCADE,
	enabled        INTEGER NOT NULL DEFAULT 0,
	label          TEXT    NOT NULL DEFAULT '',
	mode_signal_id INTEGER REFERENCES signals(id) ON DELETE SET NULL,
	show_state     INTEGER NOT NULL DEFAULT 1,
	show_step      INTEGER NOT NULL DEFAULT 1,
	show_bar       INTEGER NOT NULL DEFAULT 1,
	show_signals   INTEGER NOT NULL DEFAULT 1,
	show_alarms    INTEGER NOT NULL DEFAULT 1,
	alarm_state    INTEGER NOT NULL DEFAULT 1,
	offline_state  INTEGER NOT NULL DEFAULT 1
 );
 CREATE TABLE tg_states (
	id            INTEGER PRIMARY KEY,
	plc_id        INTEGER NOT NULL REFERENCES plcs(id) ON DELETE CASCADE,
	pos           INTEGER NOT NULL DEFAULT 0,
	name          TEXT    NOT NULL,
	color         TEXT    NOT NULL,
	pct           INTEGER NOT NULL DEFAULT 1,
	bit_signal_id INTEGER REFERENCES signals(id) ON DELETE CASCADE,
	UNIQUE (plc_id, name)
 );
 CREATE TABLE tg_state_codes (
	plc_id   INTEGER NOT NULL REFERENCES plcs(id) ON DELETE CASCADE,
	code     INTEGER NOT NULL,
	state_id INTEGER NOT NULL REFERENCES tg_states(id) ON DELETE CASCADE,
	PRIMARY KEY (plc_id, code)
 );
 CREATE TABLE tg_signals (
	plc_id    INTEGER NOT NULL REFERENCES plcs(id) ON DELETE CASCADE,
	signal_id INTEGER NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
	pos       INTEGER NOT NULL DEFAULT 0,
	how       TEXT    NOT NULL DEFAULT 'value',
	label     TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (plc_id, signal_id)
 );`

// TGShow — что показывать на карточке станка
type TGShow struct {
	State   bool // состояние сейчас
	Step    bool // шаг (сигнал шага из паспорта)
	Bar     bool // доли смены по состояниям, %
	Signals bool // выбранные сигналы
	Alarms  bool // аварии за смену
}

// TGMachine — ПЛК на телефоне
type TGMachine struct {
	PLCID        int64
	Enabled      bool   // показывать в Telegram; выключен — о нём наружу не уходит ничего
	Label        string // подпись на телефоне; пусто — имя ПЛК
	ModeSignalID int64  // сигнал режима для состояний «по коду»; 0 — не выбран
	Show         TGShow
	AlarmState   bool // встроенное состояние «Авария»
	OfflineState bool // встроенное состояние «Нет связи»
}

// TGState — состояние станка: «Автомат», «Ручной»…
type TGState struct {
	ID          int64
	PLCID       int64
	Pos         int
	Name        string
	Color       string  // одно из TGColors
	Pct         bool    // считать долю смены в %
	BitSignalID int64   // 0 — по коду сигнала режима, иначе — по этому биту
	Codes       []int64 // значения сигнала режима (только для «по коду»), по возрастанию
}

// TGSignal — сигнал на карточке станка
type TGSignal struct {
	PLCID    int64
	SignalID int64
	Name     string // имя сигнала в анализаторе — для показа, из JOIN
	Type     string // тип сигнала — для показа
	Pos      int
	How      string // одно из TGHows
	Label    string // подпись на телефоне
}

// TGColors — цвета своих состояний. Красного и жёлтого нет: по ISA-101 они только
// для аварии и предупреждения.
var TGColors = []string{"blue", "steel", "green", "orange", "purple"}

// TGHows — как показывать сигнал: текущее · текущее + мин/макс · с подписью из справочника
var TGHows = []string{"value", "minmax", "dict"}

// TGPeriods — период отправки сводки, с; по умолчанию — минута
var TGPeriods = []int{10, 30, 60, 300}

const (
	tgPeriodKey     = "tg_period"
	tgDefaultPeriod = 60
	TGNameMax       = 30 // название состояния и подпись — на телефоне в одну строку
)

var (
	ErrTGName   = fmt.Errorf("название — от 1 до %d символов", TGNameMax)
	ErrTGColor  = errors.New("такого цвета нет (красный и жёлтый — только для аварий и предупреждений)")
	ErrTGHow    = errors.New("неизвестный способ показа сигнала")
	ErrTGPeriod = errors.New("период — 10 с, 30 с, 1 мин или 5 мин")
	ErrTGSignal = errors.New("сигнал другого ПЛК или не найден")
	ErrTGBit    = errors.New("состояние «по биту» — только по сигналу BOOL этого ПЛК")
	ErrTGCodes  = errors.New("значения режима — только у состояния «по коду»")
)

func oneOf[T comparable](v T, list []T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func cleanTGName(s string, allowEmpty bool) (string, error) {
	s = strings.TrimSpace(s)
	if (!allowEmpty && s == "") || utf8.RuneCountInString(s) > TGNameMax {
		return s, ErrTGName
	}
	return s, nil
}

func idRef(id int64) any { // 0 — NULL
	if id == 0 {
		return nil
	}
	return id
}

// tgLog — запись в журнал изменений в той же транзакции
func tgLog(ctx context.Context, tx *sql.Tx, who, action, details string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, action, details)
	return err
}

// signalOf — сигнал этого ПЛК: тип; чужой или нет такого — ErrTGSignal
func (s *Store) signalOf(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, plcID, signalID int64) (name, typ string, err error) {
	err = q.QueryRowContext(ctx, `SELECT name, type FROM signals WHERE id = ? AND plc_id = ?`, signalID, plcID).Scan(&name, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrTGSignal
	}
	return
}

// ---------- Станок ----------

// TGMachine — настройки ПЛК; строки ещё нет — значения по умолчанию (выключен, всё показывать)
func (s *Store) TGMachine(ctx context.Context, plcID int64) (TGMachine, error) {
	m := TGMachine{PLCID: plcID, Show: TGShow{true, true, true, true, true}, AlarmState: true, OfflineState: true}
	err := s.db.QueryRowContext(ctx, `SELECT enabled, label, COALESCE(mode_signal_id, 0),
		show_state, show_step, show_bar, show_signals, show_alarms, alarm_state, offline_state
		FROM tg_machines WHERE plc_id = ?`, plcID).Scan(&m.Enabled, &m.Label, &m.ModeSignalID,
		&m.Show.State, &m.Show.Step, &m.Show.Bar, &m.Show.Signals, &m.Show.Alarms, &m.AlarmState, &m.OfflineState)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	return m, err
}

// SaveTGMachine — сохранить настройки ПЛК (строки нет — создать)
func (s *Store) SaveTGMachine(ctx context.Context, who string, m TGMachine) error {
	label, err := cleanTGName(m.Label, true)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if m.ModeSignalID != 0 {
		if _, _, err := s.signalOf(ctx, tx, m.PLCID, m.ModeSignalID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tg_machines (plc_id, enabled, label, mode_signal_id,
		show_state, show_step, show_bar, show_signals, show_alarms, alarm_state, offline_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (plc_id) DO UPDATE SET enabled = excluded.enabled, label = excluded.label,
		mode_signal_id = excluded.mode_signal_id, show_state = excluded.show_state,
		show_step = excluded.show_step, show_bar = excluded.show_bar, show_signals = excluded.show_signals,
		show_alarms = excluded.show_alarms, alarm_state = excluded.alarm_state, offline_state = excluded.offline_state`,
		m.PLCID, m.Enabled, label, idRef(m.ModeSignalID), m.Show.State, m.Show.Step, m.Show.Bar,
		m.Show.Signals, m.Show.Alarms, m.AlarmState, m.OfflineState); err != nil {
		return err
	}
	on := "выключен"
	if m.Enabled {
		on = "включён"
	}
	if err := tgLog(ctx, tx, who, "Telegram: станок", fmt.Sprintf("ПЛК %d: %s, подпись «%s»", m.PLCID, on, label)); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- Состояния ----------

// TGStates — состояния ПЛК по порядку, со значениями режима
func (s *Store) TGStates(ctx context.Context, plcID int64) ([]TGState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, plc_id, pos, name, color, pct, COALESCE(bit_signal_id, 0)
		FROM tg_states WHERE plc_id = ? ORDER BY pos, id`, plcID)
	if err != nil {
		return nil, err
	}
	var out []TGState
	idx := map[int64]int{}
	for rows.Next() {
		var st TGState
		if err := rows.Scan(&st.ID, &st.PLCID, &st.Pos, &st.Name, &st.Color, &st.Pct, &st.BitSignalID); err != nil {
			rows.Close()
			return nil, err
		}
		idx[st.ID] = len(out)
		out = append(out, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	codes, err := s.db.QueryContext(ctx, `SELECT state_id, code FROM tg_state_codes WHERE plc_id = ? ORDER BY code`, plcID)
	if err != nil {
		return nil, err
	}
	defer codes.Close()
	for codes.Next() {
		var sid, code int64
		if err := codes.Scan(&sid, &code); err != nil {
			return nil, err
		}
		if i, ok := idx[sid]; ok {
			out[i].Codes = append(out[i].Codes, code)
		}
	}
	return out, codes.Err()
}

// checkTGState — проверки состояния (кроме кодов)
func (s *Store) checkTGState(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, st *TGState) error {
	name, err := cleanTGName(st.Name, false)
	if err != nil {
		return err
	}
	st.Name = name
	if !oneOf(st.Color, TGColors) {
		return ErrTGColor
	}
	if st.BitSignalID != 0 {
		_, typ, err := s.signalOf(ctx, q, st.PLCID, st.BitSignalID)
		if err != nil || !strings.EqualFold(typ, "BOOL") {
			return ErrTGBit
		}
	}
	return nil
}

// AddTGState — новое состояние в конец списка ПЛК
func (s *Store) AddTGState(ctx context.Context, who string, st TGState) (int64, error) {
	if err := s.checkTGState(ctx, s.db, &st); err != nil {
		return 0, err
	}
	return s.change(ctx, who, "Telegram: добавлено состояние", fmt.Sprintf("ПЛК %d: %s", st.PLCID, st.Name),
		`INSERT INTO tg_states (plc_id, pos, name, color, pct, bit_signal_id)
		 VALUES (?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM tg_states WHERE plc_id = ?), ?, ?, ?, ?)`,
		st.PLCID, st.PLCID, st.Name, st.Color, st.Pct, idRef(st.BitSignalID))
}

// UpdateTGState — название, цвет, «в %», вид (по коду / по биту). Стало «по биту» —
// значения режима у него снимаются: у такого состояния их быть не может.
func (s *Store) UpdateTGState(ctx context.Context, who string, st TGState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.checkTGState(ctx, tx, &st); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE tg_states SET name = ?, color = ?, pct = ?, bit_signal_id = ?
		WHERE id = ? AND plc_id = ?`, st.Name, st.Color, st.Pct, idRef(st.BitSignalID), st.ID, st.PLCID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if st.BitSignalID != 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tg_state_codes WHERE state_id = ?`, st.ID); err != nil {
			return err
		}
	}
	if err := tgLog(ctx, tx, who, "Telegram: изменено состояние", fmt.Sprintf("ПЛК %d: %s", st.PLCID, st.Name)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTGState — удалить состояние; его значения режима становятся «ни в одном состоянии»
func (s *Store) DeleteTGState(ctx context.Context, who string, plcID, id int64) error {
	_, err := s.change(ctx, who, "Telegram: удалено состояние", fmt.Sprintf("ПЛК %d: состояние %d", plcID, id),
		`DELETE FROM tg_states WHERE id = ? AND plc_id = ?`, id, plcID)
	return err
}

// MoveTGState — состояние выше (d = -1) или ниже (+1). Порядок важен: если сработали два
// состояния сразу (два бита), на телефоне — то, что выше.
func (s *Store) MoveTGState(ctx context.Context, who string, plcID, id int64, d int) error {
	list, err := s.TGStates(ctx, plcID)
	if err != nil {
		return err
	}
	ids := make([]int64, len(list))
	for i, st := range list {
		ids[i] = st.ID
	}
	return s.reorder(ctx, who, "Telegram: порядок состояний", fmt.Sprintf("ПЛК %d", plcID),
		`UPDATE tg_states SET pos = ? WHERE id = ?`, ids, id, d)
}

// SetTGStateCode — значение режима в состояние (on) или из него (off). Значение может быть
// только в одном состоянии станка: включили в другом — оно ПЕРЕЕЗЖАЕТ, а не задваивается.
func (s *Store) SetTGStateCode(ctx context.Context, who string, plcID, stateID, code int64, on bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bit int64
	var name string
	err = tx.QueryRowContext(ctx, `SELECT name, COALESCE(bit_signal_id, 0) FROM tg_states WHERE id = ? AND plc_id = ?`,
		stateID, plcID).Scan(&name, &bit)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bit != 0 {
		return ErrTGCodes
	}
	action := "Telegram: значение режима убрано"
	if on {
		action = "Telegram: значение режима добавлено"
		_, err = tx.ExecContext(ctx, `INSERT INTO tg_state_codes (plc_id, code, state_id) VALUES (?, ?, ?)
			ON CONFLICT (plc_id, code) DO UPDATE SET state_id = excluded.state_id`, plcID, code, stateID)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM tg_state_codes WHERE plc_id = ? AND code = ? AND state_id = ?`,
			plcID, code, stateID)
	}
	if err != nil {
		return err
	}
	if err := tgLog(ctx, tx, who, action, fmt.Sprintf("ПЛК %d: %s ← %d", plcID, name, code)); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- Сигналы ----------

// TGSignals — сигналы карточки ПЛК по порядку
func (s *Store) TGSignals(ctx context.Context, plcID int64) ([]TGSignal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.plc_id, t.signal_id, s.name, s.type, t.pos, t.how, t.label
		FROM tg_signals t JOIN signals s ON s.id = t.signal_id WHERE t.plc_id = ? ORDER BY t.pos, s.name`, plcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TGSignal
	for rows.Next() {
		var g TGSignal
		if err := rows.Scan(&g.PLCID, &g.SignalID, &g.Name, &g.Type, &g.Pos, &g.How, &g.Label); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AddTGSignal — сигнал в конец карточки; подпись — его имя, показ — текущее значение
func (s *Store) AddTGSignal(ctx context.Context, who string, plcID, signalID int64) error {
	name, _, err := s.signalOf(ctx, s.db, plcID, signalID)
	if err != nil {
		return err
	}
	label := name
	if utf8.RuneCountInString(label) > TGNameMax {
		label = string([]rune(label)[:TGNameMax])
	}
	_, err = s.change(ctx, who, "Telegram: добавлен сигнал", fmt.Sprintf("ПЛК %d: %s", plcID, name),
		`INSERT INTO tg_signals (plc_id, signal_id, pos, how, label)
		 VALUES (?, ?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM tg_signals WHERE plc_id = ?), 'value', ?)`,
		plcID, signalID, plcID, label)
	return err
}

// UpdateTGSignal — способ показа и подпись
func (s *Store) UpdateTGSignal(ctx context.Context, who string, g TGSignal) error {
	label, err := cleanTGName(g.Label, false)
	if err != nil {
		return err
	}
	if !oneOf(g.How, TGHows) {
		return ErrTGHow
	}
	_, err = s.change(ctx, who, "Telegram: изменён сигнал", fmt.Sprintf("ПЛК %d: сигнал %d — %s, «%s»", g.PLCID, g.SignalID, g.How, label),
		`UPDATE tg_signals SET how = ?, label = ? WHERE plc_id = ? AND signal_id = ?`, g.How, label, g.PLCID, g.SignalID)
	return err
}

// DeleteTGSignal — убрать сигнал с карточки (сам сигнал в анализаторе остаётся)
func (s *Store) DeleteTGSignal(ctx context.Context, who string, plcID, signalID int64) error {
	_, err := s.change(ctx, who, "Telegram: убран сигнал", fmt.Sprintf("ПЛК %d: сигнал %d", plcID, signalID),
		`DELETE FROM tg_signals WHERE plc_id = ? AND signal_id = ?`, plcID, signalID)
	return err
}

// MoveTGSignal — сигнал выше (d = -1) или ниже (+1) на карточке
func (s *Store) MoveTGSignal(ctx context.Context, who string, plcID, signalID int64, d int) error {
	list, err := s.TGSignals(ctx, plcID)
	if err != nil {
		return err
	}
	ids := make([]int64, len(list))
	for i, g := range list {
		ids[i] = g.SignalID
	}
	return s.reorder(ctx, who, "Telegram: порядок сигналов", fmt.Sprintf("ПЛК %d", plcID),
		`UPDATE tg_signals SET pos = ? WHERE signal_id = ?`, ids, signalID, d) // сигнал — у одного ПЛК
}

// reorder — поменять id местами с соседом (d = ±1) и пронумеровать всё заново.
// update — запрос «SET pos = ? WHERE <ключ> = ?». Крайний дальше не двигается — не ошибка.
func (s *Store) reorder(ctx context.Context, who, action, details, update string, ids []int64, id int64, d int) error {
	i := -1
	for k, x := range ids {
		if x == id {
			i = k
		}
	}
	if i < 0 {
		return ErrNotFound
	}
	j := i + d
	if j < 0 || j >= len(ids) {
		return nil
	}
	ids[i], ids[j] = ids[j], ids[i]
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, x := range ids {
		if _, err := tx.ExecContext(ctx, update, k+1, x); err != nil {
			return err
		}
	}
	if err := tgLog(ctx, tx, who, action, details); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- Период ----------

// TGPeriod — период отправки сводки, с
func (s *Store) TGPeriod(ctx context.Context) (int, error) {
	v, err := s.Setting(ctx, tgPeriodKey)
	if err != nil || v == "" {
		return tgDefaultPeriod, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || !oneOf(n, TGPeriods) {
		return tgDefaultPeriod, nil // испорченное значение — как по умолчанию
	}
	return n, nil
}

// SetTGPeriod — период отправки сводки, с
func (s *Store) SetTGPeriod(ctx context.Context, who string, sec int) error {
	if !oneOf(sec, TGPeriods) {
		return ErrTGPeriod
	}
	return s.SetSetting(ctx, who, tgPeriodKey, strconv.Itoa(sec), fmt.Sprintf("Telegram: период %d с", sec))
}
