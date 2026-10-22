package plc

import "testing"

func TestParseAddress(t *testing.T) {
	tests := []struct {
		in      string
		want    Address
		wantErr bool
	}{
		{"DB11.DBW90", Address{DB: 11, Byte: 90, Kind: 'W'}, false},
		{"db11.dbd100", Address{DB: 11, Byte: 100, Kind: 'D'}, false},
		{" DB3000.DBX4.3 ", Address{DB: 3000, Byte: 4, Bit: 3, Kind: 'X'}, false},
		{"DB1.DBB0", Address{DB: 1, Byte: 0, Kind: 'B'}, false},
		{"DB11.DBX4", Address{}, true},     // нет номера бита
		{"DB11.DBX4.8", Address{}, true},   // бит больше 7
		{"DB11.DBW90.1", Address{}, true},  // бит у слова
		{"DB11DBW90", Address{}, true},     // нет точки
		{"DB0.DBW2", Address{}, true},      // DB0 не бывает
		{"M10.0", Address{}, true},         // пока только DB
		{"", Address{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseAddress(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ошибка = %v, ожидалась ошибка: %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("получили %+v, ожидалось %+v", got, tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	// Буфер начинается с байта 10 DB
	const start = 10
	buf := []byte{
		0x00, 0x0A, // байты 10-11: INT 10
		0xFF, 0xF6, // байты 12-13: INT -10 (или WORD 65526)
		0x3F, 0xC0, 0x00, 0x00, // байты 14-17: REAL 1.5
		0b00001000, // байт 18: установлен только бит 3
	}

	tests := []struct {
		addr string
		typ  DataType
		want float64
	}{
		{"DB1.DBW10", Int, 10},
		{"DB1.DBW12", Int, -10},
		{"DB1.DBW12", Word, 65526},
		{"DB1.DBD14", Real, 1.5},
		{"DB1.DBX18.3", Bool, 1},
		{"DB1.DBX18.2", Bool, 0},
		{"DB1.DBB18", Byte, 8},
	}

	for _, tt := range tests {
		t.Run(tt.addr+" "+string(tt.typ), func(t *testing.T) {
			a, err := ParseAddress(tt.addr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(buf, start, a, tt.typ)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("получили %v, ожидалось %v", got, tt.want)
			}
		})
	}

	// Адрес за пределами буфера — ошибка, а не падение программы
	a, _ := ParseAddress("DB1.DBW20")
	if _, err := Decode(buf, start, a, Int); err == nil {
		t.Error("ожидалась ошибка для адреса вне блока")
	}
}

func TestCheckType(t *testing.T) {
	d, _ := ParseAddress("DB1.DBD100")
	if err := CheckType(d, Real); err != nil {
		t.Errorf("REAL на DBD должен подходить: %v", err)
	}
	if err := CheckType(d, Int); err == nil {
		t.Error("INT на DBD не должен подходить")
	}
}
