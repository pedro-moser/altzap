package ui

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"github.com/fsnotify/fsnotify"
)

// ThemeConfigPath returns the canonical path of the theme config file:
// $XDG_CONFIG_HOME/altzap/theme, falling back to ~/.config/altzap/theme.
// Empty string if the user's config dir can't be determined.
func ThemeConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "altzap", "theme")
}

// WatchThemeFile applies the theme named in the config file at startup,
// then watches for changes (rofi-driven, KDE applet, manual edits — any
// process that writes the file). Each change triggers ApplyPalette plus
// the supplied onApplied hook, which is the caller's chance to refresh
// any widgets that captured colors by value (chat list, message list).
//
// Designed to be cheap to ignore: returns silently if the config dir
// doesn't exist or fsnotify fails, since theme switching is a "nice to
// have" and shouldn't block app startup.
func WatchThemeFile(fApp fyne.App, onApplied func(name string)) {
	path := ThemeConfigPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("theme watcher: mkdir %s: %v", filepath.Dir(path), err)
		return
	}

	apply := func(reason string) {
		name := readThemeFile(path)
		if name == "" {
			return
		}
		pal, ok := paletteByName(name)
		if !ok {
			log.Printf("theme: unknown name %q (available: %s) [%s]",
				name, strings.Join(AvailableThemes(), ", "), reason)
			return
		}
		ApplyPalette(pal)
		fApp.Settings().SetTheme(CatppuccinTheme())
		log.Printf("theme: applied %s [%s]", name, reason)
		if onApplied != nil {
			onApplied(name)
		}
	}

	apply("startup")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("theme watcher: new: %v", err)
		return
	}
	// Watch the directory rather than the file so we still get events
	// when the file is replaced atomically (write-temp + rename, the
	// pattern most editors and well-behaved scripts use).
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		log.Printf("theme watcher: add %s: %v", filepath.Dir(path), err)
		_ = watcher.Close()
		return
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != filepath.Base(path) {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				fyne.Do(func() { apply("file-event") })
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("theme watcher: %v", err)
			}
		}
	}()
}

func readThemeFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
