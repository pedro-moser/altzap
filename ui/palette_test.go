package ui

import (
	"testing"
)

// TestPaletteByNameResolvesAllAvailable guards against drift between the
// AvailableThemes() list (what users see / write to the config file) and
// paletteByName() (what the watcher dispatches on).
func TestPaletteByNameResolvesAllAvailable(t *testing.T) {
	for _, name := range AvailableThemes() {
		t.Run(name, func(t *testing.T) {
			if _, ok := paletteByName(name); !ok {
				t.Fatalf("AvailableThemes lists %q but paletteByName rejects it", name)
			}
		})
	}
}

// TestPaletteByNameAcceptsAliases — common alternate spellings rofi
// scripts or muscle memory might write.
func TestPaletteByNameAcceptsAliases(t *testing.T) {
	cases := []struct {
		input string
		want  string // expected canonical (Base color identifies the palette)
	}{
		{"mocha", "catppuccin-mocha"},
		{"MOCHA", "catppuccin-mocha"},
		{"Catppuccin_Mocha", "catppuccin-mocha"},
		{"tokyonight", "tokyo-night"},
		{"gruvbox", "gruvbox-dark"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := paletteByName(tc.input)
			if !ok {
				t.Fatalf("paletteByName(%q) returned !ok", tc.input)
			}
			want, _ := paletteByName(tc.want)
			if got.Base != want.Base {
				t.Fatalf("paletteByName(%q).Base = %v; want palette(%q).Base = %v",
					tc.input, got.Base, tc.want, want.Base)
			}
		})
	}
}

// TestApplyPaletteSwapsAllSlots ensures every color slot is rolled when
// the palette switches — a regression here would show as a "stuck" color
// on theme change (one of the 26 vars not getting reassigned).
func TestApplyPaletteSwapsAllSlots(t *testing.T) {
	t.Cleanup(func() { ApplyPalette(paletteCatppuccinMocha) })

	ApplyPalette(paletteCatppuccinLatte)

	// Spot-check a handful that should clearly differ between the dark
	// Mocha and the light Latte (full diff is too tedious to assert).
	if ctpBase != paletteCatppuccinLatte.Base {
		t.Errorf("ctpBase = %v; want %v", ctpBase, paletteCatppuccinLatte.Base)
	}
	if ctpText != paletteCatppuccinLatte.Text {
		t.Errorf("ctpText = %v; want %v", ctpText, paletteCatppuccinLatte.Text)
	}
	if ctpMauve != paletteCatppuccinLatte.Mauve {
		t.Errorf("ctpMauve = %v; want %v", ctpMauve, paletteCatppuccinLatte.Mauve)
	}
	if ctpCrust != paletteCatppuccinLatte.Crust {
		t.Errorf("ctpCrust = %v; want %v", ctpCrust, paletteCatppuccinLatte.Crust)
	}

	if ctpBase == paletteCatppuccinMocha.Base {
		t.Errorf("ctpBase still equals Mocha after ApplyPalette(Latte)")
	}
}
