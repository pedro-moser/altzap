package main

import (
	"context"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	_ "github.com/mattn/go-sqlite3"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/store/sqlstore"
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

	_, err = storeContainer.GetFirstDevice(context.Background())
	if err != nil {
		log.Printf("Warning: could not get device: %v", err)
	}

	waClient := client.NewWhatsAppClient(storeContainer)

	loginUI := ui.NewLoginUI(fApp, waClient, window)
	var chatView *ui.ChatView

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
		loginUI.TransitionToChat(chatView)
	}

	if waClient.IsLoggedIn() {
		waClient.WaitUntilLoggedIn()
		if err := waClient.Connect(); err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		chatView = ui.NewChatView(fApp, waClient, window)
		window.SetContent(chatView.Build())
	} else {
		loginUI.Show()
	}

	window.SetOnClosed(func() {
		if chatView != nil {
			chatView.StopRecordingIfActive()
		}
		waClient.Disconnect()
	})
	window.ShowAndRun()
}
