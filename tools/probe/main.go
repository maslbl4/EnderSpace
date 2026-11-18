// Command probe — диагностический инструмент для проверки контракта принтера
// Creality Ender-3 V3 KE (F005). Не часть приложения; используется в Task 2 (spike)
// и для ручной диагностики.
//
// Использование:
//
//	go run ./tools/probe <ip> [имя-файла.gcode]
//
// Последовательно проверяет:
//  1. GET  http://<ip>/info
//  2. GET  http://<ip>/server/files/list?root=gcodes  (Moonraker-кандидат)
//  3. GET  http://<ip>/printer                        (Moonraker-кандидат)
//  4. WS   ws://<ip>:9999  — {"method":"get","params":{"reqGcodeFile":1}}
//  5. GET  http://<ip>/downloads/gcode/<name>  (первые 2 КБ, Range)
//  6. GET  http://<ip>/downloads/humbnail/<name>.png (статус + размер)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const clientTimeout = 15 * time.Second

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe <ip> [file.gcode]")
		os.Exit(2)
	}
	ip := os.Args[1]
	file := ""
	if len(os.Args) > 2 {
		file = os.Args[2]
	}

	fmt.Println("=== 1. GET /info ===")
	get("http://"+ip+"/info", "", -1)

	fmt.Println("\n=== 2. GET /server/files/list?root=gcodes (Moonraker-кандидат) ===")
	get("http://"+ip+"/server/files/list?root=gcodes", "", 300)

	fmt.Println("\n=== 3. GET /printer (Moonraker-кандидат) ===")
	get("http://"+ip+"/printer", "", 300)

	fmt.Println("\n=== 4. WS :9999 reqGcodeFile ===")
	file, err := wsFileList(ip, file)
	if err != nil {
		fmt.Println("WS error:", err)
	}

	if file != "" {
		base := strings.TrimSuffix(file, ".gcode")
		fmt.Printf("\n=== 5. GET /downloads/gcode/%s (первые 2 КБ) ===\n", file)
		get("http://"+ip+"/downloads/gcode/"+file, "Range: bytes=0-2047", 2048)

		fmt.Printf("\n=== 6. GET /downloads/humbnail/%s.png ===\n", base)
		get("http://"+ip+"/downloads/humbnail/"+base+".png", "", 120)
	}
}

// get делает GET с опциональным заголовком (например Range) и печатает
// статус, ключевые заголовки и тело (до limit байт; limit < 0 — без обрезки).
func get(url, header string, limit int) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Println("  request error:", err)
		return
	}
	if header != "" {
		k, v, _ := strings.Cut(header, ": ")
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: clientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("  error:", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("  status=%d content-type=%s length=%d\n",
		resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	if resp.StatusCode != http.StatusOK {
		if len(body) > 0 {
			fmt.Printf("  body: %s\n", truncate(string(body), 200))
		}
		return
	}
	// Bинарные тела (PNG) не печатаем.
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "image/") {
		fmt.Printf("  binary body, %d bytes\n", len(body))
		return
	}
	l := len(body)
	if limit >= 0 && limit < l {
		l = limit
	}
	fmt.Printf("  body: %s\n", truncate(string(body[:l]), 1200))
}

// wsFileList подключается к ws://ip:9999, шлёт reqGcodeFile, собирает ответ
// retGcodeFileInfo (и первые пару push-сообщений состояния), печатает их и
// возвращает имя первого файла из списка (или то, что передано, если передано).
func wsFileList(ip, file string) (string, error) {
	conn, resp, err := websocket.DefaultDialer.Dial("ws://"+ip+":9999", nil)
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("dial: %v (http %d)", err, resp.StatusCode)
		}
		return "", fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	msg := `{"method":"get","params":{"reqGcodeFile":1}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	fmt.Println("  sent reqGcodeFile")

	deadline := time.Now().Add(8 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	first := file
	for i := 0; i < 5; i++ {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("  read stopped:", err)
			break
		}
		if msgType != websocket.TextMessage {
			continue
		}
		txt := string(data)
		if strings.Contains(txt, "retGcodeFileInfo") {
			fmt.Println("  retGcodeFileInfo:", truncate(txt, 1500))
			if first == "" {
				first = parseFirstFile(txt)
			}
		} else {
			fmt.Println("  push:", truncate(txt, 300))
		}
	}
	return first, nil
}

// parseFirstFile извлекает имя первого файла из JSON retGcodeFileInfo.
// Формат: {"retGcodeFileInfo":{"totalNum":N,"fileInfo":"path:name:size:...;..."}}
func parseFirstFile(jsonTxt string) string {
	var payload struct {
		RetGcodeFileInfo struct {
			TotalNum int    `json:"totalNum"`
			FileInfo string `json:"fileInfo"`
		} `json:"retGcodeFileInfo"`
	}
	if err := json.Unmarshal([]byte(jsonTxt), &payload); err != nil {
		fmt.Println("  (не распарсился retGcodeFileInfo:", err, ")")
		return ""
	}
	for _, entry := range strings.Split(payload.RetGcodeFileInfo.FileInfo, ";") {
		if entry == "" {
			continue
		}
		f := strings.Split(entry, ":")
		if len(f) >= 2 {
			return f[1]
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… [+%d байт]", len(s)-n)
}
