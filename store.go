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
	Settings      Settings       `json:"settings"`
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		path:     filepath.Join(dataDir, "store.json"),
		Settings: Settings{SoftDeleteUndoMinutes: DefaultSoftDeleteUndoMinutes},
	}
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

	// Decode settings separately so a missing settings object becomes the default,
	// while an explicit softDeleteUndoMinutes: 0 is preserved (immediate hard delete).
	var raw struct {
		Conversations []Conversation  `json:"conversations"`
		Messages      []Message       `json:"messages"`
		Settings      json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Conversations = raw.Conversations
	s.Messages = raw.Messages
	if len(raw.Settings) == 0 || string(raw.Settings) == "null" {
		s.Settings = Settings{SoftDeleteUndoMinutes: DefaultSoftDeleteUndoMinutes}
		return nil
	}
	var settings Settings
	if err := json.Unmarshal(raw.Settings, &settings); err != nil {
		return err
	}
	if settings.SoftDeleteUndoMinutes < 0 {
		settings.SoftDeleteUndoMinutes = DefaultSoftDeleteUndoMinutes
	}
	s.Settings = settings
	return nil
}

// State returns the live app snapshot. It purges expired soft-deletes from the
// store; use StateAndPurge when the caller must also remove attachment files.
func (s *Store) State() AppState {
	state, _ := s.StateAndPurge()
	return state
}

// StateAndPurge is like State but also returns attachment stored names that were
// dropped because their soft-delete undo window expired (caller should unlink files).
func (s *Store) StateAndPurge() (AppState, []string) {
	s.mu.Lock()
	removed, changed := s.purgeExpiredDeletedLocked(time.Now())
	if changed {
		_ = s.saveLocked()
	}
	conversations := append([]Conversation(nil), s.Conversations...)
	live, deleted := s.partitionMessagesLocked()
	settings := s.Settings
	s.mu.Unlock()

	sortConversations(conversations)
	sortMessages(live)
	sortDeletedMessages(deleted)
	return AppState{
		Conversations:   conversations,
		Messages:        live,
		DeletedMessages: deleted,
		Settings:        settings,
	}, removed
}

func (s *Store) GetSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Settings
}

