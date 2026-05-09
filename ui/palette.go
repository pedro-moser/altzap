package ui

import "image/color"

// Palette is the full set of colors a theme provides. Names mirror the
// Catppuccin scheme (its 26-color taxonomy is the most granular among
// supported themes, so other palettes map their values onto these slots).
//
// All fields are color.RGBA (concrete) rather than color.Color (interface)
// because some callsites build tints by reading .R/.G/.B (e.g. a 0x40
// alpha overlay over Mauve for selection highlight). Keeping the concrete
// type avoids type assertions at every site.
type Palette struct {
	// Accents
	Rosewater, Flamingo, Pink, Mauve, Red, Maroon             color.RGBA
	Peach, Yellow, Green, Teal, Sky, Sapphire, Blue, Lavender color.RGBA

	// Foreground / overlays (light → dark)
	Text, Subtext1, Subtext0     color.RGBA
	Overlay2, Overlay1, Overlay0 color.RGBA

	// Background / surfaces (dark → darker for dark themes; reverse for light)
	Surface2, Surface1, Surface0 color.RGBA
	Base, Mantle, Crust          color.RGBA
}

// Hex utility — `0xRRGGBB` literal → opaque color.RGBA.
func rgb(hex uint32) color.RGBA {
	return color.RGBA{
		R: uint8(hex >> 16),
		G: uint8(hex >> 8),
		B: uint8(hex),
		A: 0xff,
	}
}

// Catppuccin Mocha — https://github.com/catppuccin/catppuccin (dark, default).
var paletteCatppuccinMocha = Palette{
	Rosewater: rgb(0xf5e0dc), Flamingo: rgb(0xf2cdcd), Pink: rgb(0xf5c2e7),
	Mauve: rgb(0xcba6f7), Red: rgb(0xf38ba8), Maroon: rgb(0xeba0ac),
	Peach: rgb(0xfab387), Yellow: rgb(0xf9e2af), Green: rgb(0xa6e3a1),
	Teal: rgb(0x94e2d5), Sky: rgb(0x89dceb), Sapphire: rgb(0x74c7ec),
	Blue: rgb(0x89b4fa), Lavender: rgb(0xb4befe),
	Text: rgb(0xcdd6f4), Subtext1: rgb(0xbac2de), Subtext0: rgb(0xa6adc8),
	Overlay2: rgb(0x9399b2), Overlay1: rgb(0x7f849c), Overlay0: rgb(0x6c7086),
	Surface2: rgb(0x585b70), Surface1: rgb(0x45475a), Surface0: rgb(0x313244),
	Base: rgb(0x1e1e2e), Mantle: rgb(0x181825), Crust: rgb(0x11111b),
}

// Catppuccin Macchiato — slightly lighter dark.
var paletteCatppuccinMacchiato = Palette{
	Rosewater: rgb(0xf4dbd6), Flamingo: rgb(0xf0c6c6), Pink: rgb(0xf5bde6),
	Mauve: rgb(0xc6a0f6), Red: rgb(0xed8796), Maroon: rgb(0xee99a0),
	Peach: rgb(0xf5a97f), Yellow: rgb(0xeed49f), Green: rgb(0xa6da95),
	Teal: rgb(0x8bd5ca), Sky: rgb(0x91d7e3), Sapphire: rgb(0x7dc4e4),
	Blue: rgb(0x8aadf4), Lavender: rgb(0xb7bdf8),
	Text: rgb(0xcad3f5), Subtext1: rgb(0xb8c0e0), Subtext0: rgb(0xa5adcb),
	Overlay2: rgb(0x939ab7), Overlay1: rgb(0x8087a2), Overlay0: rgb(0x6e738d),
	Surface2: rgb(0x5b6078), Surface1: rgb(0x494d64), Surface0: rgb(0x363a4f),
	Base: rgb(0x24273a), Mantle: rgb(0x1e2030), Crust: rgb(0x181926),
}

// Catppuccin Frappé — dim dark.
var paletteCatppuccinFrappe = Palette{
	Rosewater: rgb(0xf2d5cf), Flamingo: rgb(0xeebebe), Pink: rgb(0xf4b8e4),
	Mauve: rgb(0xca9ee6), Red: rgb(0xe78284), Maroon: rgb(0xea999c),
	Peach: rgb(0xef9f76), Yellow: rgb(0xe5c890), Green: rgb(0xa6d189),
	Teal: rgb(0x81c8be), Sky: rgb(0x99d1db), Sapphire: rgb(0x85c1dc),
	Blue: rgb(0x8caaee), Lavender: rgb(0xbabbf1),
	Text: rgb(0xc6d0f5), Subtext1: rgb(0xb5bfe2), Subtext0: rgb(0xa5adce),
	Overlay2: rgb(0x949cbb), Overlay1: rgb(0x838ba7), Overlay0: rgb(0x737994),
	Surface2: rgb(0x626880), Surface1: rgb(0x51576d), Surface0: rgb(0x414559),
	Base: rgb(0x303446), Mantle: rgb(0x292c3c), Crust: rgb(0x232634),
}

