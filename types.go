package main

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Message struct {
	ID             string      `json:"id"`
	ConversationID string      `json:"conversationId"`
	DeviceID       string      `json:"deviceId"`
	DeviceName     string      `json:"deviceName"`
	DeviceType     string      `json:"deviceType"`
	Type           string      `json:"type"`
	Content        string      `json:"content"`
	Attachment     *Attachment `json:"attachment,omitempty"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
	DeletedAt      *time.Time  `json:"deletedAt,omitempty"`
}

type Attachment struct {
	URL        string `json:"url"`
	Name       string `json:"name"`
	StoredName string `json:"storedName"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
}

// Settings holds shared server-side policy. SoftDeleteUndoMinutes is how long a
// soft-deleted message can be restored (0 means immediate hard delete).
type Settings struct {
	SoftDeleteUndoMinutes int `json:"softDeleteUndoMinutes"`
}

const DefaultSoftDeleteUndoMinutes = 30

type AppState struct {
	Conversations    []Conversation `json:"conversations"`
	Messages         []Message      `json:"messages"`
	DeletedMessages  []Message      `json:"deletedMessages"`
	Settings         Settings       `json:"settings"`
}
