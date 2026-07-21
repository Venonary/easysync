package main

import (
	"testing"
	"time"
)

func TestStoreConversationAndMessageFlow(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	state := store.State()
	if len(state.Conversations) != 1 {
		t.Fatalf("expected default conversation, got %d", len(state.Conversations))
	}
	if state.Settings.SoftDeleteUndoMinutes != DefaultSoftDeleteUndoMinutes {
		t.Fatalf("expected default undo minutes %d, got %d", DefaultSoftDeleteUndoMinutes, state.Settings.SoftDeleteUndoMinutes)
	}

	conversation, err := store.CreateConversation("测试会话")
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "测试会话" {
		t.Fatalf("unexpected title: %s", conversation.Title)
	}

	message, err := store.AddMessage(Message{
		ConversationID: conversation.ID,
		DeviceID:       "device-1",
		DeviceName:     "Windows 设备",
		DeviceType:     "Windows",
		Type:           "text",
		Content:        "hello easysync",
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.ID == "" || message.CreatedAt.IsZero() {
		t.Fatalf("message was not populated: %#v", message)
	}
	if message.UpdatedAt.IsZero() {
		t.Fatalf("message updatedAt was not populated: %#v", message)
	}

	updated, err := store.UpdateMessage(message.ID, "device-1", "edited text")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "edited text" {
		t.Fatalf("unexpected content after update: %s", updated.Content)
	}
	if updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("updatedAt should not be before createdAt: %#v", updated)
	}

	if _, err := store.UpdateMessage(message.ID, "device-2", "hijack"); err == nil {
		t.Fatal("expected permission denied for non-owner update")
	}

	// Soft-delete by default: message leaves live list but remains restorable.
	if _, _, _, err := store.DeleteMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			t.Fatal("expected message to be absent from live list after soft delete")
		}
	}
	foundDeleted := false
	for _, item := range store.State().DeletedMessages {
		if item.ID == message.ID {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatal("expected soft-deleted message in DeletedMessages")
	}

	restored, _, err := store.RestoreMessage(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DeletedAt != nil || restored.Content != "edited text" {
		t.Fatalf("restore failed: %#v", restored)
	}

	// Hard-delete when undo window is 0.
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 0}); err != nil {
		t.Fatal(err)
	}
	if _, removeAttachment, _, err := store.DeleteMessage(message.ID); err != nil {
		t.Fatal(err)
	} else if !removeAttachment {
		t.Fatal("expected hard delete when undo minutes is 0")
	}
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			t.Fatal("expected message to be hard-deleted")
		}
	}
	for _, item := range store.State().DeletedMessages {
		if item.ID == message.ID {
			t.Fatal("hard-deleted message should not appear in deleted list")
		}
	}

	// recreate a message for conversation cleanup path
	message, err = store.AddMessage(Message{
		ConversationID: conversation.ID,
		DeviceID:       "device-1",
		DeviceName:     "Windows 设备",
		DeviceType:     "Windows",
		Type:           "text",
		Content:        "to delete with conversation",
	})
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := store.RenameConversation(conversation.ID, "重命名")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "重命名" {
		t.Fatalf("rename failed: %s", renamed.Title)
	}

	if err := store.DeleteConversation(conversation.ID); err != nil {
		t.Fatal(err)
	}
	state = store.State()
	for _, item := range state.Messages {
		if item.ConversationID == conversation.ID {
			t.Fatal("messages for deleted conversation should be removed")
		}
	}
}

