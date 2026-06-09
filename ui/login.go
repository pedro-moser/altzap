package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"sync"
	"time"

	"altzap/client"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/skip2/go-qrcode"
)

type qrState struct {
	code    []byte
	status  string
	errMsg  error
	done    bool
	timeout bool
}

func decodePNG(b []byte) (image.Image, error) {
	r := io.NopCloser(bytes.NewReader(b))
	return png.Decode(r)
}

type LoginUI struct {
	fyneApp   fyne.App
	waClient  *client.WhatsAppClient
	window    fyne.Window
	qrImage   *canvas.Image
	status    *widget.Label
	started   bool
	mu        sync.Mutex
	cancelFn  context.CancelFunc
	cancelCtx context.Context
	qrChan    chan qrState
	ticker    *time.Ticker
}

func NewLoginUI(fyneApp fyne.App, waClient *client.WhatsAppClient, window fyne.Window) *LoginUI {
	return &LoginUI{
		fyneApp:  fyneApp,
		waClient: waClient,
		window:   window,
		started:  false,
	}
}

func (l *LoginUI) Show() {
	l.window.SetContent(l.buildLoginView())
}

func (l *LoginUI) ShowLogin() {
	l.window.SetContent(l.buildLoginView())
	l.window.Show()
}

func (l *LoginUI) buildLoginView() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))

	cardBg := canvas.NewRectangle(theme.Color(theme.ColorNameButton))
	cardBg.CornerRadius = 16

	logoIcon := canvas.NewImageFromResource(AppIcon)
	logoIcon.FillMode = canvas.ImageFillContain
	logoIcon.SetMinSize(fyne.NewSize(96, 96))
	logoIconContainer := container.NewCenter(logoIcon)

	title := widget.NewLabel("AltZap")
	title.TextStyle.Bold = true
	title.Alignment = fyne.TextAlignCenter
	title.Wrapping = fyne.TextWrapWord

	subtitle := widget.NewLabel("Scan the QR code to connect")
	subtitle.Alignment = fyne.TextAlignCenter
	subtitle.Wrapping = fyne.TextWrapWord
	subtitle.Importance = widget.MediumImportance

	headerContainer := container.NewBorder(
		container.NewVBox(layout.NewSpacer(), logoIconContainer, layout.NewSpacer()),
		container.NewVBox(layout.NewSpacer(), title, layout.NewSpacer()),
		nil, nil,
		container.NewVBox(layout.NewSpacer(), subtitle, layout.NewSpacer()),
	)

	l.qrImage = canvas.NewImageFromResource(theme.ContentAddIcon())
	l.qrImage.FillMode = canvas.ImageFillContain
	l.qrImage.SetMinSize(fyne.NewSize(220, 220))

	qrInnerBox := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	qrInnerBox.CornerRadius = 12

	qrInner := container.NewStack(qrInnerBox, container.NewCenter(l.qrImage))

	l.status = widget.NewLabel("Click \"Generate QR Code\" to start")
	l.status.Alignment = fyne.TextAlignCenter
	l.status.Wrapping = fyne.TextWrapWord
	l.status.Importance = widget.MediumImportance

	qrSection := container.NewVBox(
		container.NewPadded(qrInner),
		container.NewPadded(l.status),
	)

	generateBtn := widget.NewButton("Generate QR Code", l.onGenerateQR)
	generateBtn.Importance = widget.HighImportance

	footer := widget.NewLabel("Open WhatsApp on your phone > Linked Devices > Link a Device")
	footer.Alignment = fyne.TextAlignCenter
	footer.Wrapping = fyne.TextWrapWord
	footer.Importance = widget.LowImportance

	cardContent := container.NewBorder(
		container.NewPadded(headerContainer),
		container.NewPadded(container.NewVBox(generateBtn, footer)),
		nil, nil,
		container.NewCenter(qrSection),
	)

	// Fix the layout to be more responsive and properly centered
	mainLayout := container.NewStack(
		background,
		container.NewCenter(container.NewPadded(container.NewStack(cardBg, cardContent))),
	)

	return mainLayout
}

func (l *LoginUI) onGenerateQR() {
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return
	}
	l.started = true
	l.mu.Unlock()

	l.setStatus("Connecting to WhatsApp...")
	l.qrImage.Resource = theme.ContentAddIcon()
	l.qrImage.Refresh()

	ctx, cancel := context.WithCancel(context.Background())
	l.cancelFn = cancel
	l.cancelCtx = ctx

	ch, err := l.waClient.GetQRChannel(ctx)
	if err != nil {
		cancel()
		l.setStatus(fmt.Sprintf("Failed to start: %v", err))
		l.mu.Lock()
		l.started = false
		l.mu.Unlock()
		return
	}

	// Connect first, then start the QR channel
	if err := l.waClient.Connect(); err != nil {
		cancel()
		l.setStatus(fmt.Sprintf("Failed to connect: %v", err))
		l.mu.Lock()
		l.started = false
		l.mu.Unlock()
		return
	}

	l.qrChan = make(chan qrState, 16)

	l.ticker = time.NewTicker(50 * time.Millisecond)
	go func() {
		sendState := func(s qrState) bool {
			select {
			case l.qrChan <- s:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			select {
			case item, ok := <-ch:
				if !ok {
					sendState(qrState{done: true})
					return
				}
				if item.Event == "success" {
					sendState(qrState{done: true})
					return
				}
				if item.Event == "timeout" {
					sendState(qrState{done: true, timeout: true})
					return
				}
				if item.Error != nil {
					sendState(qrState{errMsg: fmt.Errorf("QR error: %v", item.Error)})
					return
				}
				if item.Code != "" {
					qrCode, err := qrcode.New(item.Code, qrcode.Medium)
					if err != nil {
						continue
					}
					imgBytes, err := qrCode.PNG(256)
					if err != nil {
						continue
					}
					if !sendState(qrState{code: imgBytes}) {
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go l.tickLoop(ctx, l.ticker, l.qrChan)
}

func (l *LoginUI) tickLoop(ctx context.Context, ticker *time.Ticker, qrChan <-chan qrState) {
	defer ticker.Stop()
	for range ticker.C {
		select {
		case s, ok := <-qrChan:
			if !ok {
				return
			}
			if s.done && s.timeout {
				fyne.Do(func() {
					l.mu.Lock()
					l.started = false
					l.mu.Unlock()
					l.status.SetText("QR expired. Click to generate a new one.")
				})
				return
			}
			if s.done {
				return
			}
			if s.errMsg != nil {
				fyne.Do(func() {
					l.mu.Lock()
					l.started = false
					l.mu.Unlock()
					l.status.SetText(fmt.Sprintf("Error: %v", s.errMsg))
				})
				return
			}
			if len(s.code) > 0 {
				fyne.Do(func() {
					l.status.SetText("Scan the QR code with WhatsApp on your phone")
					// For direct image update, we need to use a new resource
					l.qrImage.Resource = fyne.NewStaticResource("qr", s.code)
					l.qrImage.Refresh()
				})
			}
		case <-ctx.Done():
			return
		}
	}
}

func (l *LoginUI) setStatus(msg string) {
	l.status.SetText(msg)
}

func (l *LoginUI) TransitionToChat(chatView interface {
	Build() fyne.CanvasObject
	RestoreKeyboardFocus()
}) {
	fyne.Do(func() {
		if l.cancelFn != nil {
			l.cancelFn()
		}
		dialog.ShowInformation("Connected", "Successfully logged in to WhatsApp!", l.window)
		l.window.SetContent(chatView.Build())
		chatView.RestoreKeyboardFocus()
	})
}
