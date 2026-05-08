package ui

import (
	"fmt"
	"image/color"
	"os/exec"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// openExternal launches the OS default handler for the given file path.
// Linux: xdg-open. macOS: open. Windows: rundll32 url.dll. Errors are silent
// — the user will notice if nothing opens, and we don't want to block the UI.
func openExternal(path string) {
	if path == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		return
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}

// humanizeBytes turns 1234567 into "1.2 MB".
func humanizeBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// previewDims caps preview size to a sensible bubble area, preserving aspect.
func previewDims(w, h uint32) (float32, float32) {
	const maxW, maxH float32 = 320, 320
	if w == 0 || h == 0 {
		return 240, 180
	}
	fw, fh := float32(w), float32(h)
	var scale float32 = 1
	if fw/fh > maxW/maxH {
		scale = maxW / fw
	} else {
		scale = maxH / fh
	}
	if scale > 1 {
		scale = 1
	}
	return fw * scale, fh * scale
}

// imageContent prefers the downloaded file, falls back to the embedded
// JPEGThumbnail, and finally to a placeholder icon.
func imageContent(msg *Message) fyne.CanvasObject {
	var img *canvas.Image
	switch {
	case msg.MediaPath != "":
		img = canvas.NewImageFromFile(msg.MediaPath)
	case len(msg.Thumb) > 0:
		img = canvas.NewImageFromResource(fyne.NewStaticResource("thumb_"+msg.ID, msg.Thumb))
	default:
		return widget.NewIcon(theme.FileImageIcon())
	}
	img.FillMode = canvas.ImageFillContain
	w, h := previewDims(msg.Width, msg.Height)
	img.SetMinSize(fyne.NewSize(w, h))
	return img
}

func buildImageBubble(msg *Message, window fyne.Window) fyne.CanvasObject {
	content := imageContent(msg)

	openBtn := widget.NewButtonWithIcon("Open", theme.ZoomFitIcon(), func() {
		showImageFullscreen(msg.MediaPath, window)
	})
	openBtn.Importance = widget.LowImportance
	if msg.MediaPath == "" {
		openBtn.SetText("Downloading…")
		openBtn.Disable()
	}

	rows := []fyne.CanvasObject{content}
	if msg.Text != "" {
		caption := widget.NewLabel(msg.Text)
		caption.Wrapping = fyne.TextWrapWord
		rows = append(rows, caption)
	}
	rows = append(rows, container.NewHBox(openBtn))
	return container.NewVBox(rows...)
}

// showImageFullscreen opens a near-full-window overlay with the image in
// contain mode. Tapping outside the popup dismisses it; the close button
// also works. "Open externally" is offered as a secondary action for users
// who want their OS image viewer.
func showImageFullscreen(path string, win fyne.Window) {
	if path == "" || win == nil {
		return
	}
	img := canvas.NewImageFromFile(path)
	img.FillMode = canvas.ImageFillContain

	var popup *widget.PopUp
	closeBtn := widget.NewButtonWithIcon("Close", theme.CancelIcon(), func() {
		if popup != nil {
			popup.Hide()
		}
	})
	closeBtn.Importance = widget.LowImportance

	openExtBtn := widget.NewButtonWithIcon("Open externally", theme.ZoomFitIcon(), func() {
		openExternal(path)
		if popup != nil {
			popup.Hide()
		}
	})
	openExtBtn.Importance = widget.LowImportance

	bottomBar := container.NewHBox(layout.NewSpacer(), openExtBtn, closeBtn)
	bg := canvas.NewRectangle(color.RGBA{R: 0x11, G: 0x11, B: 0x1b, A: 0xf0})

	content := container.NewBorder(nil, container.NewPadded(bottomBar), nil, nil, img)
	popup = widget.NewPopUp(container.NewStack(bg, content), win.Canvas())
	popup.Resize(win.Canvas().Size())
	popup.Show()
}

func buildVideoBubble(msg *Message) fyne.CanvasObject {
	var thumb fyne.CanvasObject
	if len(msg.Thumb) > 0 {
		img := canvas.NewImageFromResource(fyne.NewStaticResource("vthumb_"+msg.ID, msg.Thumb))
		img.FillMode = canvas.ImageFillContain
		w, h := previewDims(msg.Width, msg.Height)
		img.SetMinSize(fyne.NewSize(w, h))
		thumb = img
	} else {
		ic := widget.NewIcon(theme.MediaVideoIcon())
		thumb = container.New(layout.NewGridWrapLayout(fyne.NewSize(240, 180)), ic)
	}

	overlay := canvas.NewText("▶", whiteColor)
	overlay.TextSize = 36
	overlay.TextStyle.Bold = true
	stacked := container.NewStack(thumb, container.NewCenter(overlay))

	durTxt := ""
	if msg.Duration > 0 {
		durTxt = fmt.Sprintf("  %d:%02d", msg.Duration/60, msg.Duration%60)
	}
	openBtn := widget.NewButtonWithIcon("Play"+durTxt, theme.MediaPlayIcon(), func() {
		openExternal(msg.MediaPath)
	})
	openBtn.Importance = widget.LowImportance
	if msg.MediaPath == "" {
		openBtn.SetText("Downloading…" + durTxt)
		openBtn.Disable()
	}

	rows := []fyne.CanvasObject{stacked}
	if msg.Text != "" {
		caption := widget.NewLabel(msg.Text)
		caption.Wrapping = fyne.TextWrapWord
		rows = append(rows, caption)
	}
	rows = append(rows, container.NewHBox(openBtn))
	return container.NewVBox(rows...)
}

func buildAudioBubble(msg *Message) fyne.CanvasObject {
	var icon fyne.Resource
	if msg.MediaType == "voice" {
		icon = theme.MediaRecordIcon()
	} else {
		icon = theme.MediaMusicIcon()
	}

	durTxt := "—:—"
	if msg.Duration > 0 {
		durTxt = fmt.Sprintf("%d:%02d", msg.Duration/60, msg.Duration%60)
	}
	label := widget.NewLabel(durTxt)
	label.TextStyle.Monospace = true

	var playBtn *widget.Button
	setIcon := func(playing bool) {
		if playBtn == nil {
			return
		}
		if playing {
			playBtn.SetIcon(theme.MediaStopIcon())
		} else {
			playBtn.SetIcon(theme.MediaPlayIcon())
		}
	}

	playBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {
		if isPlayingMsg(msg.ID) {
			stopAudio()
			return
		}
		if err := playAudio(msg.ID, msg.MediaPath, setIcon); err != nil {
			// Silent fall-through; button stays in the play state. We could
			// surface a dialog but the most common cause (no ffplay) is fixed
			// once and then the button works for the session.
			return
		}
	})

	// Reflect the live controller state when the bubble is rebuilt mid-play
	// (e.g. after a sibling chat refresh).
	if isPlayingMsg(msg.ID) {
		playBtn.SetIcon(theme.MediaStopIcon())
	}
	if msg.MediaPath == "" || !hasFFplay {
		playBtn.Disable()
	}

	row := container.NewBorder(nil, nil,
		widget.NewIcon(icon),
		container.NewHBox(label, playBtn),
		nil,
	)
	return container.New(layout.NewGridWrapLayout(fyne.NewSize(240, 44)), row)
}

