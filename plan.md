# AltZap — Plano de melhorias (funcionalidade, UX, performance)

> **Para agentes executores (Opus/Sonnet):** cada tarefa abaixo é auto-contida — implemente UMA tarefa por vez, com commit próprio (conventional commit em pt-BR, ex.: `feat(ui): ...`). Antes de commitar, os três gates devem passar: `gofmt -l .` (saída vazia), `go vet ./...`, `go test ./...`. Não misture refactor com feature no mesmo commit. Se a tarefa tiver lógica pura nova, escreva teste de tabela (padrão dos `*_test.go` existentes).

**Gerado em:** 2026-06-09, a partir de leitura completa da codebase (commit `e6a9fcd` + WIP não-commitado).

## Status de execução (atualizado 2026-06-09)

**Concluídas** (uma por commit, gates verdes):

| Tarefa | Commit | |
|--------|--------|---|
| 0 — WIP de memória/foco commitado | `7109b4c` | ✅ |
| A1 — truncate por runas | `bef5156` | ✅ |
| A2 — insertEmoji por runas | `e9aa25e` | ✅ |
| A4 — log de erro no HistorySync | `c39c60d` | ✅ |
| A3 — notificação com janela na tray | `88fb057` | ✅ |
| B1 — read receipts (MarkRead) | `68ec52c` | ✅ |
| B9 — 🕓 até o ACK do envio | `66b71ee` | ✅ |
| C2 — debounce de reload + cache de settings | `2ce7e3a` + `dbad3f4` | ✅ |
| B5 — pin/mute do celular | `dbad3f4` | ✅ |
| B2 — composer multi-linha | `0cd8156` | ✅ |
| B6 — archive writeback | `844ba75` | ✅ |
| D8 — auto-QR no login | `bbd139c` | ✅ |
| B8 — chat por número | `5dda973` | ✅ |

**Pendentes**, em ordem sugerida: C4 (split do chat_view.go — fazer antes de B3), B3a/b/c (menu de contexto + editar + apagar), B4 (typing), C5 (coalesce refreshMessages), C3 (decode assíncrono), C1 (paginação), B7 (vídeo), e o restante do Milestone D.

> Validação manual recomendada pós-B1/B5/B6 (precisa de sessão real): badge do celular limpa ao abrir chat com não-lidas; fixados no topo; mutado não notifica; arquivar pelo menu ⋯ reflete no celular; Shift+Enter no composer; "💬 Conversar com +número" no novo chat.

---

## 0. Estado atual (snapshot)

- ~13k linhas de Go. Núcleo: `ui/chat_view.go` (2769 linhas) e `client/whatsmeow_client.go` (1752 linhas).
- Funciona como daily driver: texto, mídia (receber tudo; enviar imagem/áudio/doc/voz), replies, reações, edits/revokes **recebidos**, receipts recebidos, notificações, tray, single-instance, atalhos Ctrl+J/K/L/F/R, busca de chats e busca in-chat, arquivadas (read-only), temas Catppuccin hot-swap.
- **WIP não-commitado no working tree** (trabalho de memória/perf): LRU no image cache, tuning de cache SQLite, pprof opt-in, `RestoreKeyboardFocus`, refactor de `filterChatsByQuery` + 3 testes novos não rastreados (`client/image_cache_test.go`, `ui/chat_focus_test.go`, `ui/chat_search_test.go`). **Tarefa implícita nº 0: revisar e commitar esse WIP antes de qualquer outra coisa** (ou rebasear por cima dele; não sobrescrever).

### Convenções e armadilhas da codebase (leitura obrigatória)

