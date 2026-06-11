package client

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// ErrMediaNotDownloaded means a forward was requested for a media message
// whose file isn't on disk (history-sync records never download, and live
// downloads can still be in flight). The UI turns this into a friendly
// "media not downloaded yet" notice.
var ErrMediaNotDownloaded = errors.New("media not downloaded yet")

// forwardContext is the ContextInfo that makes recipients render the
// "Forwarded" tag. Score 1 = first hop, matching what official clients
// send for a fresh forward.
func forwardContext() *waE2E.ContextInfo {
	return &waE2E.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(1),
	}
}

// ForwardMessage re-sends src's content to dst marked as forwarded. Text
// goes as an ExtendedTextMessage; media is re-uploaded from the local file
// (re-upload is simpler and more robust than copying media keys, which can
// expire server-side). Returns the persisted outgoing record so the UI can
// render the destination bubble optimistically.
func (w *WhatsAppClient) ForwardMessage(dst types.JID, src SavedMessage) (SavedMessage, error) {
	if !w.IsConnected() {
		return SavedMessage{}, fmt.Errorf("not connected to WhatsApp")
	}
	if src.MediaType == "" {
		return w.forwardText(dst, src.Text)
	}
	return w.forwardMedia(dst, src)
}

func (w *WhatsAppClient) forwardText(dst types.JID, text string) (SavedMessage, error) {
	if text == "" {
		return SavedMessage{}, fmt.Errorf("nothing to forward")
	}
	// Conversation can't carry ContextInfo — the forwarded tag needs the
	// extended variant.
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: forwardContext(),
		},
	}
	resp, err := w.client.SendMessage(context.Background(), dst, msg)
	if err != nil {
		return SavedMessage{}, err
	}
	return w.finishOutgoing(SavedMessage{
		ID:        resp.ID,
		ChatJID:   dst.String(),
		Text:      text,
		Timestamp: resp.Timestamp.Unix(),
		FromMe:    true,
	})
}

func (w *WhatsAppClient) forwardMedia(dst types.JID, src SavedMessage) (SavedMessage, error) {
	if src.MediaPath == "" {
		return SavedMessage{}, ErrMediaNotDownloaded
	}
	absPath := AbsoluteMediaPath(src.MediaPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return SavedMessage{}, ErrMediaNotDownloaded
		}
		return SavedMessage{}, fmt.Errorf("read media: %w", err)
	}

	msg, mime, err := w.buildForwardedMedia(src, data)
	if err != nil {
		return SavedMessage{}, err
	}

	resp, err := w.client.SendMessage(context.Background(), dst, msg)
	if err != nil {
		return SavedMessage{}, err
	}

	return w.finishOutgoing(SavedMessage{
		ID:        resp.ID,
		ChatJID:   dst.String(),
		Text:      src.Text, // caption travels with the media
		Timestamp: resp.Timestamp.Unix(),
		FromMe:    true,
		MediaType: src.MediaType,
		MediaPath: stashOutgoingMedia(absPath, dst.String(), resp.ID, mime),
		Mimetype:  mime,
		FileName:  src.FileName,
		FileSize:  uint64(len(data)),
		Width:     src.Width,
		Height:    src.Height,
		Duration:  src.Duration,
		ThumbB64:  src.ThumbB64,
	})
}

