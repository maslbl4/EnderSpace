package printer

import (
	"encoding/json"
	"sort"
	"strings"
)

// State — накопленное состояние принтера: первое WS-сообщение — полный
// снимок, далее принтер шлёт дельты; Merge складывает их в одну картину
// (незаполненные поля дельты НЕ затирают прежние значения). Общий для
// обоих драйверов: Creality шлёт дельты состояния, Moonraker — дельты
// objects (print_stats и т.п.).
type State struct {
	data map[string]json.RawMessage
}

func NewState() *State {
	return &State{data: map[string]json.RawMessage{}}
}

// Merge складывает JSON-объект (полный снимок или дельту) в состояние.
func (s *State) Merge(raw []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if s.data == nil {
		s.data = map[string]json.RawMessage{}
	}
	for k, v := range m {
		s.data[k] = v
	}
	return nil
}

// Dump — все накопленные поля состояния одной строкой (для логов/диагностики).
func (s *State) Dump() string {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.Write(s.data[k])
	}
	return b.String()
}

// Int возвращает целочисленное поле состояния (отсутствует/не число → ok=false).
func (s *State) Int(key string) (int, bool) {
	v, ok := s.data[key]
	if !ok {
		return 0, false
	}
	var n float64
	if err := json.Unmarshal(v, &n); err != nil {
		return 0, false
	}
	return int(n), true
}

// Str возвращает строковое поле состояния (отсутствует/не строка → ok=false).
func (s *State) Str(key string) (string, bool) {
	v, ok := s.data[key]
	if !ok {
		return "", false
	}
	var x string
	if err := json.Unmarshal(v, &x); err != nil {
		return "", false
	}
	return x, true
}
