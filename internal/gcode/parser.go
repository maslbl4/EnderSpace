// Package gcode — однопроходный стрим-парсер заголовка G-кода:
// время печати и масса пластика из комментариев слайсера.
package gcode

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Info — что удалось вытащить из G-кода. Nil/пустое — «не найдено» (UI: «—»).
type Info struct {
	TimeSeconds   *int
	FilamentGrams *float64
	Density       *float64 // из «; filament_density: …» (г/см³)
	Thumb         []byte   // PNG-превью (слайсер встраивает в заголовок; берём крупнейший)
}

// Параметры пересчёта «мм филамента → граммы» (фолбэк, когда слайсер не
// указал [g]).
const (
	FilamentDiameterMM = 1.75
	// DefaultDensity — PLA, г/см³.
	DefaultDensity = 1.24
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
	// Первая настоящая команда G-кода — тело началось, заголовок (и все
	// вложенные превью) уже прочитаны.
	reGcodeCmd = regexp.MustCompile(`^[GM]\d`)
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
		// Превью: слайсер встраивает PNG base64 в заголовок блоками
		// «; thumbnail begin WxH N … ; thumbnail end» (обычно два: 96×96
		// и 300×300). Храним крупнейший.
		inThumb  bool
		thumbBuf []byte
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")

		// Блоки превью обрабатываем отдельным состоянием.
		switch {
		case strings.HasPrefix(line, "; thumbnail begin"):
			inThumb = true
			thumbBuf = thumbBuf[:0]
		case inThumb && strings.HasPrefix(line, "; thumbnail end"):
			inThumb = false
			if b, err := base64.StdEncoding.DecodeString(string(thumbBuf)); err == nil && len(b) > len(info.Thumb) {
				info.Thumb = b
			}
		case inThumb && strings.HasPrefix(line, ";"):
			thumbBuf = append(thumbBuf, bytes.TrimSpace([]byte(strings.TrimPrefix(line, ";")))...)
		}
		if inThumb {
			continue // base64-строки не гоняем через регулярки
		}

		// Тело G-кода началось — всё нужное уже в заголовке.
		if reGcodeCmd.MatchString(line) {
			break
		}

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
				info.Density = &f
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Info{}, err
	}
	// Фолбэк: грамм нет, но есть длина филамента → пересчёт по плотности.
	if info.FilamentGrams == nil && mmFound {
		if !densityFound {
			density = DefaultDensity
		}
		g := MMToGrams(mm, density)
		info.FilamentGrams = &g
	}
	return info, nil
}

// MMToGrams пересчитывает длину филамента в граммы:
// V = L · π·(d/2)² (мм³) → см³ → · плотность.
func MMToGrams(mmMM, density float64) float64 {
	r := FilamentDiameterMM / 2
	return mmMM * math.Pi * r * r / 1000.0 * density
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