// Catppuccin Latte — the only Catppuccin light variant.
var paletteCatppuccinLatte = Palette{
	Rosewater: rgb(0xdc8a78), Flamingo: rgb(0xdd7878), Pink: rgb(0xea76cb),
	Mauve: rgb(0x8839ef), Red: rgb(0xd20f39), Maroon: rgb(0xe64553),
	Peach: rgb(0xfe640b), Yellow: rgb(0xdf8e1d), Green: rgb(0x40a02b),
	Teal: rgb(0x179299), Sky: rgb(0x04a5e5), Sapphire: rgb(0x209fb5),
	Blue: rgb(0x1e66f5), Lavender: rgb(0x7287fd),
	Text: rgb(0x4c4f69), Subtext1: rgb(0x5c5f77), Subtext0: rgb(0x6c6f85),
	Overlay2: rgb(0x7c7f93), Overlay1: rgb(0x8c8fa1), Overlay0: rgb(0x9ca0b0),
	Surface2: rgb(0xacb0be), Surface1: rgb(0xbcc0cc), Surface0: rgb(0xccd0da),
	Base: rgb(0xeff1f5), Mantle: rgb(0xe6e9ef), Crust: rgb(0xdce0e8),
}

// Tokyo Night — the popular night-sky palette.
// Slot mapping: surfaces are storm-grey, base→bg/dark blues; accents map
// approximately by hue (mauve→purple, sapphire→cyan, etc.).
var paletteTokyoNight = Palette{
	Rosewater: rgb(0xf7768e), Flamingo: rgb(0xff9e64), Pink: rgb(0xbb9af7),
	Mauve: rgb(0xbb9af7), Red: rgb(0xf7768e), Maroon: rgb(0xdb4b4b),
	Peach: rgb(0xff9e64), Yellow: rgb(0xe0af68), Green: rgb(0x9ece6a),
	Teal: rgb(0x73daca), Sky: rgb(0x7dcfff), Sapphire: rgb(0x7aa2f7),
	Blue: rgb(0x7aa2f7), Lavender: rgb(0xbb9af7),
	Text: rgb(0xc0caf5), Subtext1: rgb(0xa9b1d6), Subtext0: rgb(0x9aa5ce),
	Overlay2: rgb(0x737aa2), Overlay1: rgb(0x565f89), Overlay0: rgb(0x414868),
	Surface2: rgb(0x3b4261), Surface1: rgb(0x2f3549), Surface0: rgb(0x24283b),
	Base: rgb(0x1a1b26), Mantle: rgb(0x16161e), Crust: rgb(0x101014),
}

// Tokyo Night Day — the light variant.
var paletteTokyoNightDay = Palette{
	Rosewater: rgb(0xf52a65), Flamingo: rgb(0xb15c00), Pink: rgb(0x9854f1),
	Mauve: rgb(0x9854f1), Red: rgb(0xf52a65), Maroon: rgb(0xc64343),
	Peach: rgb(0xb15c00), Yellow: rgb(0x8c6c3e), Green: rgb(0x587539),
	Teal: rgb(0x118c74), Sky: rgb(0x007197), Sapphire: rgb(0x2e7de9),
	Blue: rgb(0x2e7de9), Lavender: rgb(0x9854f1),
	Text: rgb(0x3760bf), Subtext1: rgb(0x4f5b96), Subtext0: rgb(0x6172b0),
	Overlay2: rgb(0x848cb5), Overlay1: rgb(0x9aa1c1), Overlay0: rgb(0xa8aecb),
	Surface2: rgb(0xb6bdd5), Surface1: rgb(0xc4c8da), Surface0: rgb(0xd6d8e0),
	Base: rgb(0xe1e2e7), Mantle: rgb(0xd5d6db), Crust: rgb(0xc8c9ce),
}

