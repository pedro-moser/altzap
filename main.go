package main

import (
	"context"
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatsappalt/client"
	"whatsappalt/ui"
)

func main() {
	fApp := app.NewWithID("com.wazegoo.whatsapp")
	fApp.Settings().SetTheme(ui.CatppuccinTheme())

	window := fApp.NewWindow("WhatsApp Alt")
	window.SetMaster()
	window.Resize(fyne.NewSize(1050, 700))

	logger := waLog.Stdout("Main", "INFO", false)
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", "whatsapp.db?_foreign_keys=on", logger)
	if err != nil {
		log.Fatalf("Failed to create client store: %v", err)
	}

	if _, err := storeContainer.GetFirstDevice(context.Background()); err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	waClient := client.NewWhatsAppClient(storeContainer)
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
		trayMenu := fyne.NewMenu("WhatsApp Alt", showItem, fyne.NewMenuItemSeparator(), quitItem)
		desk.SetSystemTrayMenu(trayMenu)
		desk.SetSystemTrayIcon(theme.MailComposeIcon())

		window.SetCloseIntercept(func() { window.Hide() })
	}

	// wireChatView attaches notification + tray-tooltip plumbing to the chat
	// view. Called both on fresh login and when a session resumes.
	wireChatView := func(cv *ui.ChatView) {
		cv.SetNotifyHook(func(sender, chatName, preview string, isGroup bool) {
			ui.NotifyMessage(sender, chatName, preview, isGroup)
		})
		cv.SetTotalUnreadHook(func(total int) {
			title := "WhatsApp Alt"
			if total > 0 {
				title = fmt.Sprintf("WhatsApp Alt (%d)", total)
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
		waClient.Disconnect()
	})

	window.ShowAndRun()
}