1. **Threading Fyne:** callbacks do `client` chegam em goroutines do whatsmeow. Qualquer toque em widget fora da UI thread → `fyne.Do(...)`. Padrão em todos os `OnXxx` de `NewChatView`.
2. **Locks:** nunca segurar `muMessages`/`muCachedChats` atravessando IO (ver `appendStoredMessage`, que pré-carrega fora do lock). Não aninhar locks.
3. **LID/PN:** todo contato pode ter dois JIDs (`@lid` e `@s.whatsapp.net`). Nunca usar `jid.User` cru — sempre `LookupName`/`PhoneForJID`. Qualquer mutação em mensagens precisa atualizar o chat **e** o sibling (`cv.isSibling`, padrão repetido nos callbacks `OnMediaReady`/`OnReactionUpdate`/etc.).
4. **widget.List:** altura por linha vem do cache `bubbleHeights` + `SetItemHeight`. Mudou a altura de uma bolha existente (borda, linha extra) → `invalidateBubbleHeight(id)` antes do refresh.
5. **Atalhos:** registrar no canvas **e** espelhar nas entries via `AddCustomShortcut` (Fyne entrega shortcut exclusivamente ao widget focado). Ver `installShortcuts`.
6. **Overlays:** todo dismissível usa a pilha de ESC (`pushEsc(dismiss)`); sempre guardar e invocar o pop no fechamento.
7. **UpdateItem das listas faz traversal posicional** (`Objects[0].(*...)`) do template — mudou a estrutura do template, atualize o traversal em conjunto.
8. **Envio otimista:** `GenerateMessageID()` antes do send; `AddMessage` dedupa pelo ID quando o eco do servidor chega.
9. Wrapper Mouseable externo é suprimido por filhos Tappable (quirk de hit-test DFS do Fyne) — wrap perto da folha (ver `newClickableBubble`).

---

## Milestone A — Correções (bugs reais, esforço pequeno)

### A1. `truncate()` corta UTF-8 no meio ✂️🐛
- **Onde:** `ui/chat_view.go:520` (`return s[:n] + "…"`).
- **Problema:** fatia por **bytes**. Com texto pt-BR ("ç", "ã", emoji) o corte pode cair no meio de uma sequência UTF-8 → caractere inválido em previews da sidebar (`truncate(preview, 60)`) e notificações (`truncate(body, 200)`).
- **Fix:** truncar por runas (`[]rune(s)` ou iterar `utf8.DecodeRuneInString`). Teste de tabela com "ação", emoji e ASCII puro nas bordas.
- **Esforço:** XS.

### A2. `insertEmoji` mistura índice de runas com offset de bytes
- **Onde:** `ui/emoji.go:88–104`. `Entry.CursorColumn` é índice de **runas**; o código usa como offset de **bytes** (`text[:pos]`).
- **Sintoma:** com texto multibyte antes do cursor, o emoji entra na posição errada ou corrompe a string.
- **Fix:** converter via `[]rune(text)`; cursor final = `pos + utf8.RuneCountInString(s)`. Teste cobrindo cursor após "ç"/emoji.
- **Esforço:** XS.

### A3. Mensagem silenciosa quando a janela está oculta com o chat aberto
- **Onde:** `ui/chat_view.go` `AddMessage` (`onScreen := jidStr == cur || isSibling`) + `main.go`.
- **Problema:** janela escondida na tray com o chat X "aberto" → mensagem de X não gera notificação **nem** unread (o `onScreen` é true mas ninguém está vendo).
- **Fix:** rastrear visibilidade da janela em `main.go` (flag setada no `SetCloseIntercept`/`window.Hide()`, limpa no `reshow`) e injetar na ChatView (`cv.SetWindowVisibleFn(func() bool)`). `onScreen = (chat aberto) && janelaVisível()`. Ao re-mostrar a janela com o chat aberto, zerar o unread desse chat.
- **Esforço:** S. **Pré-requisito do B1** (mesma noção de visibilidade).

### A4. Erros engolidos
- `client/whatsmeow_client.go:953` — `w.msgStore.InsertBatch(batch)` no `handleHistorySync` ignora o erro: logar.
- **Esforço:** XS (pode ir junto com A1/A2 num commit `fix:` único... não — um commit por item; A4 pode ser `fix(client): log de erro no handleHistorySync`).

---

## Milestone B — Paridade daily-driver (funcionalidade)

