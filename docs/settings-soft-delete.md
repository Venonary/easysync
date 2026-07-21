# Task 3: Settings + Soft-Delete Undo Window

## Purpose

Deleting a message no longer always purges it immediately. A shared server setting controls how long deleted messages can be restored (default **30 minutes**). Within that window, any client can restore the message to the live conversation.

## Settings

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `softDeleteUndoMinutes` | int | `30` | Minutes after soft-delete during which restore is allowed. `0` = immediate hard delete. Max `10080` (7 days). |

Persisted in `data/store.json` under `settings`, shared by all devices.

### API

```http
GET  /api/settings
PUT  /api/settings
PATCH /api/settings

{ "softDeleteUndoMinutes": 30 }
```

Also included in `GET /api/state` as `settings` and `deletedMessages`.

## Soft-delete & restore

```http
DELETE /api/messages/{id}
POST   /api/messages/{id}/restore
```

Behavior:

1. **Soft delete** (`softDeleteUndoMinutes > 0`): sets `deletedAt`, keeps content/attachments, removes from live `messages`, appears in `deletedMessages`.
2. **Hard delete** (`softDeleteUndoMinutes == 0`): removes the message immediately and deletes attachment files.
3. **Restore** within the window: clears `deletedAt`, returns the message to the live list.
4. **After the window**: restore fails; message is purged (attachments cleaned when purge runs).

### WebSocket events

| Type | Meaning |
|------|---------|
| `message_deleted` | `{ id, soft, deletedAt }` — clients drop from live list |
| `message_restored` | full message — clients re-insert |
| `settings` | settings object after update |

## UI

- **设备与设置** panel → **消息设置**: edit undo minutes and save.
- **最近删除** list with **还原**.
- After delete, a short **撤销** banner for the last deleted id.
- Confirm dialog mentions the current undo window.

## Tests

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./... -run 'SoftDelete|Settings|ConversationAndMessage|FileMessage' -count=1
```

Coverage includes: soft-delete leaves live list, restore within window, restore after expiry denied + purge, settings validation, hard-delete when minutes = 0.

## Rollback

```bash
git revert <this-task-commit>
```