func (s *Store) UpdateSettings(next Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if next.SoftDeleteUndoMinutes < 0 {
		return Settings{}, errors.New("softDeleteUndoMinutes must be >= 0")
	}
	// Cap at 7 days to avoid unbounded tombstones.
	if next.SoftDeleteUndoMinutes > 7*24*60 {
		return Settings{}, errors.New("softDeleteUndoMinutes must be <= 10080 (7 days)")
	}
	s.Settings = next
	if err := s.saveLocked(); err != nil {
		return Settings{}, err
	}
	return s.Settings, nil
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
	message.DeletedAt = nil
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
		if s.Messages[i].DeletedAt != nil {
			return Message{}, errors.New("cannot edit a deleted message")
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

// DeleteMessage soft-deletes when SoftDeleteUndoMinutes > 0, otherwise hard-deletes.
// Returns the message, whether this message's attachment should be removed immediately,
// and any attachment stored names purged because other soft-deletes expired.
func (s *Store) DeleteMessage(id string) (Message, bool, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	purged, purgedChanged := s.purgeExpiredDeletedLocked(time.Now())

	for i := range s.Messages {
		if s.Messages[i].ID != id {
			continue
		}
		if s.Messages[i].DeletedAt != nil {
			if purgedChanged {
				_ = s.saveLocked()
			}
			return Message{}, false, purged, errors.New("message already deleted")
		}

		now := time.Now()
		if s.Settings.SoftDeleteUndoMinutes <= 0 {
			message := s.Messages[i]
			conversationID := message.ConversationID
			s.Messages = append(s.Messages[:i], s.Messages[i+1:]...)
			s.bumpConversationUpdatedAtLocked(conversationID, now)
			if err := s.saveLocked(); err != nil {
				return Message{}, false, purged, err
			}
			return message, true, purged, nil
		}

		deletedAt := now
		s.Messages[i].DeletedAt = &deletedAt
		s.Messages[i].UpdatedAt = now
		s.bumpConversationUpdatedAtLocked(s.Messages[i].ConversationID, now)
		message := s.Messages[i]
		if err := s.saveLocked(); err != nil {
			return Message{}, false, purged, err
		}
		return message, false, purged, nil
	}
	if purgedChanged {
		_ = s.saveLocked()
	}
	return Message{}, false, purged, errors.New("message not found")
}

// RestoreMessage undoes a soft-delete if still within the undo window.
// The second return value lists attachment files purged during the call (expired tombstones).
func (s *Store) RestoreMessage(id string) (Message, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	purged, purgedChanged := s.purgeExpiredDeletedLocked(time.Now())

	for i := range s.Messages {
		if s.Messages[i].ID != id {
			continue
		}
		if s.Messages[i].DeletedAt == nil {
			if purgedChanged {
				_ = s.saveLocked()
			}
			return Message{}, purged, errors.New("message is not deleted")
		}
		if !s.canRestoreLocked(s.Messages[i], time.Now()) {
			// Should be rare after purgeExpiredDeletedLocked; hard-purge this row.
			if s.Messages[i].Attachment != nil && s.Messages[i].Attachment.StoredName != "" {
				purged = append(purged, s.Messages[i].Attachment.StoredName)
			}
			s.Messages = append(s.Messages[:i], s.Messages[i+1:]...)
			_ = s.saveLocked()
			return Message{}, purged, errors.New("restore window expired")
		}
		s.Messages[i].DeletedAt = nil
		s.Messages[i].UpdatedAt = time.Now()
		s.bumpConversationUpdatedAtLocked(s.Messages[i].ConversationID, s.Messages[i].UpdatedAt)
		if err := s.saveLocked(); err != nil {
			return Message{}, purged, err
		}
		return s.Messages[i], purged, nil
	}
	if purgedChanged {
		_ = s.saveLocked()
	}
	return Message{}, purged, errors.New("message not found")
}

// PurgeExpiredDeleted removes soft-deleted messages past the undo window.
// Returns attachment stored names that should be deleted from disk.
func (s *Store) PurgeExpiredDeleted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed, changed := s.purgeExpiredDeletedLocked(time.Now())
	if changed {
		_ = s.saveLocked()
	}
	return removed
}

func (s *Store) canRestoreLocked(message Message, now time.Time) bool {
	if message.DeletedAt == nil {
		return false
	}
	minutes := s.Settings.SoftDeleteUndoMinutes
	if minutes <= 0 {
		return false
	}
	deadline := message.DeletedAt.Add(time.Duration(minutes) * time.Minute)
	return !now.After(deadline)
}

func (s *Store) partitionMessagesLocked() (live, deleted []Message) {
	for _, message := range s.Messages {
		if message.DeletedAt != nil {
			deleted = append(deleted, message)
		} else {
			live = append(live, message)
		}
	}
	return live, deleted
}

// purgeExpiredDeletedLocked drops soft-deleted messages past the undo window.
// Returns attachment stored names for the caller to unlink and whether the slice changed.
func (s *Store) purgeExpiredDeletedLocked(now time.Time) (removed []string, changed bool) {
	kept := s.Messages[:0]
	for _, message := range s.Messages {
		if message.DeletedAt != nil && !s.canRestoreLocked(message, now) {
			changed = true
			if message.Attachment != nil && message.Attachment.StoredName != "" {
				removed = append(removed, message.Attachment.StoredName)
			}
			continue
		}
		kept = append(kept, message)
	}
	if changed {
		s.Messages = kept
	}
	return removed, changed
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

func sortDeletedMessages(messages []Message) {
	sort.SliceStable(messages, func(i, j int) bool {
		ai, bi := messages[i].DeletedAt, messages[j].DeletedAt
		if ai == nil || bi == nil {
			return messages[i].CreatedAt.After(messages[j].CreatedAt)
		}
		return ai.After(*bi)
	})
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