### B1. Recibos de leitura (`MarkRead`) — **o maior gap do app hoje** 🎯
- **Problema:** o app **nunca** envia read receipt. Tudo que Pedro lê no AltZap continua "não lido" no celular (badge não limpa) e o remetente nunca vê tick azul.
- **API:** `cli.MarkRead(ctx, ids, time.Now(), chat, sender)` (`receipt.go:194` do whatsmeow). Em grupo, `sender` é obrigatório e os IDs devem ser agrupados **por remetente** (uma chamada por remetente). A privacidade já é respeitada automaticamente: se a conta desativou confirmação de leitura, a lib troca para `read-self` (badge do celular limpa mesmo assim, sem tick azul para o outro lado).
- **Implementação:**
  1. `client`: novo método `MarkRead(chatJID string, msgs []MarkTarget) error` que parseia JIDs, agrupa por sender e chama `cli.MarkRead` em lotes. `MarkTarget{ID, SenderJID}`.
  2. `ui`: manter por chat o conjunto de IDs incoming ainda não marcados (alimentado em `AddMessage`; ao abrir um chat, os IDs contados no `unread`). Disparar marcação quando (a) chat aberto e janela visível recebe mensagem, (b) chat é selecionado, (c) janela volta a ficar visível com chat aberto. Sempre em goroutine (rede!), nunca na UI thread.
  3. Bônus: tratar `events.MarkChatAsRead` (`types/events/appstate.go:95`) — quando Pedro lê no **celular**, zerar o unread local do chat correspondente.
- **Critérios de aceite:** abrir um chat com não-lidas → badge do celular limpa em segundos; mensagens recebidas com o chat aberto+visível são marcadas; nenhum `MarkRead` dispara com janela oculta.
- **Dependências:** A3. **Esforço:** M.

### B2. Composer multi-linha (Shift+Enter)
- **Problema:** `ui/paste_aware_entry.go:60` força `MultiLine = false` — impossível mandar mensagem com quebra de linha (gap real de daily driver).
- **Implementação:** `MultiLine = true` + interceptar `TypedKey`: Enter → `sendMessage()`; Shift+Enter → quebra de linha (Fyne: tratar `KeyReturn`/`KeyEnter` no `pasteAwareEntry`, com desktop.Modifier consultável via shortcut/estado — alternativa robusta: campo `shiftHeld` atualizado em `KeyDown`/`KeyUp` via `desktop.Keyable`). Limitar altura visível (ex.: `SetMinRowsVisible`/wrapper com máx ~5 linhas).
- **Atenção:** o probe de largura em `buildMessageBubble` usa `canvas.NewText` (single-line). Com `\n` no texto, medir a **linha mais larga** (split em `\n`, max das larguras), senão a bolha sai com largura errada — isso já melhora também a renderização de mensagens multi-linha **recebidas** hoje.
- **Critérios:** Shift+Enter insere quebra; Enter envia; `insertEmoji` (pós-A2) continua correto com `CursorRow`>0; bolhas multi-linha com largura correta.
- **Esforço:** M.

### B3. Menu de contexto por bolha (clique direito) + editar + apagar p/ todos
Dividir em três commits:
- **B3a — infra do menu:** wrapper secondary-tap (ver `newClickableBubble` em `ui/text_links.go` e o quirk nº 9) com itens: **Copiar**, **Responder**, **Reagir…** (reusa `showReactionPickerFor`), e para mensagens próprias: **Editar**, **Apagar para todos**. `widget.NewPopUpMenu` posicionado no cursor.
- **B3b — editar mensagem própria:** `client.EditMessage(chat types.JID, id, newText string)` usando `cli.BuildEdit(chat, id, &waE2E.Message{Conversation: ...})` + `SendMessage`. Patch otimista local (`patchRecord` + UI) — o fluxo `handleEdit` existente já cobre o eco. UI: prompt `dialog.NewEntryDialog` pré-preenchido (ou entry inline). Só para `IsOwn && MediaType==""` (e captions, se trivial).
- **B3c — apagar para todos:** `client.RevokeMessage(chat, sender types.JID, id)` com `cli.BuildRevoke` + `SendMessage`; confirmação antes (`dialog.NewConfirm`). `handleRevoke` existente cobre o eco; fazer patch otimista também.
- **Critérios:** editar reflete no celular com "editada"; apagar vira "🚫 deleted" nos dois lados; menu não rouba o double-click-reply existente.
- **Esforço:** B3a M, B3b S, B3c S.

