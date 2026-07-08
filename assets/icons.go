package assets

import _ "embed"

// Tray icons in Windows ICO format (16/32/48/256 px), one per proxy state.
// systray on Windows writes icon bytes to a temp file and loads it with
// LoadImage(LR_LOADFROMFILE), which requires the ICO container format.
var (
	//go:embed icons/idling.ico
	IconGrey []byte

	//go:embed icons/working.ico
	IconGreen []byte

	//go:embed icons/error.ico
	IconRed []byte
)
