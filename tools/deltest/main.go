// Command deltest — воспроизведение бага «из двух удалённых файлов остался
// один» на реальном принтере, без риска для пользовательских файлов:
// создаёт два временных G-кода (POST /upload), удаляет их подряд по WS
// (ровно как internal/app.DeleteFiles) и смотрит, что осталось.
//
// Использование:
//
//	go run ./tools/deltest [ip]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const testBody = "; endspace delete test file\nG28\nG1 X10 F3000\n"

func main() {
	// Подкоманда: delete <ip> <name> — удалить один конкретный файл
	// (например, тестовый осколок) и убедиться, что он исчез.
	if len(os.Args) >= 4 && os.Args[1] == "delete" {
		wipe(os.Args[2], os.Args[3])
		return
	}
	ip := "192.168.3.166"
	if len(os.Args) > 1 {
		ip = os.Args[1]
	}
	names := []string{"endspace_deltest_a.gcode", "endspace_deltest_b.gcode"}

	// 1. Загружаем два временных файла.
	for _, n := range names {
		if err := upload("http://"+ip+"/upload/"+n, n, testBody); err != nil {
			fmt.Println("upload", n, ":", err)
			os.Exit(1)
		}
		fmt.Println("uploaded:", n)
	}
	time.Sleep(2 * time.Second)

	// 2. Проверяем, что оба в списке; запоминаем path.
	list, err := wsList(ip)
	if err != nil {
		fmt.Println("list:", err)
		os.Exit(1)
	}
	path := ""
	for _, n := range names {
		p, ok := list[n]
		if !ok {
			fmt.Println("file not in list after upload:", n)
			os.Exit(1)
		}
		path = p
	}
	fmt.Println("both files present, path:", path)

	// 3. Удаляем подряд — ровно как app.DeleteFiles: dial → send → close.
	for _, n := range names {
		if err := deleteOne(ip, path+"/"+n); err != nil {
			fmt.Println("delete", n, ":", err)
			os.Exit(1)
		}
		fmt.Println("deleteprt sent:", n)
	}

	// 4. Что осталось через 3 с и ещё через 7 с?
	for _, wait := range []time.Duration{3 * time.Second, 7 * time.Second} {
		time.Sleep(wait)
		list, err := wsList(ip)
		if err != nil {
			fmt.Println("list:", err)
			os.Exit(1)
		}
		var left []string
		for _, n := range names {
			if _, ok := list[n]; ok {
				left = append(left, n)
			}
		}
		fmt.Printf("after +%.0fs: remaining = %v\n", wait.Seconds(), left)
		if len(left) == 0 {
			return
		}
	}

	// 5. Остаток чистим вручную (одним запросом, с паузой).
	list, _ = wsList(ip)
	for n := range list {
		for _, target := range names {
			if n == target {
				fmt.Println("cleanup:", n)
				_ = deleteOne(ip, path+"/"+n)
			}
		}
	}
	time.Sleep(3 * time.Second)
	list, _ = wsList(ip)
	for n := range list {
		for _, target := range names {
			if n == target {
				fmt.Println("STILL PRESENT after cleanup:", n)
			}
		}
	}
}

// upload — POST /upload/<name>, multipart-поле «file».
func upload(url, filename, body string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		return err
	}
	w.Close()
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	fmt.Println("  upload response:", string(rb))
	return nil
}

// wsList — WS reqGcodeFile → map[name]path.
func wsList(ip string) (map[string]string, error) {
	conn, resp, err := websocket.DefaultDialer.Dial("ws://"+ip+":9999", nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial: %v (http %d)", err, resp.StatusCode)
		}
		return nil, err
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"method":"get","params":{"reqGcodeFile":1}}`)); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		var payload struct {
			Ret *struct {
				TotalNum int    `json:"totalNum"`
				FileInfo string `json:"fileInfo"`
			} `json:"retGcodeFileInfo"`
		}
		if err := json.Unmarshal(data, &payload); err != nil || payload.Ret == nil {
			continue
		}
		out := map[string]string{}
		for _, rec := range strings.Split(payload.Ret.FileInfo, ";") {
			rec = strings.TrimSpace(rec)
			if rec == "" {
				continue
			}
			f := strings.Split(rec, ":")
			if len(f) >= 2 {
				out[f[1]] = f[0]
			}
		}
		return out, nil
	}
}

// wipe — удалить один файл <ip>/<name> и убедиться, что он исчез.
func wipe(ip, name string) {
	list, err := wsList(ip)
	if err != nil {
		fmt.Println("list:", err)
		os.Exit(1)
	}
	path, ok := list[name]
	if !ok {
		fmt.Println(name, "already absent")
		return
	}
	for round := 0; round < 3; round++ {
		if err := deleteOne(ip, path+"/"+name); err != nil {
			fmt.Println("delete:", err)
			os.Exit(1)
		}
		fmt.Printf("round %d: deleteprt sent, waiting 2s...\n", round+1)
		time.Sleep(2 * time.Second)
		list, _ = wsList(ip)
		if _, still := list[name]; !still {
			fmt.Println(name, "deleted OK")
			return
		}
	}
	fmt.Println(name, "STILL PRESENT after 3 rounds")
	os.Exit(1)
}

// deleteOne — dial → send deleteprt → close (как printer.Client.DeleteFile).
func deleteOne(ip, full string) error {
	conn, resp, err := websocket.DefaultDialer.Dial("ws://"+ip+":9999", nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial: %v (http %d)", err, resp.StatusCode)
		}
		return err
	}
	msg, _ := json.Marshal(map[string]any{
		"method": "set",
		"params": map[string]any{"opGcodeFile": "deleteprt:" + full},
	})
	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		conn.Close()
		return err
	}
	return conn.Close()
}
