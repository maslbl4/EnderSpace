package creality

import "testing"

// FirmwareVersion — разбор поля modelVersion из WS-снимка состояния.
// Живой пример (F005, 2026-11-20):
//
//	"printer hw ver:;printer sw ver:;DWIN hw ver:F005;DWIN sw ver:V1.1.0.15;"
//
// Версия прошивки (ОС) — «DWIN sw ver»; «DWIN hw ver» дублирует модель.
func TestFirmwareVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"live snapshot", "printer hw ver:;printer sw ver:;DWIN hw ver:F005;DWIN sw ver:V1.1.0.15;", "V1.1.0.15"},
		{"short", "DWIN sw ver:V2.0.0.1;", "V2.0.0.1"},
		{"empty", "", ""},
		{"no dwin sw ver", "printer hw ver:X1;printer sw ver:Y2;", ""},
		{"dwin sw ver empty", "printer hw ver:;printer sw ver:;DWIN hw ver:F005;DWIN sw ver:;", ""},
		{"garbage", "abc def", ""},
		{"spaces around key", " DWIN sw ver :V3;", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirmwareVersion(tc.in); got != tc.want {
				t.Errorf("FirmwareVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
