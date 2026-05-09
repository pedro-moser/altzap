package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// audioDevice is a Pulse/PipeWire source or sink discovered via `pactl`.
// Name is the underlying device id (what we pass to ffmpeg/ffplay);
// Description is the user-friendly name (what we show in dropdowns).
type audioDevice struct {
	Name        string
	Description string
}

// pulseAvailable caches whether pactl exists in PATH; checked once per
// session.
var (
	pulseAvailableOnce sync.Once
	pulseAvailable     bool
)

func hasPactl() bool {
	pulseAvailableOnce.Do(func() {
		_, err := exec.LookPath("pactl")
		pulseAvailable = err == nil
	})
	return pulseAvailable
}

// listAudioDevices runs `pactl list <kind>` and parses Name/Description
// pairs. kind is "sources" (input/microphones) or "sinks" (output).
// Returns nil if pactl isn't available or the call fails — settings page
// then shows just a "Default" entry. Monitor sources (ones whose name
// contains ".monitor") are skipped for sources since they capture
// speaker output, not real microphones.
func listAudioDevices(kind string) []audioDevice {
	if !hasPactl() {
		return nil
	}
	out, err := exec.Command("pactl", "list", kind).Output()
	if err != nil {
		return nil
	}

	var devs []audioDevice
	var name, desc string
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Name: "):
			// Flush previous record (if complete) before starting a new one.
			if name != "" {
				if !(kind == "sources" && strings.Contains(name, ".monitor")) {
					devs = append(devs, audioDevice{Name: name, Description: orFallback(desc, name)})
				}
			}
			name = strings.TrimPrefix(t, "Name: ")
			desc = ""
		case strings.HasPrefix(t, "Description: "):
			desc = strings.TrimPrefix(t, "Description: ")
		}
	}
	// Trailing record.
	if name != "" {
		if !(kind == "sources" && strings.Contains(name, ".monitor")) {
			devs = append(devs, audioDevice{Name: name, Description: orFallback(desc, name)})
		}
	}
	return devs
}

func orFallback(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// AudioConfig is the persisted user-chosen device names. Empty = "use
// PulseAudio default", which is what ffmpeg/ffplay see if PULSE_SOURCE/
// PULSE_SINK env vars aren't set.
type AudioConfig struct {
	InputName  string `json:"input,omitempty"`
	OutputName string `json:"output,omitempty"`
}

var (
	audioCfgMu   sync.RWMutex
	audioCfg     AudioConfig
	audioCfgRead bool
)

// AudioConfigPath returns where the audio device config lives:
// $XDG_CONFIG_HOME/altzap/audio.json. Empty string if the user's config
// dir can't be determined.
func AudioConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "altzap", "audio.json")
}

// LoadAudioConfig reads the persisted device choices. Returns an empty
// (= defaults) config on first run or read error.
func LoadAudioConfig() AudioConfig {
	audioCfgMu.Lock()
	defer audioCfgMu.Unlock()
	if audioCfgRead {
		return audioCfg
	}
	audioCfgRead = true

	path := AudioConfigPath()
	if path == "" {
		return audioCfg
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return audioCfg
	}
	_ = json.Unmarshal(b, &audioCfg)
	return audioCfg
}

// SaveAudioConfig persists the supplied config and updates the in-memory
// cache. Used by the settings dialog.
func SaveAudioConfig(c AudioConfig) error {
	audioCfgMu.Lock()
	audioCfg = c
	audioCfgRead = true
	audioCfgMu.Unlock()

	path := AudioConfigPath()
	if path == "" {
		return errors.New("config dir unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// AudioInputDevice / AudioOutputDevice return the configured device name
// (or "default" if none chosen). Used by the recorder and audio player.
func AudioInputDevice() string {
	cfg := LoadAudioConfig()
	if cfg.InputName == "" {
		return "default"
	}
	return cfg.InputName
}

func AudioOutputDevice() string {
	cfg := LoadAudioConfig()
	return cfg.OutputName // "" → ffplay/PULSE_SINK use system default
}

// showSettingsDialog opens the settings dialog. Sections: theme, audio
// devices, profile (stub). See chat_header.go for invocation.
func (cv *ChatView) showSettingsDialog() {
	// --- Theme picker ---
	themes := AvailableThemes()
	currentTheme := readThemeFile(ThemeConfigPath())
	if currentTheme == "" {
		currentTheme = themes[0]
	}
	themeSelect := widget.NewSelect(themes, func(name string) {
		if path := ThemeConfigPath(); path != "" {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, []byte(name+"\n"), 0o644)
		}
		// File watcher in main.go applies + refreshes; no direct call here.
	})
	themeSelect.Selected = currentTheme

	// --- Audio devices ---
	cfg := LoadAudioConfig()
	inputDevs := listAudioDevices("sources")
	outputDevs := listAudioDevices("sinks")

	inputOpts := []string{"Default"}
	for _, d := range inputDevs {
		inputOpts = append(inputOpts, d.Description)
	}
	outputOpts := []string{"Default"}
	for _, d := range outputDevs {
		outputOpts = append(outputOpts, d.Description)
	}

	inputSel := widget.NewSelect(inputOpts, nil)
	outputSel := widget.NewSelect(outputOpts, nil)

	// Match current config to dropdown entries by Name.
	inputSel.Selected = "Default"
	for _, d := range inputDevs {
		if d.Name == cfg.InputName {
			inputSel.Selected = d.Description
			break
		}
	}
	outputSel.Selected = "Default"
	for _, d := range outputDevs {
		if d.Name == cfg.OutputName {
			outputSel.Selected = d.Description
			break
		}
	}

	if !hasPactl() {
		inputSel.Disable()
		outputSel.Disable()
	}

	// --- Profile photo (stub) ---
	profileNote := widget.NewLabel("Coming soon — needs a whatsmeow API hookup.")
	profileNote.Importance = widget.LowImportance

	// --- Layout ---
	form := container.NewVBox(
		sectionLabel("Theme"),
		themeSelect,
		widget.NewSeparator(),

		sectionLabel("Audio input (microphone)"),
		inputSel,

		sectionLabel("Audio output (speakers)"),
		outputSel,

		widget.NewSeparator(),
		sectionLabel("Profile photo"),
		profileNote,
	)

	d := dialog.NewCustomConfirm(
		"Settings", "Save", "Cancel",
		container.NewPadded(form),
		func(save bool) {
			if !save {
				return
			}
			// Theme already applied by the select onChange — only audio needs
			// an explicit save.
			newCfg := AudioConfig{
				InputName:  matchDeviceName(inputSel.Selected, inputDevs),
				OutputName: matchDeviceName(outputSel.Selected, outputDevs),
			}
			if err := SaveAudioConfig(newCfg); err != nil {
				dialog.ShowError(fmt.Errorf("save audio config: %w", err), cv.window)
			}
		},
		cv.window,
	)
	d.Resize(fyne.NewSize(440, 520))
	d.Show()
}

// matchDeviceName returns the Name of the device whose Description matches
// label, or "" for "Default" (and any unmatched value).
func matchDeviceName(label string, devs []audioDevice) string {
	if label == "Default" || label == "" {
		return ""
	}
	for _, d := range devs {
		if d.Description == label {
			return d.Name
		}
	}
	return ""
}

func sectionLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle.Bold = true
	return l
}
