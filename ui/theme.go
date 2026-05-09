package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Per-color package vars — populated by ApplyPalette(activePalette) in
// init(). All over the UI code references these directly (74 callsites at
// time of writing); keeping them as package-level vars rather than
// function calls means a theme switch only needs ApplyPalette + a
// widget.List Refresh to roll new colors into freshly-built bubbles.
var (
	ctpRosewater color.RGBA
	ctpFlamingo  color.RGBA
	ctpPink      color.RGBA
	ctpMauve     color.RGBA
	ctpRed       color.RGBA
	ctpMaroon    color.RGBA
	ctpPeach     color.RGBA
	ctpYellow    color.RGBA
	ctpGreen     color.RGBA
	ctpTeal      color.RGBA
	ctpSky       color.RGBA
	ctpSapphire  color.RGBA
	ctpBlue      color.RGBA
	ctpLavender  color.RGBA

	ctpText     color.RGBA
	ctpSubtext1 color.RGBA
	ctpSubtext0 color.RGBA
	ctpOverlay2 color.RGBA
	ctpOverlay1 color.RGBA
	ctpOverlay0 color.RGBA
	ctpSurface2 color.RGBA
	ctpSurface1 color.RGBA
	ctpSurface0 color.RGBA
	ctpBase     color.RGBA
	ctpMantle   color.RGBA
	ctpCrust    color.RGBA
)

func init() {
	ApplyPalette(activePalette)
}

// ApplyPalette swaps the active palette into the package-level color vars
// so subsequent widget construction reads the new palette. Returns the
// palette so callers can chain (mostly for tests).
//
// Note: existing canvas.Rectangle.FillColor (and similar) keep their
// captured-at-construction values until the owning widget is rebuilt.
// For chat content, that happens naturally via widget.List.Refresh
// triggering UpdateItem; static chrome (chat header, separators) stays
// stale until the next interaction or a window-content rebuild.
func ApplyPalette(p Palette) Palette {
	activePalette = p
	ctpRosewater = p.Rosewater
	ctpFlamingo = p.Flamingo
	ctpPink = p.Pink
	ctpMauve = p.Mauve
	ctpRed = p.Red
	ctpMaroon = p.Maroon
	ctpPeach = p.Peach
	ctpYellow = p.Yellow
	ctpGreen = p.Green
	ctpTeal = p.Teal
	ctpSky = p.Sky
	ctpSapphire = p.Sapphire
	ctpBlue = p.Blue
	ctpLavender = p.Lavender
	ctpText = p.Text
	ctpSubtext1 = p.Subtext1
	ctpSubtext0 = p.Subtext0
	ctpOverlay2 = p.Overlay2
	ctpOverlay1 = p.Overlay1
	ctpOverlay0 = p.Overlay0
	ctpSurface2 = p.Surface2
	ctpSurface1 = p.Surface1
	ctpSurface0 = p.Surface0
	ctpBase = p.Base
	ctpMantle = p.Mantle
	ctpCrust = p.Crust
	applySemanticColors()
	return p
}

type paletteTheme struct{}

// CatppuccinTheme returns a fyne.Theme that delegates color/size lookups
// to the active palette. Kept under this name for source compatibility —
// the same symbol now powers Tokyo Night, Gruvbox, Nord, and any palette
// you set via ApplyPalette.
func CatppuccinTheme() fyne.Theme {
	return paletteTheme{}
}

func (paletteTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return ctpBase
	case theme.ColorNameForeground:
		return ctpText
	case theme.ColorNameForegroundOnPrimary:
		return ctpCrust
	case theme.ColorNameForegroundOnError:
		return ctpCrust
	case theme.ColorNameForegroundOnSuccess:
		return ctpCrust
	case theme.ColorNameForegroundOnWarning:
		return ctpCrust
	case theme.ColorNamePrimary:
		return ctpMauve
	case theme.ColorNameHyperlink:
		return ctpSapphire
	case theme.ColorNameButton:
		return ctpSurface1
	case theme.ColorNameDisabledButton:
		return ctpSurface0
	case theme.ColorNameDisabled:
		return ctpOverlay0
	case theme.ColorNameError:
		return ctpRed
	case theme.ColorNameSuccess:
		return ctpGreen
	case theme.ColorNameWarning:
		return ctpYellow
	case theme.ColorNameFocus:
		// Keyboard-focus ring on inputs/buttons — kept for accessibility.
		return color.RGBA{R: ctpMauve.R, G: ctpMauve.G, B: ctpMauve.B, A: 0x80}
	case theme.ColorNameSelection:
		// Fyne paints this on top of every list row's selection AND text-entry
		// selections. The chat list owns its own selection visual via
		// `selectionTint` (see chat_view.go), so a second mauve overlay just
		// doubles up and looks muddy. Transparent here means Fyne adds no
		// overlay; chat list shows our solid tint, message-list rows have no
		// "selected" highlight at all (intended — messages aren't selectable).
		// Text-entry selection loses its highlight too — minor regression
		// accepted for a cleaner list look.
		return color.RGBA{A: 0}
	case theme.ColorNameHover:
		// Hover overlay applied on every list row + button. Looked grungy on
		// the message bubbles (Surface2 wash over Surface1/Surface2 already)
		// — transparent kills it everywhere.
		return color.RGBA{A: 0}
	case theme.ColorNamePressed:
		return ctpSurface2
	case theme.ColorNameHeaderBackground:
		return ctpMantle
	case theme.ColorNameInputBackground:
		return ctpSurface0
	case theme.ColorNameInputBorder:
		return ctpSurface2
	case theme.ColorNameMenuBackground:
		return ctpMantle
	case theme.ColorNameOverlayBackground:
		return ctpCrust
	case theme.ColorNamePlaceHolder:
		return ctpOverlay0
	case theme.ColorNameScrollBar:
		return ctpSurface2
	case theme.ColorNameScrollBarBackground:
		return ctpMantle
	case theme.ColorNameSeparator:
		return ctpSurface1
	case theme.ColorNameShadow:
		return color.RGBA{A: 0x80}
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (paletteTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (paletteTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (paletteTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 18
	case theme.SizeNameCaptionText:
		return 14
	case theme.SizeNameSubHeadingText:
		return 22
	case theme.SizeNameHeadingText:
		return 28
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNamePadding:
		return 7
	}
	return theme.DefaultTheme().Size(n)
}
