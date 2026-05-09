# AltZap

Lightweight, native WhatsApp desktop client for Linux. Built in Go with [Fyne](https://fyne.io) and [whatsmeow](https://github.com/tulir/whatsmeow).

> **Disclaimer**: AltZap uses whatsmeow, an unofficial reverse-engineered WhatsApp client library. **Use at your own risk** — the WhatsApp Terms of Service prohibit unofficial clients, and accounts have been banned for using them. Not affiliated with WhatsApp, Meta, or Tulir Asokan. This is a personal project shared in case it's useful to others.

## Why

I couldn't find a WhatsApp client that was light enough, so I asked my good friend Claude to help me out in this task. AltZap is a Fyne-based Go app that idles around 80 MB and starts in under two seconds.

## Features

- QR-code login with persistent local sessions (SQLite)
- Send and receive text, media, replies, reactions, edits, delete-for-everyone
- Voice notes (record and playback)
- Inline media: image, video, audio, voice, document, sticker
- Delivery and read receipts (single/double check)
- Desktop notifications, system tray, unread badge in the title bar
- Catppuccin theme with hot-swap — write a theme name to `~/.config/altzap/theme` and the running app reapplies the palette
- Performance work: virtualized message list, image-decode cache, lazy bubble rendering

## Status

**Alpha.** Daily-driven by the author since early 2026, but rough edges should be expected. The on-disk schema is stable; behavior and UI are not.

## Build

System dependencies (Linux only — macOS and Windows are untested):

| Distro | Command |
|--------|---------|
| Debian/Ubuntu | `sudo apt install -y gcc libgl1-mesa-dev libxinerama-dev libxcursor-dev libxrandr-dev libxi-dev libxxf86vm-dev pkg-config` |
| Arch | `sudo pacman -S gcc mesa libxinerama libxcursor libxrandr libxi libxxf86vm pkgconf` |
| Fedora | `sudo dnf install gcc mesa-libGL-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel pkgconf-pkg-config` |

Then:

```bash
go build -o altzap .
./altzap
```

First run shows a QR code. Scan it with WhatsApp on your phone (Settings → Linked Devices → Link a device).

## How it works

```
main.go ─┬─ client/  whatsmeow + SQLite persistence (whatsapp.db, store/messages.db)
         └─ ui/      Fyne v2.7 widgets (chat view, login, media, settings)
```

Two SQLite databases sit next to the binary:

- `whatsapp.db` — whatsmeow session and device state (managed by the library)
- `store/messages.db` — local message store with O(log N) lookups for receipts and edits

Design notes for the bigger decisions live under `docs/superpowers/specs/`.

## Files and configuration

AltZap follows the XDG Base Directory specification.

**Configs** (`~/.config/altzap/`):

| File | Purpose |
|------|---------|
| `theme` | One word: `latte`, `frappe`, `macchiato`, or `mocha`. Hot-reloaded. |
| `settings.json` | Persisted settings (media autodownload, current theme). |

**Data** (`~/.local/share/altzap/`):

| Path | Purpose |
|------|---------|
| `whatsapp.db` | Whatsmeow session and device state (managed by the library). |
| `messages.db` | Local message store (chats, reactions, edits, receipts). |
| `media/` | Downloaded media organized as `<chat-jid>/<msg-id>.<ext>` plus `avatars/`. |

The data directory honors `XDG_DATA_HOME` if set, and an `ALTZAP_DATA_DIR` environment variable overrides both for portable / dev / testing setups.

The first run after upgrading from v0.1 will move any data found at the working directory (`./whatsapp.db`, `./store/`, `./media/`) into the XDG location automatically.

## Contributing

Pull requests are welcome. The codebase uses [conventional commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `perf:`, `refactor:`, `chore:`, `docs:`).

Before opening a PR, please run:

```bash
gofmt -l .         # must print nothing
go vet ./...
go test ./...
```

CI runs the same checks on every push.

## License

[MIT](LICENSE).

Built on top of [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0) and [Fyne](https://fyne.io) (BSD-3-Clause).
