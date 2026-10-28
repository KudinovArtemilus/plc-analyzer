package passport

import (
	"encoding/csv"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// WriteCSV — паспорт в CSV: строка на цикл.
//
// Формат тот же, что у выгрузки из «Истории»: «;» и десятичная запятая — файл открывается
// в русском Excel двойным щелчком. В Python: pd.read_csv(path, sep=";", decimal=",").
//
// Имена столбцов латиницей (cycle_s, step_12_s, Pressure.max) — их удобно писать в коде:
// df["cycle_s"]. Пустая ячейка — значения нет (шаг в этом цикле не проходили, у сигнала
// не было точек); pandas прочитает её как NaN, и в медианы она не попадёт, в отличие от 0.
func (t Table) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	cw.Comma = ';'

	head := []string{"start", "cycle_s", "idle_s", "idle_cut", "passes", "slow_step", "slow_s"}
	for _, s := range t.Steps {
		head = append(head, "step_"+strconv.FormatInt(s, 10)+"_s")
	}
	for _, n := range t.Num {
		head = append(head, n+".min", n+".max", n+".mean")
	}
	for _, n := range t.Bits {
		head = append(head, n+".on")
	}
	if err := cw.Write(head); err != nil {
		return err
	}

	for _, r := range t.Rows {
		row := []string{
			time.UnixMilli(r.From).Format("2006-01-02 15:04:05.000"),
			sec(r.Dur()), sec(r.IdleMs), flag(r.IdleCut), strconv.Itoa(r.Passes),
			"", "",
		}
		if r.SlowMs > 0 { // есть с чем сравнивать — шаг, затянувшийся сильнее всего
			row[5], row[6] = strconv.FormatInt(r.SlowStep, 10), sec(r.SlowMs)
		}
		for _, s := range t.Steps {
			if ms, ok := r.StepMs[s]; ok {
				row = append(row, sec(ms))
			} else {
				row = append(row, "")
			}
		}
		for _, n := range t.Num {
			if a := r.Num[n]; a.OK {
				row = append(row, num(a.Min), num(a.Max), num(math.Round(a.Mean*1e4)/1e4))
			} else {
				row = append(row, "", "", "")
			}
		}
		for _, n := range t.Bits {
			row = append(row, strconv.Itoa(r.Ons[n]))
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// sec: 7250 мс → "7,25"
func sec(ms int64) string { return num(float64(ms) / 1000) }

// num: 85.3 → "85,3". Среднее перед этим округляем до 4 знаков: дальше шум, а файл раздувается
func num(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
