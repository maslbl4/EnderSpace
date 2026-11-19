// Package config — локальный конфиг приложения (config.json в каталоге
// конфигурации): IP последнего принтера и общая ёмкость eMMC.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Filename — имя файла конфига в каталоге конфигурации.
const Filename = "config.json"

// DefaultTotalBytes — общая ёмкость eMMC по умолчанию: 8 GB (общая с
// прошивкой; принтер не отдаёт df, поэтому total — константа из конфига).
const DefaultTotalBytes int64 = 8 * 1024 * 1024 * 1024

// DefaultSystemBytes — оценка объёма прошивки и системных файлов по
// умолчанию: 512 MiB (среднее экспертного диапазона 300–800 МБ для принтеров
// этого класса; принтер реальный объём системы не сообщает).
const DefaultSystemBytes int64 = 512 * 1024 * 1024

// Config — настройки приложения.
type Config struct {
	LastIP      string `json:"lastIP"`
	TotalBytes  int64  `json:"totalBytes,omitempty"`
	SystemBytes int64  `json:"systemBytes,omitempty"`
}

// Total — общая ёмкость памяти: явный TotalBytes или дефолт 8 GiB.
func (c *Config) Total() int64 {
	if c.TotalBytes > 0 {
		return c.TotalBytes
	}
	return DefaultTotalBytes
}

// System — оценка объёма прошивки и системных файлов: явный SystemBytes
// или дефолт 512 MiB.
func (c *Config) System() int64 {
	if c.SystemBytes > 0 {
		return c.SystemBytes
	}
	return DefaultSystemBytes
}

// Load читает config.json из dir. Отсутствующий или повреждённый файл —
// дефолты без ошибки (приложение не должно умирать из-за мусора в конфиге).
func Load(dir string) (*Config, error) {
	c := &Config{}
	data, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		return c, nil // файла нет — дефолты
	}
	if err := json.Unmarshal(data, c); err != nil {
		return &Config{}, nil // повреждён — дефолты
	}
	return c, nil
}

// Save записывает config.json в dir (создаёт каталог при необходимости).
func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, Filename), data, 0o644)
}