// buildForwardedMedia uploads data and assembles the proto variant matching
// src.MediaType, tagged with the forwarded ContextInfo. Returns the wire
// message plus the mimetype recorded locally.
func (w *WhatsAppClient) buildForwardedMedia(src SavedMessage, data []byte) (*waE2E.Message, string, error) {
	srcThumb := decodeThumbB64(src.ThumbB64)

	switch src.MediaType {
	case "image":
		resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaImage)
		if err != nil {
			return nil, "", fmt.Errorf("upload image: %w", err)
		}
		mime := http.DetectContentType(data)
		width, height, thumb := decodeAndThumbnail(data)
		im := &waE2E.ImageMessage{
			Mimetype:      proto.String(mime),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			ContextInfo:   forwardContext(),
		}
		if src.Text != "" {
			im.Caption = proto.String(src.Text)
		}
		if width > 0 && height > 0 {
			im.Width = proto.Uint32(uint32(width))
			im.Height = proto.Uint32(uint32(height))
		}
		if len(thumb) > 0 {
			im.JPEGThumbnail = thumb
		}
		return &waE2E.Message{ImageMessage: im}, mime, nil

	case "video":
		resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaVideo)
		if err != nil {
			return nil, "", fmt.Errorf("upload video: %w", err)
		}
		mime := src.Mimetype
		if mime == "" {
			mime = "video/mp4"
		}
		vm := &waE2E.VideoMessage{
			Mimetype:      proto.String(mime),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			ContextInfo:   forwardContext(),
		}
		if src.Text != "" {
			vm.Caption = proto.String(src.Text)
		}
		if src.Duration > 0 {
			vm.Seconds = proto.Uint32(src.Duration)
		}
		if src.Width > 0 && src.Height > 0 {
			vm.Width = proto.Uint32(src.Width)
			vm.Height = proto.Uint32(src.Height)
		}
		if len(srcThumb) > 0 {
			vm.JPEGThumbnail = srcThumb
		}
		return &waE2E.Message{VideoMessage: vm}, mime, nil

	case "audio", "voice":
		resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaAudio)
		if err != nil {
			return nil, "", fmt.Errorf("upload audio: %w", err)
		}
		mime := src.Mimetype
		if mime == "" {
			mime = "audio/ogg; codecs=opus"
		}
		am := &waE2E.AudioMessage{
			Mimetype:      proto.String(mime),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			ContextInfo:   forwardContext(),
		}
		if src.Duration > 0 {
			am.Seconds = proto.Uint32(src.Duration)
		}
		if src.MediaType == "voice" {
			am.PTT = proto.Bool(true)
		}
		return &waE2E.Message{AudioMessage: am}, mime, nil

	case "document":
		resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaDocument)
		if err != nil {
			return nil, "", fmt.Errorf("upload document: %w", err)
		}
		mime := src.Mimetype
		if mime == "" {
			mime = http.DetectContentType(data)
		}
		fileName := src.FileName
		if fileName == "" {
			fileName = "document"
		}
		dm := &waE2E.DocumentMessage{
			Mimetype:      proto.String(mime),
			FileName:      proto.String(fileName),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			ContextInfo:   forwardContext(),
		}
		if src.Text != "" {
			dm.Caption = proto.String(src.Text)
		}
		return &waE2E.Message{DocumentMessage: dm}, mime, nil

	case "sticker":
		// Stickers upload through the image pipe (whatsmeow has no
		// dedicated sticker MediaType).
		resp, err := w.client.Upload(context.Background(), data, whatsmeow.MediaImage)
		if err != nil {
			return nil, "", fmt.Errorf("upload sticker: %w", err)
		}
		mime := src.Mimetype
		if mime == "" {
			mime = "image/webp"
		}
		sm := &waE2E.StickerMessage{
			Mimetype:      proto.String(mime),
			URL:           &resp.URL,
			DirectPath:    &resp.DirectPath,
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			ContextInfo:   forwardContext(),
		}
		if src.Width > 0 && src.Height > 0 {
			sm.Width = proto.Uint32(src.Width)
			sm.Height = proto.Uint32(src.Height)
		}
		return &waE2E.Message{StickerMessage: sm}, mime, nil
	}

	return nil, "", fmt.Errorf("can't forward media type %q", src.MediaType)
}

// decodeThumbB64 is a forgiving base64 decode for stored thumbnails —
// returns nil on empty or corrupt input.
func decodeThumbB64(b64 string) []byte {
	if b64 == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	return b
}
