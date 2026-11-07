//go:build !devserver

// Command jot-desktop is the jot desktop app: a window over the same
// Markdown memory store the jot CLI and MCP server use.
package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var version = "dev"

//go:embed all:frontend/dist
var assets embed.FS

// Window holds the Wails context for the calls that need a native dialog.
type Window struct {
	app *App
	ctx context.Context
}

// ChooseProject opens a folder picker and adds the chosen folder.
func (w *Window) ChooseProject() (string, error) {
	dir, err := wruntime.OpenDirectoryDialog(w.ctx, wruntime.OpenDialogOptions{Title: "Choose a project folder"})
	if err != nil || dir == "" {
		return "", err
	}
	return dir, w.app.AddProjectDir(dir)
}

// Confirm asks a yes/no question in a native dialog.
func (w *Window) Confirm(title, message string) bool {
	res, err := wruntime.MessageDialog(w.ctx, wruntime.MessageDialogOptions{
		Type: wruntime.QuestionDialog, Title: title, Message: message,
		Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No",
	})
	return err == nil && (res == "Yes" || res == "Ok")
}

// ApplyTheme matches the native title bar to the page: "light", "dark"
// or "system".
func (w *Window) ApplyTheme(theme string) {
	switch theme {
	case "dark":
		wruntime.WindowSetDarkTheme(w.ctx)
	case "light":
		wruntime.WindowSetLightTheme(w.ctx)
	default:
		wruntime.WindowSetSystemDefaultTheme(w.ctx)
	}
}

// Quit closes the app, so an update's installer can replace it.
func (w *Window) Quit() { wruntime.Quit(w.ctx) }

func main() {
	app := NewApp(prefsPath())
	win := &Window{app: app}
	paper := &options.RGBA{R: 0xFB, G: 0xFA, B: 0xF7, A: 0xFF}
	theme := windows.SystemDefault
	switch app.prefs.Theme {
	case "dark":
		paper, theme = &options.RGBA{R: 0x1A, G: 0x19, B: 0x17, A: 0xFF}, windows.Dark
	case "light":
		theme = windows.Light
	}
	err := wails.Run(&options.App{
		Title:            "jot",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        560,
		BackgroundColour: paper,
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        func(ctx context.Context) { win.ctx = ctx },
		Bind:             []interface{}{app, win},
		Windows: &windows.Options{
			Theme:                theme,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			CustomTheme: &windows.ThemeSettings{
				LightModeTitleBar:          windows.RGB(0xEE, 0xEB, 0xE5),
				LightModeTitleText:         windows.RGB(0x1D, 0x1C, 0x1A),
				LightModeBorder:            windows.RGB(0xD5, 0xD0, 0xC6),
				LightModeTitleBarInactive:  windows.RGB(0xF2, 0xF0, 0xEB),
				LightModeTitleTextInactive: windows.RGB(0x8C, 0x87, 0x7D),
				LightModeBorderInactive:    windows.RGB(0xE5, 0xE1, 0xD9),
				DarkModeTitleBar:           windows.RGB(0x12, 0x11, 0x10),
				DarkModeTitleText:          windows.RGB(0xEC, 0xE8, 0xE1),
				DarkModeBorder:             windows.RGB(0x2A, 0x28, 0x25),
				DarkModeTitleBarInactive:   windows.RGB(0x15, 0x14, 0x12),
				DarkModeTitleTextInactive:  windows.RGB(0x85, 0x7F, 0x75),
				DarkModeBorderInactive:     windows.RGB(0x22, 0x21, 0x1E),
			},
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About:    &mac.AboutInfo{Title: "jot", Message: "Shared memory for your AI tools."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
