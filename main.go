package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	winapi "golang.org/x/sys/windows"
)

//go:embed all:frontend
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	mutex, err := winapi.CreateMutex(nil, false, winapi.StringToUTF16Ptr(`Local\Tartarus-Go`))
	if err != nil && err != winapi.ERROR_ALREADY_EXISTS {
		log.Fatal(err)
	}
	if err == winapi.ERROR_ALREADY_EXISTS {
		if hwnd := findWindow(windowTitle); hwnd != 0 {
			activateWindow(hwnd)
		}
		winapi.CloseHandle(mutex)
		return
	}
	defer winapi.CloseHandle(mutex)

	app := NewApp()
	err = wails.Run(&options.App{
		Title:            windowTitle,
		Width:            1120,
		Height:           760,
		MinWidth:         280,
		MinHeight:        40,
		BackgroundColour: &options.RGBA{R: 16, G: 14, B: 18, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind:             []any{app},
		Windows: &windows.Options{
			Theme:                windows.Dark,
			DisablePinchZoom:     true,
			WebviewGpuIsDisabled: false,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
