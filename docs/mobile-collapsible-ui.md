# Task 1: Mobile / Collapsible UI

## Purpose

Improve portrait phone and narrow-viewport use of EasySync so the message list is the main vertical region, instead of being squeezed into a thin strip by always-visible side panels and full-width action buttons.

## What changed

### Collapsible chrome

- **Session list (sidebar)** and **device/settings panel** open as drawers on narrow screens.
- Top bar has toggle buttons:
  - `☰` session list
  - `⚙` device & settings
- Backdrop tap or close (`×`) dismisses drawers.
- Selecting a conversation on a phone closes the session drawer so the chat reclaims the screen.

### Portrait packing

At `max-width: 720px` (and tighter short-height rules):

| Region | Behavior |
|--------|----------|
| Shell | Single-column workspace only; side panels off-canvas |
| Top bar | Compact single row; secondary rename/delete text buttons hidden (still available via title / session list) |
| Composer actions | 2-column grid (image / file / paste / send) instead of one full-width button per row |
| Textarea | Shorter min-height; safe-area padding for notched phones |
| Message list | Flexes to fill remaining height (`minmax(0, 1fr)` + `100dvh`) |

At `max-width: 980px`, the device panel is already a right drawer so medium tablets keep the session column but still free vertical space for chat.

### Layout height model

```
┌─────────────────────────────┐
│ compact topbar (~48px)      │
├─────────────────────────────┤
│                             │
│ message-list (flex grow)    │
│                             │
├─────────────────────────────┤
│ compact composer            │
│ [img] [file]                │
│ [paste] [send]              │
└─────────────────────────────┘
```

## Files

- `frontend/src/App.vue` — drawer state, toggles, conversation select closes drawer
- `frontend/src/styles.css` — breakpoints, drawers, multi-column composer, compact chrome
- Rebuild `frontend/dist` after source changes so the embedded Go binary serves the update

## Rebuild frontend

```powershell
npm.cmd --prefix frontend install
npm.cmd --prefix frontend run build
```

## Manual check (phone or DevTools)

1. Open the app at ~390×844 (iPhone-like).
2. Confirm message list is taller than topbar + composer combined.
3. Tap `☰` / `⚙` and confirm drawers open/close without covering the chat permanently.
4. Confirm composer shows two columns of actions, not four stacked full-width rows.

## Rollback

```bash
git revert <this-task-commit>
```
