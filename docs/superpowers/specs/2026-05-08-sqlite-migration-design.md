# SQLite Migration — Mensagens fora do JSONL append-only

**Data**: 2026-05-08
**Status**: Aprovado — pendente plano de implementação

## Contexto

Hoje cada chat persiste em `store/msg_<jid>.json` (JSONL append-only). Append é barato, mas qualquer mutação (reação, receipt delivered/read/played, edit, delete-for-everyone, media-ready) chama `patchRecord`, que **reescreve o arquivo inteiro** (decode tudo → re-encode tudo → tmpfile → rename atomic). Custo é O(N) por evento, com `muStoreFile` global serializando todas as escritas. Para chats que crescem, fica O(N²) cumulativo na vida do chat.

Hoje invisível (chat maior tem 175KB). Em 6 meses de uso, com chats de 5–10MB, vira lag perceptível na chegada de cada receipt.

A solução: mover mensagens pra SQLite. Lookups e mutações ficam O(log N) via índice.

## Objetivos

- Eliminar a reescrita full-file em mutações (root cause do problema #1, #2, #4 do audit).
- Reduzir o boot-time scan da sidebar (#5 audit) substituindo `os.ReadDir + os.Stat + getLastMessagePreview` por uma única query agregada.
- Preservar 100% do comportamento de UI atual e dos dados já no disco.
- Manter o codebase enxuto — sem novo subpacote, mantendo o estilo "arquivo irmão" de `media.go`/`avatars.go`.

## Não-objetivos

- Não mexemos em `whatsapp.db` (banco da whatsmeow). Migração e tuning ficam no nosso `messages.db` separado.
- Não otimizamos os outros itens do audit (sidebar throttle, in-place splice, probe widget, etc) — fica pra PRs separados.
- Não introduzimos nova feature de busca em mensagens. O schema permite isso depois sem migração adicional.

## Schema

Arquivo: `store/messages.db` (separado do `whatsapp.db` da whatsmeow).

```sql
CREATE TABLE messages (
    chat_jid          TEXT    NOT NULL,
    id                TEXT    NOT NULL,
    sender_jid        TEXT    NOT NULL DEFAULT '',
    sender_name       TEXT    NOT NULL DEFAULT '',
    text              TEXT    NOT NULL DEFAULT '',
    ts                INTEGER NOT NULL,
    from_me           INTEGER NOT NULL DEFAULT 0,

    media_type        TEXT    NOT NULL DEFAULT '',
    media_path        TEXT    NOT NULL DEFAULT '',
    mimetype          TEXT    NOT NULL DEFAULT '',
    filename          TEXT    NOT NULL DEFAULT '',
    file_size         INTEGER NOT NULL DEFAULT 0,
    width             INTEGER NOT NULL DEFAULT 0,
    height            INTEGER NOT NULL DEFAULT 0,
    duration          INTEGER NOT NULL DEFAULT 0,
    thumb_b64         TEXT    NOT NULL DEFAULT '',

    reply_to_id            TEXT NOT NULL DEFAULT '',
    reply_to_sender_jid    TEXT NOT NULL DEFAULT '',
    reply_to_sender_name   TEXT NOT NULL DEFAULT '',
    reply_to_text          TEXT NOT NULL DEFAULT '',
    reply_to_media_type    TEXT NOT NULL DEFAULT '',

    reactions_json    TEXT    NOT NULL DEFAULT '[]',
    edited            INTEGER NOT NULL DEFAULT 0,
    edited_at         INTEGER NOT NULL DEFAULT 0,
    deleted           INTEGER NOT NULL DEFAULT 0,
    deleted_at        INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL DEFAULT '',

    PRIMARY KEY (chat_jid, id)
);

CREATE INDEX idx_messages_chat_ts ON messages(chat_jid, ts);
```

**Decisões**:
- `PRIMARY KEY (chat_jid, id)` — composta porque `id` não é globalmente único (duas chats podem ter o mesmo stanza id); a combinação sim. Dedup automático via `INSERT OR IGNORE`.
- `idx_messages_chat_ts` — toda query de leitura ordena por `ts` filtrando por `chat_jid`. Index cobre o uso dominante.
- `reactions_json` denormalizado — reações são 0–3 tipicamente, sempre lidas/escritas junto da mensagem. Tabela separada seria pureza relacional sem ganho.
- `NOT NULL DEFAULT ''/0` em opcionais — evita ponteiros no Go e simplifica scan.
- Sem FK pra outras tabelas — `chat_jid` é string opaca; whatsmeow gerencia sua própria visão de chats.

DSN: `file:store/messages.db?_journal_mode=WAL&_synchronous=NORMAL&_cache_size=-32000&_busy_timeout=5000&_foreign_keys=on`
- **WAL**: readers não bloqueiam writer; checkpoint async.
- **synchronous=NORMAL**: ainda safe contra crash do app (WAL guarantees), 2-3x mais rápido que FULL.
- **cache_size=-32000**: 32MB de page cache.
- **busy_timeout=5000**: tolera contenção sem retornar SQLITE_BUSY.

## Camada de acesso (`client/store.go`)

Mesmo package `client`, sem subpacote. Renomeamos `savedMessage` (unexported) → `SavedMessage` (exported) pra UI conseguir consumir via API pública. É a mesma struct, só capitalizada. Tipo `MessageStore`:

```go
type MessageStore struct { db *sql.DB }

func OpenMessageStore(path string) (*MessageStore, error)
func (s *MessageStore) Close() error

// Writes — INSERT OR IGNORE no PK composto torna tudo idempotente.
func (s *MessageStore) Insert(rec SavedMessage) error
func (s *MessageStore) InsertBatch(recs []SavedMessage) error  // 1 transação

// Mutação — load → mutate fn → if changed UPDATE. Mantém semântica do patchRecord
// atual. fn retorna true se a mutação deve ser persistida.
func (s *MessageStore) Patch(chatJID, msgID string, fn func(*SavedMessage) bool) error

// Reads
func (s *MessageStore) LoadChat(chatJID string) ([]SavedMessage, error)
func (s *MessageStore) ChatSummaries() ([]ChatSummary, error)
```

`ChatSummary` é struct com `(ChatJID, LastTimestamp, LastText, LastFromMe, LastSenderName, LastMediaType)`. Implementação usa window function:

```sql
SELECT chat_jid, ts AS last_ts, text, from_me, sender_name, media_type
FROM (
    SELECT chat_jid, ts, text, from_me, sender_name, media_type,
           ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY ts DESC) AS rn
    FROM messages
)
WHERE rn = 1
ORDER BY last_ts DESC;
```

Substitui o loop atual de `os.ReadDir + os.Stat + getLastMessagePreview` (1 query vs N file ops).

`*sql.DB` é thread-safe e já gerencia connection pool. Não precisamos do `muStoreFile` que existia.

## Migração

Em `main.go`, depois de `OpenMessageStore`:

```
1. SELECT COUNT(*) FROM messages
   → > 0: skip (já migrado)
   → = 0: prosseguir

2. os.ReadDir("store/") filtrando msg_*.json
3. Decodar todos os arquivos em []savedMessage
4. s.InsertBatch(all) — uma única transação
5. Se commit OK:
     mkdir store/.legacy/
     pra cada msg_*.json: os.Rename → store/.legacy/
6. Se commit falha:
     log error, JSONLs ficam intactos, próximo boot tenta de novo
```

Atomicidade total: tudo dentro de 1 tx. Sem estado intermediário inválido. JSONLs só somem (.legacy/) depois do commit bem-sucedido — backup natural caso algo dê errado em prod.

## Substituições

| Função atual (arquivo:linha) | Vira |
|---|---|
| `appendMessages` (`whatsmeow_client.go:746`) | `s.InsertBatch` |
| `persistOwn` (`whatsmeow_client.go:941`) | `s.Insert` (wrapper continua existindo, fino) |
| `persistIncoming` (`whatsmeow_client.go:667`) | `s.Insert` |
| `loadMessageIDs` (`whatsmeow_client.go:722`) | **deletada** — INSERT OR IGNORE faz o dedup |
| `patchRecord` (`media.go:171`) | `s.Patch` |
| `patchMediaPath` (`media.go:157`) | mantém wrapper, agora chama `s.Patch` |
| `loadMessagesFromDisk` (`chat_view.go:1282`) | UI chama `cv.waClient.LoadMessages(jid)` → `[]SavedMessage`; o mapping `SavedMessage→ui.Message` (incluindo lookup "You"/legacy parsing) fica em uma helper na UI |
| `getLastMessagePreview` (`chat_view.go:988`) | dado vem direto em `ChatSummaries()` — função sai |
| `loadChatList` scan de filesystem (`chat_view.go:917-946`) | `s.ChatSummaries()` |
| `muStoreFile` (`whatsmeow_client.go:177`) | **deletado** |

Estimativa: -150 linhas em arquivos existentes, +250 linhas novas em `store.go`. Saldo positivo em complexidade conceitual.

## Concorrência

- `*sql.DB` é thread-safe; pool de conexões interno.
- WAL: múltiplos readers simultâneos sem bloquear o writer.
- SQLite serializa writers internamente (1 writer por vez no journal). Nossas escritas são curtas → sem contenção observável.
- `MessageStore` não precisa de mutexes próprios.

## Testes (`client/store_test.go`)

1. **Migration roundtrip**: cria 3 JSONLs canônicos em `t.TempDir()`, executa migração, valida cada record via `LoadChat`. Inclui caso com reactions, replies, edited, deleted.
2. **Insert + LoadChat**: insere `savedMessage` com todos os 27 campos populados, recarrega, deep equal.
3. **InsertBatch**: 1000 records em 1 tx; verifica ordem ao recarregar.
4. **Patch idempotência**: insere, `Patch` retornando false → record intacto. Retornando true → mutação aplicada.
5. **Dedup**: `Insert` mesma chave `(chat_jid, id)` duas vezes → 1 row no DB.
6. **ChatSummaries**: 3 chats com 5/10/2 mensagens; valida ordem por `last_ts DESC` e payload da última.
7. **WAL concurrency**: 10 goroutines paralelas fazendo Insert + Patch; verify count final = expected, sem data race.

Verificação manual:
- Bootar app pré-migração; observar log "migrated N records". Confirmar `store/messages.db` criado e `store/.legacy/msg_*.json` movidos.
- Mandar/receber mensagens, fechar app, reabrir → tudo presente.
- Reagir/receber receipt → UI atualiza; `sqlite3 store/messages.db "SELECT status, reactions_json FROM messages WHERE id = ?"` confirma persistência.

## Rollback / Safety

- JSONLs ficam em `store/.legacy/` indefinidamente após migração — usuário pode inspecionar/restaurar manualmente.
- Migração é idempotente: re-rodar com tabela populada é no-op.
- Falha em migration → JSONLs intactos em `store/`, código antigo continuaria funcionando se revertêssemos. Mas: como apagamos `appendMessages/patchRecord`, não há "código antigo" — revert seria via git.
- Single point of failure: se `messages.db` corrompe (improvável com WAL + sync=NORMAL), usuário precisa restaurar de backup ou re-fazer login (HistorySync repopula). Mitigação: a `.legacy/` mantém histórico parcial até o user limpar.