### B4. Indicador "digitando…" (receber e enviar)
- **Receber:** handler para `*events.ChatPresence` → se for o chat aberto, `chatSubtitle.SetText("digitando…")` (ou "gravando áudio…" para media=audio) com revert ao subtitle anterior após ~10s sem evento ou ao receber `paused`. Cuidado com o ticker de gravação que também usa o subtitle (`lastSubtitle`).
- **Enviar:** `cli.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)` no primeiro `OnChanged` do composer, re-armado com debounce; `ChatPresencePaused` após ~5s parado, ao enviar e ao trocar de chat. Best-effort: erros só logados.
- **Esforço:** M.

### B5. Respeitar Pin e Mute do celular (dados já sincronizados!)
- **Fato:** `IsChatArchived` já lê `LocalChatSettings` do store do whatsmeow — a mesma struct traz `Pinned bool` e `MutedUntil time.Time`. Custo marginal zero de protocolo.
- **Implementação:**
  1. `client`: trocar `IsChatArchived` por `ChatSettings(jid) (archived, pinned bool, mutedUntil time.Time)` (uma chamada só, menos round-trips — ver C2).
  2. `loadChatList`: após o sort por recência, partição estável com fixados primeiro; ícone 📌 na row (canvas.Text pequeno no topRow).
  3. Notificações: se `mutedUntil` futuro → não chamar `notifyHook` (unread continua contando); ícone 🔕 na row.
  4. Live-sync: handlers para `*events.Pin`/`*events.Mute`/`*events.Archive` → invalidar cache + `loadChatList`.
- **Esforço:** M.

### B6. Arquivar/desarquivar a partir do AltZap (v2 do archive — único item restante do roadmap v0.3)
- **API:** `appstate.BuildArchive(target, archive, lastMessageTimestamp, lastMessageKey)` + `cli.SendAppState(ctx, patch)`.
- **Implementação:** `client.SetChatArchived(jid types.JID, archived bool, lastTS time.Time) error`; UI: item "Arquivar"/"Desarquivar" no menu ⋯ do header (`showChatMenu`) e no futuro menu de contexto da sidebar. Update otimista dos buckets + reconciliação via `events.Archive` (B5.4).
- **Critérios:** arquivar no AltZap reflete no celular e vice-versa.
- **Esforço:** S–M (depois de B5).

### B7. Enviar vídeo
- **Problema:** attach menu só tem Image/Audio/Document; `whatsmeow.MediaVideo` não é usado.
- **Implementação:** `client.SendVideo(jid, path, caption)` espelhando `SendImage` (upload `MediaVideo`, `waE2E.VideoMessage` com `Seconds`, `Width/Height`, `JPEGThumbnail`). Thumbnail/dimensões/duração via ffmpeg/ffprobe (já é dependência soft p/ voz): `ffmpeg -ss 0.5 -i in -frames:v 1 -f image2 -` para o frame, `ffprobe -show_streams` p/ metadata; sem ffmpeg → enviar sem thumb (degradação graciosa). Item "Video" no `showAttachMenu` com filtro `.mp4 .mkv .webm .mov`; preview de confirmação com caption (reusar o dialog do `confirmAndSendImage` generalizado).
- **Esforço:** M.

### B8. Novo chat por número de telefone
- **Problema:** `onNewChat` só lista contatos existentes — impossível iniciar conversa com número novo.
- **Implementação:** no dialog de novo chat, se a query for numérica (≥8 dígitos, strip de `+ - ( ) espaço`), mostrar linha extra "💬 Conversar com +<número>"; ao confirmar, `cli.IsOnWhatsApp([]string{num})` para validar/resolver JID e `selectChatJID`. Erro amigável se não existir.
- **Esforço:** S.

### B9. Estado "enviando" (🕓) no envio otimista
- **Problema:** a bolha otimista já nasce com "✓" antes do ACK do servidor — informação falsa quando a rede está ruim.
- **Implementação:** `Status: "pending"` no `newMsg` otimista; em `buildMessageBubble`, case `"pending"` → "🕓 "; quando `SendMessage` retorna OK, patch para `""` (→ "✓") + `refreshMessages` (o rollback de erro já existe). Aplicar também em `appendStoredMessage`? Não — anexos só aparecem após o send retornar; basta no `sendMessage` de texto.
- **Esforço:** S.

