package client

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder so DetectContentType+image.Decode handles it
	"log"

	_ "golang.org/x/image/webp" // register WebP decoder

	"golang.org/x/image/draw"
)

const thumbMaxDim = 240

// decodeAndThumbnail decodes raw image bytes and returns its dimensions plus
// a small JPEG thumbnail (~thumbMaxDim on the long side) suitable for the
// JPEGThumbnail field of an ImageMessage. On any error, returns zeros.
func decodeAndThumbnail(data []byte) (width, height int, thumb []byte) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		log.Printf("thumbnail: decode failed: %v", err)
		return 0, 0, nil
	}

	bounds := img.Bounds()
	width, height = bounds.Dx(), bounds.Dy()

	tw, th := width, height
	if tw > thumbMaxDim || th > thumbMaxDim {
		if tw >= th {
			th = th * thumbMaxDim / tw
			tw = thumbMaxDim
		} else {
			tw = tw * thumbMaxDim / th
			th = thumbMaxDim
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 70}); err != nil {
		log.Printf("thumbnail: encode failed: %v", err)
		return width, height, nil
	}
	return width, height, buf.Bytes()
}
