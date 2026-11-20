package creality

import "strings"

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