---

## Milestone C — Performance e arquitetura

### C1. Paginação real de mensagens (DB-backed)
- **Problema:** `LoadChat` faz `SELECT ... WHERE chat_jid = ?` **sem LIMIT** — o histórico inteiro de cada chat aberto vai pra RAM e fica no map `cv.messages` para sempre. `renderLimit` só limita a *renderização*. Chats grandes (10k+ msgs) pagam parse+RAM no clique.
- **Implementação:**
  1. `store`: `LoadChatTail(chatJID string, limit int, beforeTS int64) ([]SavedMessage, error)` (`ORDER BY ts DESC, rowid DESC LIMIT ?` + reverse; `beforeTS` para páginas antigas). Manter `LoadChat` para migração/compat.
  2. `ui`: `loadMessagesFromDisk` carrega tail (ex.: 300); "Load older" passa a buscar do DB quando o in-memory acabar (hoje só expande janela de render). Merge de siblings: tail de cada um, merge+sort (já existe a lógica).
  3. `scrollToMessageByID` (tap no quote): se o ID não está carregado, puxar mais páginas até achar (com teto) — hoje já é no-op silencioso, manter como fallback.
  4. Busca in-chat: documentar que busca só o carregado, ou (melhor) buscar no DB via `LIKE` — decidir no PR.
  5. Eviction: ao trocar de chat, descartar `cv.messages[jid]` de chats não-abertos além dos N mais recentes (e o `bubbleHeights` correspondente — hoje cresce sem limite).
- **Critérios:** abrir chat gigante < 100ms; RAM estável navegando por muitos chats; reply-jump e search continuam funcionando.
- **Esforço:** L. **Maior alavanca de performance da lista.**

### C2. Debounce do `loadChatList` + cache de chat settings
- **Problema:** todo `AddMessage` dispara `go cv.loadChatList()` (rajadas de N goroutines em sync), e cada `loadChatList` chama `IsChatArchived` **por chat** = 1 query SQL × N chats × cada mensagem recebida. Com B5 seriam 1–3 queries por chat.
- **Implementação:** (1) coalescer chamadas num timer estilo `scheduleContactRefresh` (250–500ms); (2) cache em memória `map[jid]chatSettings` preenchido no primeiro load e invalidado pelos eventos `Pin/Mute/Archive` (B5.4) — aí o custo por reload vira zero queries no caminho comum.
- **Esforço:** S–M. Fazer junto ou logo após B5.

### C3. Decode + downscale assíncrono de imagens
- **Problema:** o primeiro render de cada imagem decodifica **na UI thread** (`CachedImage` dentro de `messageListUpdate`) — foto de 12MP = 100–300ms de jank ao rolar. E o cache LRU (WIP) guarda a imagem **inteira** decodificada (12MP ≈ 48MB) para uma bolha de ≤320px.
- **Implementação:**
  1. `client`: `CachedImageScaled(path string, maxDim int) image.Image` — decodifica e reduz (golang.org/x/image/draw, `ApproxBiLinear`) para ≤640px (2× da bolha p/ hidpi) antes de entrar no LRU → 10–50× menos bytes por entrada, upload de textura mais rápido.
  2. `ui`: `imageContent` consulta o cache; em miss, renderiza o thumb embutido (já existe) e dispara decode em worker; ao terminar, `fyne.Do(refreshMessages-coalescido)` (ver C5). Fullscreen continua usando a imagem original.
- **Critérios:** rolar um chat cheio de fotos sem stutter perceptível; RSS estável (medir com `ALTZAP_PPROF=1`).
- **Esforço:** M.

### C4. Quebrar `ui/chat_view.go` (2769 linhas) — refactor puro
- **Proposta:** mover blocos coesos, sem mudança de comportamento: `chat_sidebar.go` (buildChatList, loadChatList, search, arquivadas, onNewChat), `chat_messages.go` (messageList*, buildMessageBubble, scroll/refresh), `chat_reply.go` (reply + reply-mode), `chat_unread.go` (unread/notify/hooks). `ChatView` struct fica em `chat_view.go`.
- **Por quê agora:** tarefas B1–B9 tocam regiões distintas — arquivos menores = menos conflito entre agentes trabalhando em paralelo.
- **Regra:** commit único `refactor(ui):`, zero diffs semânticos (`go build` + testes idênticos antes/depois).
- **Esforço:** M (mecânico). Pode ser feito a qualquer momento; ideal antes de B3.

