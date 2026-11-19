package gcode

import (
	"regexp"
	"strconv"
)

// TimeFromName — оценка времени печати из имени файла. OrcaSlicer/Bambu
// дописывают её в название: «orcs_3_PLA_2h30m.gcode» → 2ч 30м. Суффикс —
// 1..3 компонента «Nh»/«Nm»/«Ns» перед «.gcode». ok=false, если суффикс
// отсутствует (тогда UI показывает «—»).
func TimeFromName(name string) (int, bool) {
	m := reNameTime.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	var total int
	for _, p := range []struct {
		re  *regexp.Regexp
		mul int
	}{
		{reNameH, 3600},
		{reNameM, 60},
		{reNameS, 1},
	} {
		if g := p.re.FindStringSubmatch(m[1]); g != nil {
			n, err := strconv.Atoi(g[1])
			if err != nil {
				return 0, false
			}
			total += n * p.mul
		}
	}
	if total == 0 {
		return 0, false
	}
	return total, true
}

// ApplyFallbacks — дополняет Info, когда заголовок G-кода молчит (OrcaSlicer
// 2.5.1 время/граммы в файл не пишет): время — из суффикса имени, граммы —
// из длины филамента, которую принтер знает сам (WS-список, поле length).
func ApplyFallbacks(name string, filamentMM int, info Info) Info {
	if info.TimeSeconds == nil {
		if v, ok := TimeFromName(name); ok {
			info.TimeSeconds = &v
		}
	}
	if info.FilamentGrams == nil && filamentMM > 0 {
		d := DefaultDensity
		if info.Density != nil {
			d = *info.Density
		}
		g := MMToGrams(float64(filamentMM), d)
		info.FilamentGrams = &g
	}
	return info
}

var (
	// Опциональная группа: «2h30m», «19m22s», «13h1m»; пустое совпадение
	// допустимо (тогда total = 0 → ok=false).
	reNameTime = regexp.MustCompile(`_((?:\d+h)?(?:\d+m)?(?:\d+s)?)\.gcode$`)
	reNameH    = regexp.MustCompile(`(\d+)h`)
	reNameM    = regexp.MustCompile(`(\d+)m`)
	reNameS    = regexp.MustCompile(`(\d+)s`)
)
