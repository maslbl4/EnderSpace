package gcode

import "testing"

func TestTimeFromName(t *testing.T) {
	cases := []struct {
		name string
		want int
		ok   bool
	}{
		{"orcs_3_PLA_2h30m.gcode", 2*3600 + 30*60, true},
		{"orcs_PLA_19m22s.gcode", 19*60 + 22, true},
		{"all_PLA_13h1m.gcode", 13*3600 + 60, true},
		{"x_PLA_1h0m5s.gcode", 3605, true},
		{"Holder.gcode", 0, false},
		{"box_123.gcode", 0, false}, // число без h/m/s — не время
		{"part.gcode", 0, false},
		{"orcs_PLA_2h30m", 0, false}, // без .gcode
	}
	for _, c := range cases {
		got, ok := TimeFromName(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("TimeFromName(%q) = %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestMMToGrams(t *testing.T) {
	// 1000 мм 1.75 при 1.24 г/см³ → 1 × π×0.875²/1000… нет: 1000·π·0.875²/1000·1.24
	want := 1000.0 * 3.141592653589793 * 0.875 * 0.875 / 1000.0 * 1.24
	got := MMToGrams(1000, 1.24)
	if got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("MMToGrams(1000, 1.24) = %v, want %v", got, want)
	}
}
