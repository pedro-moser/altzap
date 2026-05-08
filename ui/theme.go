package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Catppuccin Mocha palette — https://github.com/catppuccin/catppuccin
var (
	ctpRosewater = color.RGBA{R: 0xf5, G: 0xe0, B: 0xdc, A: 0xff}
	ctpFlamingo  = color.RGBA{R: 0xf2, G: 0xcd, B: 0xcd, A: 0xff}
	ctpPink      = color.RGBA{R: 0xf5, G: 0xc2, B: 0xe7, A: 0xff}
	ctpMauve     = color.RGBA{R: 0xcb, G: 0xa6, B: 0xf7, A: 0xff}
	ctpRed       = color.RGBA{R: 0xf3, G: 0x8b, B: 0xa8, A: 0xff}
	ctpMaroon    = color.RGBA{R: 0xeb, G: 0xa0, B: 0xac, A: 0xff}
	ctpPeach     = color.RGBA{R: 0xfa, G: 0xb3, B: 0x87, A: 0xff}
	ctpYellow    = color.RGBA{R: 0xf9, G: 0xe2, B: 0xaf, A: 0xff}
	ctpGreen     = color.RGBA{R: 0xa6, G: 0xe3, B: 0xa1, A: 0xff}
	ctpTeal      = color.RGBA{R: 0x94, G: 0xe2, B: 0xd5, A: 0xff}
	ctpSky       = color.RGBA{R: 0x89, G: 0xdc, B: 0xeb, A: 0xff}
	ctpSapphire  = color.RGBA{R: 0x74, G: 0xc7, B: 0xec, A: 0xff}
	ctpBlue      = color.RGBA{R: 0x89, G: 0xb4, B: 0xfa, A: 0xff}
	ctpLavender  = color.RGBA{R: 0xb4, G: 0xbe, B: 0xfe, A: 0xff}

	ctpText     = color.RGBA{R: 0xcd, G: 0xd6, B: 0xf4, A: 0xff}
	ctpSubtext1 = color.RGBA{R: 0xba, G: 0xc2, B: 0xde, A: 0xff}
	ctpSubtext0 = color.RGBA{R: 0xa6, G: 0xad, B: 0xc8, A: 0xff}
	ctpOverlay2 = color.RGBA{R: 0x93, G: 0x99, B: 0xb2, A: 0xff}
	ctpOverlay1 = color.RGBA{R: 0x7f, G: 0x84, B: 0x9c, A: 0xff}
	ctpOverlay0 = color.RGBA{R: 0x6c, G: 0x70, B: 0x86, A: 0xff}
	ctpSurface2 = color.RGBA{R: 0x58, G: 0x5b, B: 0x70, A: 0xff}
	ctpSurface1 = color.RGBA{R: 0x45, G: 0x47, B: 0x5a, A: 0xff}
	ctpSurface0 = color.RGBA{R: 0x31, G: 0x32, B: 0x44, A: 0xff}
	ctpBase     = color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}
	ctpMantle   = color.RGBA{R: 0x18, G: 0x18, B: 0x25, A: 0xff}
	ctpCrust    = color.RGBA{R: 0x11, G: 0x11, B: 0x1b, A: 0xff}
)

type catppuccinTheme struct{}

// CatppuccinTheme returns a fyne.Theme implementing Catppuccin Mocha.
func CatppuccinTheme() fyne.Theme {
	return catppuccinTheme{}
}

func (catppuccinTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
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
		return color.RGBA{R: ctpMauve.R, G: ctpMauve.G, B: ctpMauve.B, A: 0x80}
	case theme.ColorNameSelection:
		return color.RGBA{R: ctpMauve.R, G: ctpMauve.G, B: ctpMauve.B, A: 0x60}
	case theme.ColorNameHover:
		return color.RGBA{R: ctpSurface2.R, G: ctpSurface2.G, B: ctpSurface2.B, A: 0x80}
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

func (catppuccinTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (catppuccinTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (catppuccinTheme) Size(n fyne.ThemeSizeName) float32 {
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
