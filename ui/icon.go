package ui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon.png
var appIconBytes []byte

// AppIcon is the embedded AltZap glyph (256×256 PNG, transparent background).
// Reuse it for the tray icon, login logo, and empty-chat placeholder so the
// brand is consistent across surfaces.
var AppIcon fyne.Resource = fyne.NewStaticResource("altzap.png", appIconBytes)
