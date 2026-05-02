//go:build windows

package settings

import (
	"runtime"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"github.com/zelgray/sing-box-tray/internal/config"
)

var (
	mu sync.Mutex
	mw *walk.MainWindow
)

// Show opens the settings window, or brings it to front if already open.
// onSave is called with the updated config when the user clicks Save.
func Show(cfg *config.TrayConfig, onSave func(*config.TrayConfig)) {
	mu.Lock()
	existing := mw
	mu.Unlock()

	if existing != nil {
		existing.Synchronize(func() {
			win.ShowWindow(existing.Handle(), win.SW_RESTORE)
			win.SetForegroundWindow(existing.Handle())
		})
		return
	}

	go runWindow(cfg, onSave)
}

func runWindow(cfg *config.TrayConfig, onSave func(*config.TrayConfig)) {
	runtime.LockOSThread()

	var w *walk.MainWindow
	var singBoxEdit, wintunEdit, configEdit *walk.LineEdit

	browseFn := func(edit **walk.LineEdit, filter string) func() {
		return func() {
			dlg := &walk.FileDialog{
				Filter:   filter,
				FilePath: (*edit).Text(),
			}
			if ok, err := dlg.ShowOpen(w); err == nil && ok {
				_ = (*edit).SetText(dlg.FilePath)
			}
		}
	}

	if err := (MainWindow{
		AssignTo: &w,
		Title:    "sing-box-tray — Settings",
		MinSize:  Size{Width: 550, Height: 185},
		MaxSize:  Size{Width: 900, Height: 185},
		Layout:   Grid{Columns: 3, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
		Children: []Widget{
			Label{Text: "sing-box path:"},
			LineEdit{AssignTo: &singBoxEdit, Text: cfg.SingBoxPath, MinSize: Size{Width: 350}},
			PushButton{Text: "Browse...", OnClicked: browseFn(&singBoxEdit, "Executables (*.exe)|*.exe|All files (*.*)|*.*")},

			Label{Text: "wintun.dll path:"},
			LineEdit{AssignTo: &wintunEdit, Text: cfg.WintunDllPath},
			PushButton{Text: "Browse...", OnClicked: browseFn(&wintunEdit, "DLL files (*.dll)|*.dll|All files (*.*)|*.*")},

			Label{Text: "config.json path:"},
			LineEdit{AssignTo: &configEdit, Text: cfg.ConfigPath},
			PushButton{Text: "Browse...", OnClicked: browseFn(&configEdit, "JSON files (*.json)|*.json|All files (*.*)|*.*")},

			HSpacer{ColumnSpan: 2},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "Save", OnClicked: func() {
						cfg.SingBoxPath = singBoxEdit.Text()
						cfg.WintunDllPath = wintunEdit.Text()
						cfg.ConfigPath = configEdit.Text()
						onSave(cfg)
						w.Close()
					}},
					PushButton{Text: "Cancel", OnClicked: func() { w.Close() }},
				},
			},
		},
	}).Create(); err != nil {
		return
	}

	mu.Lock()
	mw = w
	mu.Unlock()

	w.Show()
	win.SetForegroundWindow(w.Handle())
	w.Run()

	mu.Lock()
	mw = nil
	mu.Unlock()
}
