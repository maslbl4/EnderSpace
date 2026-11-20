// Package printer — общее абстрагирование принтера: интерфейс драйверов
// и типы, которые драйверы обменивают со слоем приложения. Протокольные
// детали конкретного принтера (Creality F005, HTTP+WS) живут в
// internal/printer/creality; режим без железа — в internal/printer/emulator.
package printer

import (
	"context"
	"errors"
	"io"
)

// Printer — один подключённый принтер: G-коды в его памяти и текущее
// состояние устройства. Реализации: creality.Client (реальный F005),
// emulator.Emulator (отладка, без сети).
type Printer interface {
	// DriverName — устойчивое имя драйвера ("creality", "nebulaos",
	// "emulator"); слой приложения помнит его в конфиге, чтобы при
	// следующем подключении не опрашивать обе ОС.
	DriverName() string
	// Info — модель и MAC принтера.
	Info(ctx context.Context) (Info, error)
	// ListFiles — список G-кодов из памяти принтера (до обогащения).
	ListFiles(ctx context.Context) ([]File, error)
	// DeleteFile — удалить G-код из памяти.
	DeleteFile(ctx context.Context, path, name string) error
	// PrintFile — запустить печать G-кода.
	PrintFile(ctx context.Context, path, name string) error
	// DownloadGcode — поток G-кода (обогащение: время/граммы из заголовка).
	// path — каталог файла в памяти принтера ("" — корень списка).
	DownloadGcode(ctx context.Context, path, name string) (io.ReadCloser, error)
	// DownloadThumbnail — поток PNG-превью G-кода ("" — превью нет).
	DownloadThumbnail(ctx context.Context, path, name string) (io.ReadCloser, error)
	// WatchStatus — наблюдение за состоянием устройства: драйвер сам
	// складывает снимки/дельты своего протокола в DeviceStatus и вызывает
	// fn на каждое обновление. Возвращает при отмене ctx или обрыве связи.
	WatchStatus(ctx context.Context, fn func(DeviceStatus)) error
}

// ErrNotFound — принтер ответил 404: ресурса точно нет (отличается от
// сетевого сбоя, который стоит ретраить).
var ErrNotFound = errors.New("http 404: resource not found")

// Info — сведения о модели принтера (для Creality — GET /info).
type Info struct {
	MAC   string
	Model string // например "F005" (Ender-3 V3 KE)
}

// File — один G-код в памяти принтера (общий тип; драйвер мапит wire-формат
// своего протокола в него). Path — каталог файла (в Creality — полный путь
// на принтере, в Moonraker — путь относительно gcode-корня, "" — корень);
// "Path/Name" — полный путь, которым слой приложения управляет файлом.
type File struct {
	Path       string
	Name       string
	Size       int64
	Layer      float64 // высота слоя, мм
	Epoch      int64   // unix-time загрузки (0 — неизвестно)
	FilamentMM int     // длина филамента, мм (0 — неизвестно)
	Thumb      string  // путь к превью на принтере ("" — нет)
}

// FileMeta — файл из памяти принтера, обогащённый данными G-кода.
// Nil-поля → «—» в UI (время/граммы не найдены).
type FileMeta struct {
	Path      string   // полный путь на принтере (для удаления/ключа кэша)
	Name      string   // basename
	Size      int64    // байты
	Epoch     int64    // unix-time загрузки (из списка; 0 — неизвестно)
	PrintTime *int     // секунды
	Filament  *float64 // граммы
	Thumb     string   // превью, data URI ("" — нет/ошибка)
}

// Storage — ёмкость внутренней памяти. Total и System — оценки (константы
// из конфига), т.к. у принтера нет df-эндпоинта.
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
	Printing        bool   // принтер сейчас печатает (если драйвер сообщает)
	PrintFile       string // полный путь печатающегося файла ("" — не печатает)
	Model           string // код модели, например "F005"
	FirmwareVersion string // версия прошивки ("" — неизвестна)
}

// DeviceStatus — текущее состояние устройства, нормализованное драйвером:
// сырые поля протокола (state, modelVersion и т.п.) в слой приложения
// не уходят.
type DeviceStatus struct {
	Printing  bool   // печатает (включая паузу)
	PrintFile string // полный путь печатающегося файла ("" — не печатает)
	Firmware  string // версия прошивки ("" — неизвестна)
}