// Gruvbox Dark — warm earthy retro.
var paletteGruvboxDark = Palette{
	Rosewater: rgb(0xfb4934), Flamingo: rgb(0xfe8019), Pink: rgb(0xd3869b),
	Mauve: rgb(0xd3869b), Red: rgb(0xfb4934), Maroon: rgb(0xcc241d),
	Peach: rgb(0xfe8019), Yellow: rgb(0xfabd2f), Green: rgb(0xb8bb26),
	Teal: rgb(0x8ec07c), Sky: rgb(0x83a598), Sapphire: rgb(0x83a598),
	Blue: rgb(0x83a598), Lavender: rgb(0xd3869b),
	Text: rgb(0xebdbb2), Subtext1: rgb(0xd5c4a1), Subtext0: rgb(0xbdae93),
	Overlay2: rgb(0xa89984), Overlay1: rgb(0x928374), Overlay0: rgb(0x7c6f64),
	Surface2: rgb(0x665c54), Surface1: rgb(0x504945), Surface0: rgb(0x3c3836),
	Base: rgb(0x282828), Mantle: rgb(0x1d2021), Crust: rgb(0x171717),
}

// Gruvbox Light.
var paletteGruvboxLight = Palette{
	Rosewater: rgb(0x9d0006), Flamingo: rgb(0xaf3a03), Pink: rgb(0x8f3f71),
	Mauve: rgb(0x8f3f71), Red: rgb(0x9d0006), Maroon: rgb(0xcc241d),
	Peach: rgb(0xaf3a03), Yellow: rgb(0xb57614), Green: rgb(0x79740e),
	Teal: rgb(0x427b58), Sky: rgb(0x076678), Sapphire: rgb(0x076678),
	Blue: rgb(0x076678), Lavender: rgb(0x8f3f71),
	Text: rgb(0x3c3836), Subtext1: rgb(0x504945), Subtext0: rgb(0x665c54),
	Overlay2: rgb(0x7c6f64), Overlay1: rgb(0x928374), Overlay0: rgb(0xa89984),
	Surface2: rgb(0xbdae93), Surface1: rgb(0xd5c4a1), Surface0: rgb(0xebdbb2),
	Base: rgb(0xfbf1c7), Mantle: rgb(0xf2e5bc), Crust: rgb(0xe6d8a3),
}

// Nord — arctic-inspired cool palette.
var paletteNord = Palette{
	Rosewater: rgb(0xbf616a), Flamingo: rgb(0xd08770), Pink: rgb(0xb48ead),
	Mauve: rgb(0xb48ead), Red: rgb(0xbf616a), Maroon: rgb(0xbf616a),
	Peach: rgb(0xd08770), Yellow: rgb(0xebcb8b), Green: rgb(0xa3be8c),
	Teal: rgb(0x8fbcbb), Sky: rgb(0x88c0d0), Sapphire: rgb(0x81a1c1),
	Blue: rgb(0x5e81ac), Lavender: rgb(0xb48ead),
	Text: rgb(0xeceff4), Subtext1: rgb(0xe5e9f0), Subtext0: rgb(0xd8dee9),
	Overlay2: rgb(0x7b88a1), Overlay1: rgb(0x6c7a96), Overlay0: rgb(0x4c566a),
	Surface2: rgb(0x434c5e), Surface1: rgb(0x3b4252), Surface0: rgb(0x363c4a),
	Base: rgb(0x2e3440), Mantle: rgb(0x272b35), Crust: rgb(0x21252e),
}

// activePalette is the live source of truth; ctp* color vars in theme.go
// are refreshed from it via ApplyPalette.
var activePalette = paletteCatppuccinMocha

// paletteByName maps a user-friendly theme name (case-insensitive,
// "-" / "_" interchangeable) to its Palette. Returns ok=false on miss.
func paletteByName(name string) (Palette, bool) {
	switch normalizeThemeName(name) {
	case "catppuccin-mocha", "mocha":
		return paletteCatppuccinMocha, true
	case "catppuccin-macchiato", "macchiato":
		return paletteCatppuccinMacchiato, true
	case "catppuccin-frappe", "frappe":
		return paletteCatppuccinFrappe, true
	case "catppuccin-latte", "latte":
		return paletteCatppuccinLatte, true
	case "tokyo-night", "tokyonight":
		return paletteTokyoNight, true
	case "tokyo-night-day", "tokyonight-day", "tokyonightday":
		return paletteTokyoNightDay, true
	case "gruvbox-dark", "gruvbox":
		return paletteGruvboxDark, true
	case "gruvbox-light":
		return paletteGruvboxLight, true
	case "nord":
		return paletteNord, true
	}
	return Palette{}, false
}

// AvailableThemes returns the canonical names users can write into the
// theme config file. Order is presentation-friendly (dark variants before
// light, related families clustered).
func AvailableThemes() []string {
	return []string{
		"catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe",
		"catppuccin-latte",
		"tokyo-night", "tokyo-night-day",
		"gruvbox-dark", "gruvbox-light",
		"nord",
	}
}

func normalizeThemeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c == '_':
			out = append(out, '-')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
