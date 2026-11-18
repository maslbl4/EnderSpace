package gcode

import (
	"math"
	"strings"
	"testing"
)

// multipartWrapper имитирует обёртку, которую сервер принтера хранит в начале
// скачиваемого G-кода (строки без «;»).
const multipartWrapper = `
--------------------------2a0ac08189aaaeb7
Content-Disposition: form-data; name="file"; filename="orcs_3_PLA_2h30m.gcode"
Content-Type: application/octet-stream

`

func parseString(t *testing.T, s string) Info {
	t.Helper()
	info, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return info
}

func TestParseOrcaTime(t *testing.T) {
	info := parseString(t, "; estimated printing time (normal mode) = 2h 29m 52s\nG28\n")
	if info.TimeSeconds == nil {
		t.Fatal("TimeSeconds = nil, want 8992")
	}
	if *info.TimeSeconds != 8992 { // 2*3600 + 29*60 + 52
		t.Fatalf("TimeSeconds = %d, want 8992", *info.TimeSeconds)
	}
}

func TestParseOrcaTimeWithoutModeSuffix(t *testing.T) {
	info := parseString(t, "; estimated printing time = 1h 0m 5s\n")
	if info.TimeSeconds == nil || *info.TimeSeconds != 3605 {
		t.Fatalf("TimeSeconds = %v, want 3605", info.TimeSeconds)
	}
}

func TestParseOrcaGrams(t *testing.T) {
	info := parseString(t, "; total filament used [g] = 9.09\n")
	if info.FilamentGrams == nil {
		t.Fatal("FilamentGrams = nil, want 9.09")
	}
	if math.Abs(*info.FilamentGrams-9.09) > 1e-9 {
		t.Fatalf("FilamentGrams = %v, want 9.09", *info.FilamentGrams)
	}
}

// Фолбэк: только «; filament used [mm]» (без [g]) — пересчёт по длине:
// V = L · π·(1.75/2)² мм³; граммы = V/1000 · плотность (1.24 из заголовка).
func TestParseMMFallback(t *testing.T) {
	src := "; filament_density: 1.24\n; filament used [mm] = 3048.81\n"
	info := parseString(t, src)
	if info.FilamentGrams == nil {
		t.Fatal("FilamentGrams = nil, want пересчёт из мм")
	}
	// 3048.81 * π*0.875² / 1000 * 1.24 ≈ 9.093
	want := 3048.81 * math.Pi * 0.875 * 0.875 / 1000.0 * 1.24
	if math.Abs(*info.FilamentGrams-want) > 0.01 {
		t.Fatalf("FilamentGrams = %v, want ≈ %v", *info.FilamentGrams, want)
	}
}

// Плотность из заголовка учитывается в пересчёте.
func TestParseMMFallbackCustomDensity(t *testing.T) {
	src := "; filament_density: 1.04\n; filament used [mm] = 1000\n"
	info := parseString(t, src)
	if info.FilamentGrams == nil {
		t.Fatal("FilamentGrams = nil")
	}
	want := 1000.0 * math.Pi * 0.875 * 0.875 / 1000.0 * 1.04
	if math.Abs(*info.FilamentGrams-want) > 1e-9 {
		t.Fatalf("FilamentGrams = %v, want ≈ %v", *info.FilamentGrams, want)
	}
}

func TestParseCompatibilityTime(t *testing.T) {
	info := parseString(t, "; TIME = 09:20:11\n")
	if info.TimeSeconds == nil || *info.TimeSeconds != 33611 { // 9*3600+20*60+11
		t.Fatalf("TimeSeconds = %v, want 33611", info.TimeSeconds)
	}
}

func TestParseCompatibilityTotalPrintingTime(t *testing.T) {
	info := parseString(t, ";Total printing time: 1:02:03\n")
	if info.TimeSeconds == nil || *info.TimeSeconds != 3723 { // 3600+120+3
		t.Fatalf("TimeSeconds = %v, want 3723", info.TimeSeconds)
	}
}

func TestParseMultipartWrapperIgnored(t *testing.T) {
	src := multipartWrapper +
		"; HEADER_BLOCK_START\n; total layer number: 411\n; HEADER_BLOCK_END\n" +
		"; estimated printing time (normal mode) = 2h 29m 52s\n" +
		"; total filament used [g] = 9.09\nG28\nG1 X10\n"
	info := parseString(t, src)
	if info.TimeSeconds == nil || *info.TimeSeconds != 8992 {
		t.Fatalf("TimeSeconds = %v, want 8992 (обёртка не должна ломать парсинг)", info.TimeSeconds)
	}
	if info.FilamentGrams == nil || math.Abs(*info.FilamentGrams-9.09) > 1e-9 {
		t.Fatalf("FilamentGrams = %v, want 9.09", info.FilamentGrams)
	}
}

func TestParseNoTime(t *testing.T) {
	info := parseString(t, "G28\nG1 X10 Y10\n")
	if info.TimeSeconds != nil {
		t.Fatalf("TimeSeconds = %v, want nil", *info.TimeSeconds)
	}
	if info.FilamentGrams != nil {
		t.Fatalf("FilamentGrams = %v, want nil", *info.FilamentGrams)
	}
}

func TestParseEmptyFile(t *testing.T) {
	info := parseString(t, "")
	if info.TimeSeconds != nil || info.FilamentGrams != nil {
		t.Fatalf("пустой файл: TimeSeconds=%v FilamentGrams=%v, want nil/nil", *info.TimeSeconds, *info.FilamentGrams)
	}
}

// Парсинг должен остановиться на первых совпадениях в заголовке, не зависая
// на больших файлах: проверяем, что данные после заголовка не влияют.
func TestParseHeaderOnly(t *testing.T) {
	var b strings.Builder
	b.WriteString(multipartWrapper)
	b.WriteString("; estimated printing time (normal mode) = 1h 0m 0s\n")
	b.WriteString("; total filament used [g] = 10\n")
	// «двойное» совпадение дальше — должно игнорироваться
	for i := 0; i < 1000; i++ {
		b.WriteString("G1 X10 Y10 E1\n")
	}
	b.WriteString("; estimated printing time (normal mode) = 99h 0m 0s\n")
	info := parseString(t, b.String())
	if info.TimeSeconds == nil || *info.TimeSeconds != 3600 {
		t.Fatalf("TimeSeconds = %v, want 3600 (первое совпадение)", info.TimeSeconds)
	}
}
