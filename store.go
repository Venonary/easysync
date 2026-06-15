package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu   sync.RWMutex
	path string

	Conversations []Conversation `json:"conversations"`
	Messages      []Message      `json:"messages"`
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	s := &Store{path: filepath.Join(dataDir, "store.json")}
	if err := s.load(); err != nil {
		return nil, err
	}
	if len(s.Conversations) == 0 {
		s.Conversations = append(s.Conversations, Conversation{
			ID:        newID(),
			Title:     "默认会话",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, s)
}

func (s *Store) State() AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conversations := append([]Conversation(nil), s.Conversations...)
	messages := append([]Message(nil), s.Messages...)
	sortConversations(conversations)
	sortMessages(messages)
	return AppState{Conversations: conversations, Messages: messages}
}

func (s *Store) ListConversations() []Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := append([]Conversation(nil), s.Conversations...)
	sortConversations(out)
	return out
}

func (s *Store) CreateConversation(title string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	title = strings.TrimSpace(title)
	if title == "" {
		title = "新会话"
	}
	now := time.Now()
	conversation := Conversation{ID: newID(), Title: title, CreatedAt: now, UpdatedAt: now}
	s.Conversations = append(s.Conversations, conversation)
	return conversation, s.saveLocked()
}

func (s *Store) RenameConversation(id, title string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	title = strings.TrimSpace(title)
	if title == "" {
		return Conversation{}, errors.New("title is required")
	}
	for i := range s.Conversations {
		if s.Conversations[i].ID == id {
			s.Conversations[i].Title = title
			s.Conversations[i].UpdatedAt = time.Now()
			if err := s.saveLocked(); err != nil {
				return Conversation{}, err
			}
			return s.Conversations[i], nil
		}
	}
	return Conversation{}, errors.New("conversation not found")
}

func (s *Store) DeleteConversation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Conversations) <= 1 {
		return errors.New("at least one conversation is required")
	}

	found := false
	conversations := s.Conversations[:0]
	for _, conversation := range s.Conversations {
		if conversation.ID == id {
			found = true
			continue
		}
		conversations = append(conversations, conversation)
	}
	if !found {
		return errors.New("conversation not found")
	}

	messages := s.Messages[:0]
	for _, message := range s.Messages {
		if message.ConversationID != id {
			messages = append(messages, message)
		}
	}
	s.Conversations = conversations
	s.Messages = messages
	return s.saveLocked()
}

func (s *Store) AddMessage(message Message) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if message.ConversationID == "" || !s.hasConversationLocked(message.ConversationID) {
		return Message{}, errors.New("conversation not found")
	}
	message.ID = newID()
	message.Type = normalizeMessageType(message.Type)
	message.Content = strings.TrimSpace(message.Content)
	message.CreatedAt = time.Now()
	message.UpdatedAt = message.CreatedAt
	if message.Content == "" && message.Attachment == nil {
		return Message{}, errors.New("message content is required")
	}
	if message.Type == "file" && message.Attachment == nil {
		return Message{}, errors.New("file attachment is required")
	}
	if message.Type == "image" && message.Attachment == nil && message.Content == "" {
		return Message{}, errors.New("image content is required")
	}

	s.Messages = append(s.Messages, message)
	for i := range s.Conversations {
		if s.Conversations[i].ID == message.ConversationID {
			s.Conversations[i].UpdatedAt = message.CreatedAt
			break
		}
	}
	return message, s.saveLocked()
}

func (s *Store) UpdateMessage(id, deviceID, content string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, errors.New("message content is required")
	}

	for i := range s.Messages {
		if s.Messages[i].ID != id {
			continue
		}
		if strings.TrimSpace(deviceID) == "" || s.Messages[i].DeviceID != deviceID {
			return Message{}, errors.New("permission denied")
		}
		s.Messages[i].Content = content
		s.Messages[i].UpdatedAt = time.Now()
		s.bumpConversationUpdatedAtLocked(s.Messages[i].ConversationID, s.Messages[i].UpdatedAt)
		if err := s.saveLocked(); err != nil {
			return Message{}, err
		}
		return s.Messages[i], nil
	}
	return Message{}, errors.New("message not found")
}

func (s *Store) DeleteMessage(id string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.Messages {
		if s.Messages[i].ID != id {
			continue
		}
		message := s.Messages[i]
		conversationID := s.Messages[i].ConversationID
		s.Messages = append(s.Messages[:i], s.Messages[i+1:]...)
		s.bumpConversationUpdatedAtLocked(conversationID, time.Now())
		return message, s.saveLocked()
	}
	return Message{}, errors.New("message not found")
}

func (s *Store) hasConversationLocked(id string) bool {
	for _, conversation := range s.Conversations {
		if conversation.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) bumpConversationUpdatedAtLocked(conversationID string, ts time.Time) {
	for i := range s.Conversations {
		if s.Conversations[i].ID == conversationID {
			s.Conversations[i].UpdatedAt = ts
			return
		}
	}
}

func (s *Store) saveLocked() error {
	tmp := s.path + ".tmp"
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func normalizeMessageType(value string) string {
	switch value {
	case "text", "image", "file":
		return value
	default:
		return "text"
	}
}

func sortConversations(conversations []Conversation) {
	sort.SliceStable(conversations, func(i, j int) bool {
		return conversations[i].UpdatedAt.After(conversations[j].UpdatedAt)
	})
}

func sortMessages(messages []Message) {
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
