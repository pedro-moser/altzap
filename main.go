package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"altzap/client"
	"altzap/ui"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func main() {
	// Persistent log: stdout/stderr go to /dev/null when launched from a
	// Hyprland keybind, so write logs to $XDG_STATE_HOME/altzap/altzap.log
	// in addition to stderr. Append-only; tail -f to follow live.
	logPath := filepath.Join(client.AppStateDir(), "altzap.log")
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	} else {
		log.Printf("could not open %s for logging: %v (logs only on stderr)", logPath, err)
	}
	log.Printf("altzap starting (pid=%d)", os.Getpid())

	// Single-instance lock: if another AltZap is already running, send it
	// a "show" signal and exit so launcher invocations (rofi, dock, etc.)
	// raise the existing window instead of spawning a duplicate process.
	// The window slot is filled in below; until then a "show" arriving in
	// the boot window is benignly dropped (boot is sub-second; users don't
	// re-click that fast).
	socketPath := client.SingleInstanceSocketPath()
	var windowSlot atomic.Value // fyne.Window
	releaseLock, isSecondary, err := client.Acquire(socketPath, func() {
		v := windowSlot.Load()
		if v == nil {
			return
		}
		w := v.(fyne.Window)
		fyne.Do(func() {
			w.Show()
			w.RequestFocus()
		})
	})
	if err != nil {
		log.Fatalf("Failed to acquire single-instance lock at %s: %v", socketPath, err)
	}
	if isSecondary {
		log.Printf("single-instance: another AltZap is running at %s; signaled show and exiting", socketPath)
		os.Exit(0)
	}
	log.Printf("single-instance: primary, listening on %s", socketPath)
	defer releaseLock()

	fApp := app.NewWithID("com.altzap.app")
	fApp.Settings().SetTheme(ui.CatppuccinTheme())

	window := fApp.NewWindow("AltZap")
	windowSlot.Store(window)
	window.SetMaster()
	window.Resize(fyne.NewSize(1050, 700))
	window.SetIcon(ui.AppIcon)
	fApp.SetIcon(ui.AppIcon)

	// Ctrl+Q always quits, even if the tray failed to register (so the user
	// is never stuck after a close-to-tray on a desktop without a SNI host).
	window.Canvas().AddShortcut(&desktop.CustomShortcut{
		KeyName:  fyne.KeyQ,
		Modifier: fyne.KeyModifierControl,
	}, func(_ fyne.Shortcut) { fApp.Quit() })

	// ESC dispatch: every dismissible overlay (image/video popup, modal
	// notice, reply preview, …) pushes a dismiss callback on the stack;
	// pressing ESC pops + invokes the topmost.
	ui.InstallEscHandler(window.Canvas())

	dataDir := client.AppDataDir()
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to read working directory: %v", err)
	}
	if err := client.MigrateCWDToXDG(cwd, dataDir); err != nil {
		log.Fatalf("Failed to migrate legacy data layout: %v", err)
	}

	logger := waLog.Stdout("Main", "INFO", false)
	sessionDB := filepath.Join(dataDir, "whatsapp.db") + "?_foreign_keys=on"
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", sessionDB, logger)
	if err != nil {
		log.Fatalf("Failed to create client store: %v", err)
	}

	if _, err := storeContainer.GetFirstDevice(context.Background()); err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	msgStore, err := client.OpenMessageStore(filepath.Join(dataDir, "messages.db"))
	if err != nil {
		log.Fatalf("Failed to open message store: %v", err)
	}
	defer msgStore.Close()

	if migrated, err := client.MigrateLegacyJSONLs(msgStore, dataDir); err != nil {
		log.Printf("warning: legacy JSONL migration failed: %v", err)
	} else if migrated > 0 {
		log.Printf("migrated %d legacy messages from JSONL to SQLite", migrated)
	}

	// Migrate any pre-circular avatar JPGs to circular PNGs (idempotent).
	client.MigrateLegacyAvatars()

	// Pre-transcode any .webp media into .png siblings so Fyne never sees
	// formats its decoder rejects (animated webp, VP8X+VP8L). Idempotent;
	// runs in the background so startup isn't blocked.
	go client.PreTranscodeWebPMedia()

	waClient := client.NewWhatsAppClient(storeContainer, msgStore)
	loginUI := ui.NewLoginUI(fApp, waClient, window)
	var chatView *ui.ChatView

	// Theme switcher: watches ~/.config/altzap/theme. Any tool (rofi,
	// KDE/GNOME script, manual edit) writes the theme name there, AltZap
	// reapplies the palette without restart. ChatView.RefreshAll rolls
	// new colors into bubbles + sidebar; static chrome (chat header)
	// stays with old colors until the next interaction or app restart.
	ui.WatchThemeFile(fApp, func(_ string) {
		if chatView != nil {
			fyne.Do(func() { chatView.RefreshAll() })
		}
	})

	// System tray (where supported). When present, the close button hides the
	// window instead of quitting — user uses tray menu's Quit to actually exit.
	if desk, ok := fApp.(desktop.App); ok {
		showItem := fyne.NewMenuItem("Show", func() {
			window.Show()
			window.RequestFocus()
		})
		quitItem := fyne.NewMenuItem("Quit", func() { fApp.Quit() })
		trayMenu := fyne.NewMenu("AltZap", showItem, fyne.NewMenuItemSeparator(), quitItem)
		desk.SetSystemTrayMenu(trayMenu)
		desk.SetSystemTrayIcon(ui.AppIcon)

		window.SetCloseIntercept(func() { window.Hide() })
	}

	// wireChatView attaches notification + tray-tooltip plumbing to the chat
	// view. Called both on fresh login and when a session resumes.
	wireChatView := func(cv *ui.ChatView) {
		cv.SetNotifyHook(func(sender, chatName, preview string, isGroup bool) {
			ui.NotifyMessage(sender, chatName, preview, isGroup)
		})
		cv.SetTotalUnreadHook(func(total int) {
			title := "AltZap"
			if total > 0 {
				title = fmt.Sprintf("AltZap (%d)", total)
			}
			fyne.Do(func() { window.SetTitle(title) })
		})
	}

	waClient.OnMessage = func(evt client.MessageEvent) {
		log.Printf("Message from %s in %s: %s", evt.SenderName, evt.Info.Chat, evt.Text)
		if chatView != nil {
			chatView.AddMessage(evt)
		}
	}

	waClient.OnHistoryUpdate = func() {
		if chatView != nil {
			chatView.ReloadFromDisk()
		}
	}

	waClient.OnConnected = func() {
		if chatView != nil {
			chatView.ReloadFromDisk()
		}
	}

	waClient.OnLogin = func() {
		log.Println("Login callback triggered - transitioning to chat view")
		// Tear down a prior view (e.g. on relink/re-pair in the same session)
		// so its background refresh loop + ticker don't leak.
		if chatView != nil {
			chatView.Stop()
			chatView.StopRecordingIfActive()
		}
		chatView = ui.NewChatView(fApp, waClient, window)
		wireChatView(chatView)
		loginUI.TransitionToChat(chatView)
	}

	if waClient.IsLoggedIn() {
		waClient.WaitUntilLoggedIn()
		chatView = ui.NewChatView(fApp, waClient, window)
		wireChatView(chatView)
		window.SetContent(chatView.Build())
		if err := waClient.Connect(); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
	} else {
		loginUI.Show()
	}

	// Cleanup on app stop (Quit menu, OS signal). Note: SetOnClosed isn't
	// reliable when the window is intercepted/hidden, so we attach to the
	// app lifecycle instead.
	fApp.Lifecycle().SetOnStopped(func() {
		if chatView != nil {
			chatView.Stop()
			chatView.StopRecordingIfActive()
		}
		ui.StopAudioIfActive()
		waClient.Disconnect()
	})

	window.ShowAndRun()
}