func TestStoreFileMessageFlow(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Force hard delete for attachment cleanup expectation of this older test path.
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 0}); err != nil {
		t.Fatal(err)
	}

	conversation := store.State().Conversations[0]
	message, err := store.AddMessage(Message{
		ConversationID: conversation.ID,
		DeviceID:       "device-1",
		DeviceName:     "iPhone",
		DeviceType:     "iOS",
		Type:           "image",
		Content:        "photo.jpg",
		Attachment: &Attachment{
			URL:        "/uploads/photo.jpg",
			Name:       "photo.jpg",
			StoredName: "stored-photo.jpg",
			MimeType:   "image/jpeg",
			Size:       2048,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Attachment == nil || message.Attachment.Name != "photo.jpg" {
		t.Fatalf("attachment was not persisted: %#v", message)
	}

	deleted, removeAttachment, _, err := store.DeleteMessage(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removeAttachment {
		t.Fatal("expected attachment removal flag for hard delete")
	}
	if deleted.Attachment == nil || deleted.Attachment.StoredName != "stored-photo.jpg" {
		t.Fatalf("deleted message should include attachment metadata: %#v", deleted)
	}
}

func TestSoftDeleteRestoreWindow(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 30}); err != nil {
		t.Fatal(err)
	}

	conversation := store.State().Conversations[0]
	message, err := store.AddMessage(Message{
		ConversationID: conversation.ID,
		DeviceID:       "device-1",
		DeviceName:     "Phone",
		DeviceType:     "Android",
		Type:           "text",
		Content:        "soft delete me",
	})
	if err != nil {
		t.Fatal(err)
	}

	deleted, removeAttachment, _, err := store.DeleteMessage(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removeAttachment {
		t.Fatal("soft delete should not request immediate attachment purge")
	}
	if deleted.DeletedAt == nil {
		t.Fatal("soft-deleted message must set deletedAt")
	}

	// Not in live list.
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			t.Fatal("soft-deleted message must leave live list")
		}
	}

	// Restore within window succeeds.
	restored, _, err := store.RestoreMessage(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DeletedAt != nil {
		t.Fatal("restored message must clear deletedAt")
	}
	found := false
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("restored message must return to live list")
	}

	// Soft-delete again, then expire the window by rewriting DeletedAt into the past.
	if _, _, _, err := store.DeleteMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	expired := time.Now().Add(-31 * time.Minute)
	for i := range store.Messages {
		if store.Messages[i].ID == message.ID {
			store.Messages[i].DeletedAt = &expired
		}
	}
	if err := store.saveLocked(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()

	if _, _, err := store.RestoreMessage(message.ID); err == nil {
		t.Fatal("expected restore after window to fail")
	}
	// After failed restore (or purge), message should be gone.
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			t.Fatal("expired message should not be live")
		}
	}
	for _, item := range store.State().DeletedMessages {
		if item.ID == message.ID {
			t.Fatal("expired message should be purged from deleted list")
		}
	}
}

func TestStateAndPurgeRemovesExpiredAttachmentNames(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 30}); err != nil {
		t.Fatal(err)
	}

	conversation := store.State().Conversations[0]
	message, err := store.AddMessage(Message{
		ConversationID: conversation.ID,
		DeviceID:       "device-1",
		DeviceName:     "Phone",
		DeviceType:     "Android",
		Type:           "image",
		Content:        "photo.jpg",
		Attachment: &Attachment{
			URL:        "/uploads/expired-photo.jpg",
			Name:       "photo.jpg",
			StoredName: "expired-photo.jpg",
			MimeType:   "image/jpeg",
			Size:       100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.DeleteMessage(message.ID); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	expired := time.Now().Add(-31 * time.Minute)
	for i := range store.Messages {
		if store.Messages[i].ID == message.ID {
			store.Messages[i].DeletedAt = &expired
		}
	}
	if err := store.saveLocked(); err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()

	_, purged := store.StateAndPurge()
	if len(purged) != 1 || purged[0] != "expired-photo.jpg" {
		t.Fatalf("expected purged attachment name, got %#v", purged)
	}
	// Second call should not re-report the same file.
	_, purgedAgain := store.StateAndPurge()
	if len(purgedAgain) != 0 {
		t.Fatalf("expected no further purge, got %#v", purgedAgain)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: -1}); err == nil {
		t.Fatal("expected negative undo minutes to fail")
	}
	if _, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 20000}); err == nil {
		t.Fatal("expected too-large undo minutes to fail")
	}
	got, err := store.UpdateSettings(Settings{SoftDeleteUndoMinutes: 45})
	if err != nil {
		t.Fatal(err)
	}
	if got.SoftDeleteUndoMinutes != 45 {
		t.Fatalf("settings not updated: %#v", got)
	}
	if store.GetSettings().SoftDeleteUndoMinutes != 45 {
		t.Fatal("GetSettings mismatch")
	}
}
