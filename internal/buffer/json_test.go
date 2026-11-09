package buffer

import (
	"encoding/json"
	"math"
	"testing"
)

// Точка Live в JSON: NaN и бесконечность — null, остальные числа — как у encoding/json
func TestPointJSON(t *testing.T) {
	p := Point{T: 1762675200000, V: []float64{1.5, math.NaN(), -2, math.Inf(1), 0, 1e21, 0.0000001, 85.3}}
	b, err := json.Marshal([]Point{p}) // так их шлёт поток Live (в составе сообщения)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"T":1762675200000,"V":[1.5,null,-2,null,0,1e+21,1e-07,85.3]}]`
	if string(b) != want {
		t.Errorf("JSON:\n %s\nждали\n %s", b, want)
	}
	// Как раньше без NaN — те же ключи, что у encoding/json по умолчанию
	std, _ := json.Marshal(struct {
		T int64
		V []float64
	}{1, []float64{2, 3.25}})
	mine, _ := json.Marshal(Point{T: 1, V: []float64{2, 3.25}})
	if string(std) != string(mine) {
		t.Errorf("без NaN формат изменился: %s / %s", mine, std)
	}
}