### C5. Coalescer `refreshMessages`
- **Problema:** rajada de receipts (`handleReceipt` itera IDs) dispara um `refreshMessages` **por mensagem** — cada um rebuilda ~10 bolhas visíveis.
- **Implementação:** trocar chamadas diretas por `cv.requestRefresh()` que agenda um único refresh via `time.AfterFunc(50ms)` (não-reentrante, na UI thread). Aplicar nos callbacks de status/reaction/edit/delete/media.
- **Esforço:** S.

---

## Milestone D — Polish de UX (ordem livre, escolher por gosto)

| # | Item | Descrição | Esforço |
|---|------|-----------|---------|
| D1 | **Formatação WhatsApp** | Renderizar `*negrito*`, `_itálico_`, `~tachado~`, `` ```mono``` `` via `widget.RichText` (parser puro + testes; cuidado com o probe de largura) | M |
| D2 | **Menções legíveis** | `@5511...` → `@Nome` via `ContextInfo.GetMentionedJID()` + `LookupName` no texto recebido | S–M |
| D3 | **Link preview** | `ExtendedTextMessage` já traz `Title/Description/JPEGThumbnail` das previews **sem precisar de fetch** — hoje é descartado em `extractText`. Persistir + card clicável na bolha | M |
| D4 | **Busca global de mensagens** | FTS5 (ou `LIKE` v1) em `messages.db` + dialog (Ctrl+Shift+F): resultado abre o chat e rola até a mensagem (usa C1) | M–L |
| D5 | **Pill "N novas mensagens"** | Quando scrolled-up e chega mensagem, botão flutuante "↓ 3 novas" → `scrollToLatest` | S–M |
| D6 | **Divisor "Não lidas"** | Linha "— N não lidas —" ao abrir chat com unread > 0 (mesma mecânica do dateSep) | S |
| D7 | **Progresso de áudio** | Ticker `0:07 / 0:32` durante playback (ffplay não reporta posição — cronometrar localmente); waveform do proto se vier | S–M |
| D8 | **Auto-QR no login** | Gerar QR automaticamente ao mostrar a tela (remove o clique em "Generate QR Code") | XS |
| D9 | **Logout nas settings** | Botão "Desconectar" → confirm → `cli.Logout()` → volta pra tela de QR; + versão do app no rodapé do dialog | S |
| D10 | **Overlay de atalhos** | Ctrl+? (ou item no menu ⋯) com a lista de atalhos | S |
| D11 | **Drag & drop** | `window.SetOnDropped` → roteia para o fluxo de confirmação de imagem/arquivo do chat aberto | S–M |
| D12 | **Location/Poll melhores** | Location → link OpenStreetMap clicável (lat/lng do proto); Poll → render read-only de pergunta+opções | S / M |
| D13 | **Avatar refresh** | Tratar `*events.Picture` → invalidar avatar em disco e re-fetch (hoje avatar antigo fica para sempre) | S |

---

## Ordem de execução recomendada

```
[✅ feitos] 0, A1–A4, B1, B9, C2, B5, B2, B6, D8, B8 — ver Status acima
C4 (split do chat_view.go)    ← próximo: destrava paralelismo e o B3
B3a → B3b → B3c (menu de contexto / editar / apagar p/ todos)
B4 (typing) → C5 (coalesce refreshMessages)
C3 (decode assíncrono) → C1 (paginação SQL)
B7 (vídeo) e Milestone D conforme apetite
```

**Notas para o fluxo multi-agente:** um agente implementa, outro revisa (commits/revisões auto-contidos). Tarefas em `‖` não compartilham arquivos após C4. Specs/planos detalhados de tarefas L (C1, D4) podem ganhar um doc próprio em `docs/superpowers/specs/` antes da implementação, seguindo o padrão dos existentes.
