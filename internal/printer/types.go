package printer

import (
	"encoding/json"
	"sort"
	"strings"
)

// Info — ответ GET /info.
type Info struct {
	MAC   string
	Model string // например "F005" (Ender-3 V3 KE)
}

// RawFile — одна запись из WS-списка файлов (до обогащения G-кодом).
// Формат записи: path:name:size:layer:epoch:filament_mm:thumbnail.
type RawFile struct {
	Path       string
	Name       string
	Size       int64
	Layer      float64 // высота слоя, мм
	Epoch      int64   // unix-time загрузки
	FilamentMM int     // длина филамента, мм
	Thumb      string  // путь к превью на принтере
}

// FileMeta — файл из памяти принтера, обогащённый данными G-кода.
// Nil-поля → «—» в UI (время/граммы не найдены).
type FileMeta struct {
	Path      string   // полный путь на принтере (для удаления/ключа кэша)
	Name      string   // basename
	Size      int64    // байты
	Epoch     int64    // unix-time загрузки (из WS-списка; 0 — неизвестно)
	PrintTime *int     // секунды
	Filament  *float64 // граммы
	Thumb     string   // превью, data URI ("" — нет/ошибка)
}

// Storage — ёмкость внутренней памяти (eMMC). Total и System — оценки
// (константы из конфига), т.к. у принтера нет df-эндпоинта.
type Storage struct {
	Total  int64
	Used   int64 // пользовательские G-коды
	System int64 // прошивка + системные файлы (оценка)
	Free   int64 // Total − Used − System (оценка)
}

// PrinterStatus — текущее состояние подключения.
type PrinterStatus struct {
	IP              string
	Connected       bool
	Printing        bool   // принтер сейчас печатает (если API сообщает)
	PrintFile       string // полный путь печатающегося файла ("" — не печатает)
	Model           string // код модели, например "F005"
	FirmwareVersion string // версия прошивки (DWIN sw ver), "" — неизвестна
}

// FirmwareVersion извлекает версию прошивки из поля modelVersion WS-снимка:
// пары «ключ:значение», разделённые «;», например
// "printer hw ver:;printer sw ver:;DWIN hw ver:F005;DWIN sw ver:V1.1.0.15;".
// Версия ОС/прошивки — «DWIN sw ver»; «DWIN hw ver» дублирует модель,
// «printer hw/sw ver» у F005 пусты. Нет поля/пусто → "".
func FirmwareVersion(modelVersion string) string {
	for _, seg := range strings.Split(modelVersion, ";") {
		k, v, ok := strings.Cut(seg, ":")
		if ok && k == "DWIN sw ver" {
			return v
		}
	}
	return ""
}

// State — накопленное состояние принтера: первое WS-сообщение — полный
// снимок, далее принтер шлёт дельты; Merge складывает их в одну картину
// (незаполненные поля дельты НЕ затирают прежние значения).
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
