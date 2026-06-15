package main

import "testing"

func TestStoreConversationAndMessageFlow(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	state := store.State()
	if len(state.Conversations) != 1 {
		t.Fatalf("expected default conversation, got %d", len(state.Conversations))
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

	if _, err := store.DeleteMessage(message.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range store.State().Messages {
		if item.ID == message.ID {
			t.Fatal("expected message to be deleted")
		}
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

	deleted, err := store.DeleteMessage(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Attachment == nil || deleted.Attachment.StoredName != "stored-photo.jpg" {
		t.Fatalf("deleted message should include attachment metadata: %#v", deleted)
	}
}
