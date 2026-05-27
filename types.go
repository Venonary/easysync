package main

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	DeviceID       string    `json:"deviceId"`
	DeviceName     string    `json:"deviceName"`
	DeviceType     string    `json:"deviceType"`
	Type           string    `json:"type"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type AppState struct {
	Conversations []Conversation `json:"conversations"`
	Messages      []Message      `json:"messages"`
}
