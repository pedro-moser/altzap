package main

import (
	"context"
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"altzap/client"
	"altzap/ui"
)

func main() {
	fApp := app.NewWithID("com.altzap.app")
	fApp.Settings().SetTheme(ui.CatppuccinTheme())

	window := fApp.NewWindow("AltZap")
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

	logger := waLog.Stdout("Main", "INFO", false)
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", "whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		log.Fatalf("Failed to create client store: %v", err)
	}

	if _, err := storeContainer.GetFirstDevice(context.Background()); err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	msgStore, err := client.OpenMessageStore("store/messages.db")
	if err != nil {
		log.Fatalf("Failed to open message store: %v", err)
	}
	defer msgStore.Close()

	if migrated, err := client.MigrateLegacyJSONLs(msgStore, "store"); err != nil {
		log.Printf("warning: legacy JSONL migration failed: %v", err)
	} else if migrated > 0 {
		log.Printf("migrated %d legacy messages from JSONL to SQLite", migrated)
	}

	// Migrate any pre-circular avatar JPGs to circular PNGs (idempotent).
	client.MigrateLegacyAvatars()

	waClient := client.NewWhatsAppClient(storeContainer, msgStore)
	loginUI := ui.NewLoginUI(fApp, waClient, window)
	var chatView *ui.ChatView

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

	waClient.OnLogin = func() {
		log.Println("Login callback triggered - transitioning to chat view")
		chatView = ui.NewChatView(fApp, waClient, window)
		wireChatView(chatView)
		loginUI.TransitionToChat(chatView)
	}

	if waClient.IsLoggedIn() {
		waClient.WaitUntilLoggedIn()
		if err := waClient.Connect(); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		chatView = ui.NewChatView(fApp, waClient, window)
		wireChatView(chatView)
		window.SetContent(chatView.Build())
	} else {
		loginUI.Show()
	}

	// Cleanup on app stop (Quit menu, OS signal). Note: SetOnClosed isn't
	// reliable when the window is intercepted/hidden, so we attach to the
	// app lifecycle instead.
	fApp.Lifecycle().SetOnStopped(func() {
		if chatView != nil {
			chatView.StopRecordingIfActive()
		}
		ui.StopAudioIfActive()
		waClient.Disconnect()
	})

	window.ShowAndRun()
}
