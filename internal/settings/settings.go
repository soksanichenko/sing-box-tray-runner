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

// langLabels/langCodes are parallel: the ComboBox shows each language in its
// own script (not translated via Strings — this is the control that picks
// the language), langCodes[i] is the tray-config.json value to store.
var (
	langLabels = []string{"Auto", "English", "Русский", "Українська"}
	langCodes  = []string{"auto", "en", "ru", "ua"}
)

func langIndex(code string) int {
	for i, c := range langCodes {
		if c == code {
			return i
		}
	}
	return 0 // "auto"
}

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
	var launcherAutoCheck, singBoxAutoCheck, prereleaseCheck *walk.CheckBox
	var langCombo *walk.ComboBox

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
		MinSize:  Size{Width: 550, Height: 310},
		MaxSize:  Size{Width: 900, Height: 310},
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

			CheckBox{AssignTo: &launcherAutoCheck, Text: strs.SettingsAutoUpdateLauncher, ColumnSpan: 3, Checked: cfg.LauncherUpdate.AutoUpdate},
			CheckBox{AssignTo: &singBoxAutoCheck, Text: strs.SettingsAutoUpdateSingBox, ColumnSpan: 3, Checked: cfg.Update.AutoUpdate},
			CheckBox{AssignTo: &prereleaseCheck, Text: strs.UsePrereleaseLabel, ColumnSpan: 3, Checked: cfg.Update.Channel == "alpha"},

			Label{Text: strs.SettingsLanguageLabel},
			ComboBox{AssignTo: &langCombo, Model: langLabels, CurrentIndex: langIndex(cfg.Language), ColumnSpan: 2},

			HSpacer{ColumnSpan: 2},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: strs.SettingsSave, OnClicked: func() {
						cfg.SingBoxPath = singBoxEdit.Text()
						cfg.WintunDllPath = wintunEdit.Text()
						cfg.ConfigPath = configEdit.Text()
						cfg.LauncherUpdate.AutoUpdate = launcherAutoCheck.Checked()
						cfg.Update.AutoUpdate = singBoxAutoCheck.Checked()
						if prereleaseCheck.Checked() {
							cfg.Update.Channel = "alpha"
						} else {
							cfg.Update.Channel = "stable"
						}
						cfg.Language = langCodes[langCombo.CurrentIndex()]
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
