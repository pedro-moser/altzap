package ui

import (
	"bufio"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// showVideoFullscreen plays a video file inside the app window via an
// overlay popup. Same pattern as showImageFullscreen, but instead of a
// static image we run two ffmpeg children:
//
//   - one decoding the video track to an MJPEG byte stream which we read
//     frame-by-frame and blit into a canvas.Image (visual)
//   - one playing just the audio track with ffplay -vn (sound)
//
// They start in parallel so picture + sound are roughly in sync at the
// granularity of process startup; small drift is acceptable for chat
// playback. Close button or natural EOF tears both down.
func showVideoFullscreen(path string, win fyne.Window) {
	if path == "" || win == nil {
		return
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		dialog := widget.NewLabel("ffmpeg not found in PATH — install ffmpeg for inline video playback.")
		showSimpleNotice("Video playback unavailable", dialog, win)
		return
	}
	if _, err := exec.LookPath("ffplay"); err != nil {
		dialog := widget.NewLabel("ffplay not found in PATH — install ffmpeg/ffplay for inline video playback.")
		showSimpleNotice("Video playback unavailable", dialog, win)
		return
	}

	imgWidget := canvas.NewImageFromImage(blankFrame())
	imgWidget.FillMode = canvas.ImageFillContain
	imgWidget.SetMinSize(fyne.NewSize(640, 360))

	state := &videoPlayer{
		path:    path,
		display: imgWidget,
	}

	var popup *widget.PopUp
	var popEsc func()

	dismiss := func() {
		state.stop()
		if popEsc != nil {
			popEsc()
			popEsc = nil
		}
		if popup != nil {
			popup.Hide()
		}
	}

	closeBtn := widget.NewButtonWithIcon("Close", theme.CancelIcon(), dismiss)
	closeBtn.Importance = widget.LowImportance

	openExtBtn := widget.NewButtonWithIcon("Open externally", theme.MediaPlayIcon(), func() {
		openExternal(path, win)
		dismiss()
	})
	openExtBtn.Importance = widget.LowImportance

	bottomBar := container.NewHBox(layout.NewSpacer(), openExtBtn, closeBtn)
	bg := canvas.NewRectangle(color.RGBA{R: ctpCrust.R, G: ctpCrust.G, B: ctpCrust.B, A: 0xf0})

	content := container.NewBorder(nil, container.NewPadded(bottomBar), nil, nil, imgWidget)
	popup = widget.NewPopUp(container.NewStack(bg, content), win.Canvas())
	popup.Resize(win.Canvas().Size())
	popup.Show()
	popEsc = pushEsc(dismiss)

	state.start()
}

// videoPlayer owns the ffmpeg + ffplay child processes for a single video
// playback session. Stop is idempotent; Start may be called only once.
type videoPlayer struct {
	path    string
	display *canvas.Image

	mu       sync.Mutex
	ffmpeg   *exec.Cmd
	ffplay   *exec.Cmd
	stopping bool
}

func (vp *videoPlayer) start() {
	vp.mu.Lock()
	if vp.ffmpeg != nil || vp.stopping {
		vp.mu.Unlock()
		return
	}

	// Video pipe: ffmpeg → MJPEG on stdout → jpeg.Decode loop → canvas.Image.
	// `-re` gates output to the source frame rate so playback isn't fast-forwarded.
	// `scale=720:-2` keeps -2 (any) for height, ensuring even number for codecs.
	vmCmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-re",
		"-i", vp.path,
		"-an",                 // strip audio (ffplay handles it)
		"-vf", "scale=720:-2", // cap width 720; preserves aspect ratio
		"-r", "25",
		"-f", "image2pipe",
		"-c:v", "mjpeg",
		"-q:v", "5", // moderate JPEG quality — speed > pixel-perfect for chat playback
		"-",
	)
	vmCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	vmStdout, err := vmCmd.StdoutPipe()
	if err != nil {
		vp.mu.Unlock()
		log.Printf("video pipe: %v", err)
		return
	}
	if err := vmCmd.Start(); err != nil {
		vp.mu.Unlock()
		log.Printf("video ffmpeg start: %v", err)
		return
	}
	vp.ffmpeg = vmCmd

	// Audio: ffplay with no display, no video, autoexit. Routed through the
	// user-chosen output sink if any (settings page).
	apCmd := exec.Command("ffplay",
		"-hide_banner", "-loglevel", "error",
		"-nodisp", "-vn", "-autoexit",
		vp.path,
	)
	apCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if sink := AudioOutputDevice(); sink != "" {
		apCmd.Env = append(os.Environ(), "PULSE_SINK="+sink)
	}
	if err := apCmd.Start(); err != nil {
		log.Printf("video ffplay start: %v", err)
		// Visual still works; just no sound.
	} else {
		vp.ffplay = apCmd
	}
	vp.mu.Unlock()

	go vp.readLoop(vmStdout)
	go func() {
		_ = vmCmd.Wait()
	}()
	if apCmd != nil {
		go func() {
			_ = apCmd.Wait()
		}()
	}
}

// readLoop pulls one JPEG-encoded frame at a time off ffmpeg's stdout and
// pushes it into the display canvas. Exits when the pipe drains (EOF on
// natural EOF or after stop kills ffmpeg).
//
// Important: jpeg.Decode wraps non-buffered readers in a fresh bufio.Reader
// each call, which would discard bytes already read past the EOI marker.
// We pre-wrap once so the buffered position carries across frames.
func (vp *videoPlayer) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 256*1024)
	for {
		frame, err := jpeg.Decode(br)
		if err != nil {
			return
		}
		fyne.Do(func() {
			vp.display.Image = frame
			vp.display.Refresh()
		})
	}
}

// stop tears down both child processes. Safe to call multiple times.
func (vp *videoPlayer) stop() {
	vp.mu.Lock()
	if vp.stopping {
		vp.mu.Unlock()
		return
	}
	vp.stopping = true
	ffmpeg, ffplay := vp.ffmpeg, vp.ffplay
	vp.ffmpeg, vp.ffplay = nil, nil
	vp.mu.Unlock()

	killProc(ffmpeg)
	killProc(ffplay)
}

func killProc(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

// blankFrame returns a 1x1 black RGBA image used as the initial display
// before the first decoded frame lands.
func blankFrame() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: ctpCrust.R, G: ctpCrust.G, B: ctpCrust.B, A: 0xff})
	return img
}

// showSimpleNotice displays a small "Close" dialog overlay used for
// dependency-missing errors (ffmpeg / ffplay not installed). Non-fatal.
func showSimpleNotice(title string, body fyne.CanvasObject, win fyne.Window) {
	var p *widget.PopUp
	var popEsc func()

	dismiss := func() {
		if popEsc != nil {
			popEsc()
			popEsc = nil
		}
		if p != nil {
			p.Hide()
		}
	}

	closeBtn := widget.NewButtonWithIcon("Close", theme.CancelIcon(), dismiss)
	titleLbl := widget.NewLabel(title)
	titleLbl.TextStyle.Bold = true
	content := container.NewVBox(titleLbl, body, closeBtn)
	p = widget.NewModalPopUp(container.NewPadded(content), win.Canvas())
	p.Show()
	popEsc = pushEsc(dismiss)
}