func buildDocBubble(msg *Message) fyne.CanvasObject {
	name := msg.FileName
	if name == "" {
		name = "Document"
	}
	nameLbl := widget.NewLabel(name)
	nameLbl.TextStyle.Bold = true
	nameLbl.Truncation = fyne.TextTruncateEllipsis

	sizeLbl := widget.NewLabel(humanizeBytes(msg.FileSize))
	sizeLbl.Importance = widget.LowImportance

	openBtn := widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() {
		openExternal(msg.MediaPath)
	})
	if msg.MediaPath == "" {
		openBtn.SetText("Downloading…")
		openBtn.Disable()
	}

	textCol := container.NewVBox(nameLbl, sizeLbl)
	row := container.NewBorder(nil, nil,
		widget.NewIcon(theme.FileIcon()),
		openBtn,
		textCol,
	)
	return container.New(layout.NewGridWrapLayout(fyne.NewSize(320, 60)), row)
}

func buildStickerBubble(msg *Message) fyne.CanvasObject {
	const stickerSide float32 = 140
	if msg.MediaPath != "" {
		img := canvas.NewImageFromFile(msg.MediaPath)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(stickerSide, stickerSide))
		return img
	}
	if len(msg.Thumb) > 0 {
		img := canvas.NewImageFromResource(fyne.NewStaticResource("sthumb_"+msg.ID, msg.Thumb))
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(stickerSide, stickerSide))
		return img
	}
	return widget.NewIcon(theme.FileImageIcon())
}
