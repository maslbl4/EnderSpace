package printer

import "encoding/json"

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
	PrintTime *int     // секунды
	Filament  *float64 // граммы
	Thumb     string   // превью, data URI ("" — нет/ошибка)
}

// Storage — ёмкость внутренней памяти (eMMC). Total может быть оценкой
// (константа из конфига), т.к. у принтера нет df-эндпоинта.
type Storage struct {
	Total int64
	Used  int64
	Free  int64
}

// PrinterStatus — текущее состояние подключения.
type PrinterStatus struct {
	IP        string
	Connected bool
	Printing  bool   // принтер сейчас печатает (если API сообщает)
	Model     string // код модели, например "F005"
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
