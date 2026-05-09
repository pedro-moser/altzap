# XDG Path Layout + Auto-Migration — AltZap v0.2

**Data**: 2026-05-09
**Status**: Aprovado — pendente plano de implementação

## Contexto

A v0.1.0 abre todos os arquivos persistentes via paths relativos ao CWD:

- `whatsapp.db` (whatsmeow session) em `main.go:41`
- `store/messages.db` em `main.go:50`
- `store` (dir pra `MigrateLegacyJSONLs` e legacy JSONLs) em `main.go:56`
- `mediaDir = "media"` em `client/media.go:16`, usado em `avatars.go`, `webp_render.go`

Configs já estão XDG-compliant (`~/.config/altzap/{theme,settings.json,audio.json}` em `ui/theme_watcher.go` e `ui/settings.go`). Só os **dados** estão CWD-bound.

Consequência prática: o app só funciona se for executado a partir do diretório do binário. Atalhos de WM, `go install`, empacotamento futuro (AUR/flatpak) e qualquer cenário onde CWD ≠ diretório dos dados quebram com `Failed to open message store: no such file or directory`.

A v0.2 resolve isso: dados vão pra `~/.local/share/altzap/` (XDG_DATA_HOME), com migração automática one-shot dos dados legacy do CWD pra essa nova localização.

## Objetivos

- App roda corretamente a partir de qualquer CWD após primeira run.
- Zero ação do usuário pra migrar dados existentes da v0.1.0.
- `go install` produz binário utilizável.
- `ALTZAP_DATA_DIR` env override desbloqueia portable mode, testes E2E e dev contra dataset alternativo.

## Não-objetivos

- Sem rename `whatsapp.db` → `session.db` (mantém diff focado).
- Sem split DATA/CACHE — mídia continua sendo DATA (URL do WhatsApp expira em ~7 dias; após download, perdê-la = perda permanente).
- Sem suporte macOS/Windows nesta etapa (README documenta "Linux only"). Helper é fácil de estender depois.
- Sem mudança no bind do Hyprland do desenvolvedor — é config externa ao repo.

## Layout final

```
~/.config/altzap/             (já existia)
├── theme
├── settings.json
└── audio.json

~/.local/share/altzap/        (novo, criado no startup)
├── whatsapp.db               (whatsmeow session)
├── messages.db               (era ./store/messages.db — flattened)
├── messages.db-shm
├── messages.db-wal
├── .legacy/                  (preservado de ./store/.legacy/)
└── media/
    ├── <chat-jid>/
    │   └── <msg-id>.<ext>
    └── avatars/
        └── <jid>.png
```

**Decisão de flatten**: o namespace `~/.local/share/altzap/` já isola; manter um `store/` intermediário seria redundância. `media/` permanece como subdir só pra agrupar binários grandes (245 MB no caso do dev, podendo crescer).

## Componentes

| Arquivo | Mudança |
|---------|---------|
| `client/paths.go` | **NEW** — `AppDataDir()`, `AppConfigDir()`, `MediaDir()` helpers + `ALTZAP_DATA_DIR` override |
| `client/migrate_xdg.go` | **NEW** — one-shot CWD→XDG migration |
| `client/paths_test.go` | **NEW** — testes de resolução XDG |
| `client/migrate_xdg_test.go` | **NEW** — testes da migração |
| `client/media.go` | `const mediaDir = "media"` removido; chamadas usam `MediaDir()` |
| `client/avatars.go` | usa `MediaDir()` em vez de `mediaDir` |
| `client/webp_render.go` | `PreTranscodeWebPMedia` usa `MediaDir()` |
| `client/migrate.go` | `MigrateLegacyJSONLs(msgStore, dataDir)` em vez de literal `"store"` |
| `main.go` | substitui literais `whatsapp.db` / `store/messages.db` / `store` |
| `README.md` | seção Configuration documenta paths XDG |

## API

```go
// client/paths.go

// AppDataDir returns the canonical data directory for AltZap, creating it
// (with 0o755) if missing. Resolution order:
//   1. ALTZAP_DATA_DIR env var if set
//   2. $XDG_DATA_HOME/altzap if XDG_DATA_HOME is set
//   3. ~/.local/share/altzap
// Fatal if home dir cannot be resolved.
func AppDataDir() string

// AppConfigDir returns ~/.config/altzap (honors XDG_CONFIG_HOME via
// os.UserConfigDir). Creates if missing.
func AppConfigDir() string

// MediaDir returns AppDataDir() + "/media".
func MediaDir() string
```

Helpers retornam string (não erro): falhas em resolver são fatais — sem data dir, app não roda. `log.Fatalf` no startup é a UX correta.

## Migration

Em `client/migrate_xdg.go`, função `MigrateCWDToXDG()` chamada em `main.go` **antes** de qualquer `sqlstore.New` ou `OpenMessageStore`:

```
1. dataDir := AppDataDir()                       (cria se não existir)

2. Migrar arquivos top-level do CWD:
     ./whatsapp.db, ./whatsapp.db-shm, ./whatsapp.db-wal  →  dataDir/<same-name>
     ./media (diretório)                                  →  dataDir/media

3. Drenar todo o conteúdo de ./store/ pra dataDir/:
     Itera ./store/* e move cada entrada pra dataDir/<same-name>.
     Cobre:
       - messages.db (+ -shm, -wal sidecars)              → flatten p/ raiz
       - .legacy/ (subdir com JSONLs antigos)             → preserved
       - msg_*.json órfãos (encontrados após v0.1 ship)   → consumidos por
                                                            MigrateLegacyJSONLs
                                                            que roda depois

4. Para cada movimento:
     a. Se targetPath existe → skip silenciosamente (idempotente).
     b. Se legacyPath não existe → skip silenciosamente.
     c. os.Rename(legacy, target).
     d. Se erro for syscall.EXDEV (cross-device) → copy+verify+remove.
     e. Outros erros → return error (não procede com startup).

5. Log informativo por item migrado: "migrated <name> → <dataDir>/<name>".

6. Cleanup: se ./store/ virou vazio após drenar, os.Remove (ignora erro).
   ./media já moveu como diretório no passo 2.
```

A ordem importa: passo 3 deixa orphan `msg_*.json` em `dataDir/` (raiz), e em seguida `main.go` chama `MigrateLegacyJSONLs(msgStore, dataDir)` que escaneia esse mesmo dir e consome os JSONLs (movendo pra `dataDir/.legacy/`). Pipeline natural.

Best-effort em sidecars (`-shm`, `-wal`) significa "tenta mas não falha o startup se erro" — SQLite recria no open.

### Cross-device fallback

Quando `os.Rename` retorna `EXDEV` (legacy em filesystem diferente do data dir — ex: home em ZFS, CWD em tmpfs):

```
1. Para arquivo: os.Open(legacy), os.Create(target+".tmp"), io.Copy.
   Verify file size. os.Rename(target+".tmp", target). os.Remove(legacy).
2. Para diretório (./media com 245MB): filepath.WalkDir + recriar estrutura.
   Mais complexo; só roda se EXDEV ocorrer. Em prática raríssimo.
3. Se qualquer cópia falhar no meio: para, log error, não remove legacy,
   não procede com app startup. Estado: ambos paths têm cópias parciais
   ou o target tem .tmp; user pode investigar manualmente.
```

## Testing

`client/paths_test.go`:
- `AppDataDir()` respeita `ALTZAP_DATA_DIR`.
- `AppDataDir()` respeita `XDG_DATA_HOME`.
- `AppDataDir()` cai pro default `~/.local/share/altzap` quando nenhum env set.
- Diretório é criado se não existe.

`client/migrate_xdg_test.go`:
- **fresh install**: CWD vazio + dataDir vazio → no-op, retorna nil.
- **full legacy**: CWD com whatsapp.db + store/messages.db + media/ + dataDir vazio → todos movidos, originais sumiram, targets existem com bytes corretos.
- **partial legacy**: só `./media` no CWD → só ele move.
- **already migrated**: dataDir tem whatsapp.db + CWD tem whatsapp.db → CWD intocado (skip por target existir).
- **store drain com órfãos**: `./store/messages.db` + `./store/.legacy/` + `./store/msg_X.json` → todos os 3 chegam em dataDir/ (último na raiz, prontíssimo pra `MigrateLegacyJSONLs` consumir em seguida).
- **cross-device fallback**: simula via duas tmpdirs; cópia + verify size + remove. Skip elegante se não puder simular.

CI já roda `go test ./...`. Sem mudança no workflow.

## Error handling

| Cenário | Comportamento |
|---------|---------------|
| `os.MkdirAll` no data dir falha | `log.Fatalf` no startup. Sem data dir, sem app. |
| `os.Rename` cross-device falha | Cai pro copy fallback. |
| Copy fallback falha mid-stream | Para, log error, não procede com app. Não deleta legacy. |
| Sidecar `-shm`/`-wal` falta | Best-effort; SQLite recria no open. |
| `./store/` não vazio após migração | Não remove (deixa o que tiver lá pro user inspecionar). |
| `ALTZAP_DATA_DIR` aponta pra path inválido | `log.Fatalf` com mensagem clara. |

## Plano de rollout

1. Implementação na branch `feat/xdg-paths`.
2. Testes locais: rodar com dados existentes, verificar migração, abrir app, ver mensagens/mídia chegando dos novos paths.
3. CI verde.
4. Merge → `main`. Tag `v0.2.0`.
5. Atualizar manualmente o bind do Hyprland do dev: revert do `cd ... && ./altzap` pro caminho absoluto direto (`/home/pedro/.claudio/whatsappalt/altzap`) — agora funciona porque os dados não dependem mais do CWD.

## Riscos

- **Cópia parcial num crash mid-migration**: mitigado por copy-then-rename atomic com `.tmp` + verify size. Diretório (media) é o mais sensível por tamanho; em prática só roda no cross-device, raríssimo.
- **Race com app já rodando**: migração no startup, antes de qualquer DB open. Se duas instâncias rodarem ao mesmo tempo no mesmo CWD, segunda vê target já migrado e segue. SQLite WAL aguenta concorrência.
- **User edita keybinds.conf antes de testar v0.2**: bind antigo (`cd ... && ./altzap`) continua funcionando após migração — o `cd` vira no-op útil, dados estão no XDG. Sem regressão.
