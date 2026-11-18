package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"ender-space/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Сервис приложения: подключение к принтеру, память, файлы, удаление.
	svc := app.NewApp(configDir())

	wapp := application.New(application.Options{
		Name:        "ender-space",
		Description: "EnderSpace — менеджер внутренней памяти Creality Ender-3 V3 KE",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "EnderSpace",
		Width:  1000,
		Height: 618,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	// Run the application. This blocks until the application has been exited.
	if err := wapp.Run(); err != nil {
		log.Fatal(err)
	}
}

// configDir — каталог config.json: %AppData%/EnderSpace (Windows).
func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "EnderSpace")
}
