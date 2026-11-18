// Package gcode — однопроходный стрим-парсер заголовка G-кода:
// время печати и масса пластика из комментариев слайсера.
package gcode

import (
	"bufio"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Info — что удалось вытащить из G-кода. Nil — маркер «не найдено» (UI: «—»).
type Info struct {
	TimeSeconds   *int
	FilamentGrams *float64
}

// Параметры пересчёта «мм филамента → граммы» (фолбэк, когда слайсер не
// указал [g]).
const (
	filamentDiameterMM = 1.75
	defaultDensity     = 1.24 // PLA, г/см³
)

var (
	// OrcaSlicer/Bambu: «; estimated printing time (normal mode) = 2h 29m 52s»
	// (скобочная часть может отсутствовать).
	reTimeOrca = regexp.MustCompile(`;\s*estimated printing time(?:\s*\([^)]*\))?\s*=\s*(\d+)h\s*(\d+)m\s*(\d+)s`)
	// СуперСлайсер/старые: «; TIME = 09:20:11»
	reTimeEq = regexp.MustCompile(`;\s*TIME\s*=\s*(\d+):(\d+):(\d+)`)
	// PrusaSlicer: «;Total printing time: 1:02:03»
	reTimeColon = regexp.MustCompile(`;\s*Total printing time:\s*(\d+):(\d+):(\d+)`)
	// OrcaSlicer: «; total filament used [g] = 9.09»
	reGrams = regexp.MustCompile(`;\s*total filament used \[g\]\s*=\s*(\d+(?:\.\d+)?)`)
	// OrcaSlicer: «; filament used [mm] = 3048.81»
	reMM = regexp.MustCompile(`;\s*filament used \[mm\]\s*=\s*(\d+(?:\.\d+)?)`)
	// OrcaSlicer: «; filament_density: 1.24»
	reDensity = regexp.MustCompile(`;\s*filament_density:\s*(\d+(?:\.\d+)?)`)
)

// Parse читает G-код потоково (строкa за строкой, без загрузки файла в
// память) и возвращает время печати / граммы из заголовка. Строки без «;»
// (например, multipart-обёртка, которую хранит сервер принтера) не совпадают
// с шаблонами и пропускаются. Ошибок парсинга не бывает: не нашёл — nil.
func Parse(r io.Reader) (Info, error) {
	var (
		info         Info
		mmFound      bool
		mm           float64
		densityFound bool
		density      float64
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")

		if info.TimeSeconds == nil {
			if v, ok := matchTime(reTimeOrca, line); ok {
				info.TimeSeconds = &v
			} else if v, ok := matchTime(reTimeEq, line); ok {
				info.TimeSeconds = &v
			} else if v, ok := matchTime(reTimeColon, line); ok {
				info.TimeSeconds = &v
			}
		}
		if info.FilamentGrams == nil {
			if f, ok := parseFloat(reGrams, line); ok {
				info.FilamentGrams = &f
			} else if !mmFound {
				if f, ok := parseFloat(reMM, line); ok {
					mm = f
					mmFound = true
				}
			}
		}
		if !densityFound {
			if f, ok := parseFloat(reDensity, line); ok {
				density = f
				densityFound = true
			}
		}
		// Всё нужное в заголовке собрано — не гоняем регулярки по телу.
		if info.TimeSeconds != nil && (info.FilamentGrams != nil || (mmFound && densityFound)) {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return Info{}, err
	}
	// Фолбэк: грамм нет, но есть длина филамента → пересчёт по плотности.
	if info.FilamentGrams == nil && mmFound {
		if !densityFound {
			density = defaultDensity
		}
		volumeCC := mm * math.Pi * (filamentDiameterMM / 2) * (filamentDiameterMM / 2) / 1000.0
		g := volumeCC * density
		info.FilamentGrams = &g
	}
	return info, nil
}

func matchTime(re *regexp.Regexp, line string) (int, bool) {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	a, err1 := strconv.Atoi(m[1])
	b, err2 := strconv.Atoi(m[2])
	c, err3 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return a*3600 + b*60 + c, true
}

func parseFloat(re *regexp.Regexp, line string) (float64, bool) {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
