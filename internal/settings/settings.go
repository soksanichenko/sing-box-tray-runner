//go:build windows

package settings

import (
	"runtime"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"github.com/zelgray/sing-box-tray/internal/config"
	"github.com/zelgray/sing-box-tray/internal/i18n"
)

var (
	mu sync.Mutex
	mw *walk.MainWindow
)

// Show opens the settings window, or brings it to front if already open.
// onSave is called with the updated config when the user clicks Save.
func Show(cfg *config.TrayConfig, strs i18n.Strings, onSave func(*config.TrayConfig)) {
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

	go runWindow(cfg, strs, onSave)
}

func runWindow(cfg *config.TrayConfig, strs i18n.Strings, onSave func(*config.TrayConfig)) {
	runtime.LockOSThread()

	var w *walk.MainWindow
	var singBoxEdit, wintunEdit, configEdit *walk.LineEdit
	var alphaCheck *walk.CheckBox

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
		Title:    strs.SettingsTitle,
		MinSize:  Size{Width: 550, Height: 215},
		MaxSize:  Size{Width: 900, Height: 215},
		Layout:   Grid{Columns: 3, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
		Children: []Widget{
			Label{Text: strs.SettingsSingBoxPath},
			LineEdit{AssignTo: &singBoxEdit, Text: cfg.SingBoxPath, MinSize: Size{Width: 350}},
			PushButton{Text: strs.SettingsBrowse, OnClicked: browseFn(&singBoxEdit, "Executables (*.exe)|*.exe|All files (*.*)|*.*")},

			Label{Text: strs.SettingsWintunPath},
			LineEdit{AssignTo: &wintunEdit, Text: cfg.WintunDllPath},
			PushButton{Text: strs.SettingsBrowse, OnClicked: browseFn(&wintunEdit, "DLL files (*.dll)|*.dll|All files (*.*)|*.*")},

			Label{Text: strs.SettingsConfigPath},
			LineEdit{AssignTo: &configEdit, Text: cfg.ConfigPath},
			PushButton{Text: strs.SettingsBrowse, OnClicked: browseFn(&configEdit, "JSON files (*.json)|*.json|All files (*.*)|*.*")},

			CheckBox{AssignTo: &alphaCheck, Text: strs.SettingsAlphaChannel, ColumnSpan: 3, Checked: cfg.Update.Channel == "alpha"},

			HSpacer{ColumnSpan: 2},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: strs.SettingsSave, OnClicked: func() {
						cfg.SingBoxPath = singBoxEdit.Text()
						cfg.WintunDllPath = wintunEdit.Text()
						cfg.ConfigPath = configEdit.Text()
						if alphaCheck.Checked() {
							cfg.Update.Channel = "alpha"
						} else {
							cfg.Update.Channel = "stable"
						}
						onSave(cfg)
						w.Close()
					}},
					PushButton{Text: strs.SettingsCancel, OnClicked: func() { w.Close() }},
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
