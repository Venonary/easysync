package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

type Client struct {
	id         string
	deviceID   string
	deviceName string
	deviceType string
	conn       *WSConn
	hub        *Hub
	send       chan ServerEvent
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client
	store   *Store
}

type ClientEvent struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	MessageType    string `json:"messageType"`
	Content        string `json:"content"`
	DeviceID       string `json:"deviceId"`
	DeviceName     string `json:"deviceName"`
	DeviceType     string `json:"deviceType"`
}

type ServerEvent struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

func NewHub(store *Store) *Hub {
	return &Hub{clients: map[string]*Client{}, store: store}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := UpgradeWebSocket(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	client := &Client{
		id:         newID(),
		deviceID:   r.URL.Query().Get("deviceId"),
		deviceName: r.URL.Query().Get("deviceName"),
		deviceType: r.URL.Query().Get("deviceType"),
		conn:       conn,
		hub:        h,
		send:       make(chan ServerEvent, 32),
	}
	if client.deviceName == "" {
		client.deviceName = "匿名设备"
	}

	h.register(client)
	go client.writePump()
	client.readPump()
}

func (h *Hub) register(client *Client) {
	h.mu.Lock()
	h.clients[client.id] = client
	h.mu.Unlock()
	h.broadcast(ServerEvent{Type: "presence", Data: h.Presence()})
}

func (h *Hub) unregister(client *Client) {
	h.mu.Lock()
	if _, ok := h.clients[client.id]; ok {
		delete(h.clients, client.id)
		close(client.send)
	}
	h.mu.Unlock()
	_ = client.conn.Close()
	h.broadcast(ServerEvent{Type: "presence", Data: h.Presence()})
}

func (h *Hub) broadcast(event ServerEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		select {
		case client.send <- event:
		default:
		}
	}
}

func (h *Hub) Presence() []map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	devices := make([]map[string]string, 0, len(h.clients))
	for _, client := range h.clients {
		devices = append(devices, map[string]string{
			"id":     client.deviceID,
			"name":   client.deviceName,
			"type":   client.deviceType,
			"socket": client.id,
		})
	}
	return devices
}

func (c *Client) readPump() {
	defer c.hub.unregister(c)
	for {
		payload, err := c.conn.ReadText()
		if err != nil {
			return
		}

		var event ClientEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			log.Printf("invalid websocket payload: %v", err)
			continue
		}
		if event.Type != "send_message" {
			continue
		}

		message, err := c.hub.store.AddMessage(Message{
			ConversationID: event.ConversationID,
			DeviceID:       firstNonEmpty(event.DeviceID, c.deviceID),
			DeviceName:     firstNonEmpty(event.DeviceName, c.deviceName),
			DeviceType:     firstNonEmpty(event.DeviceType, c.deviceType),
			Type:           event.MessageType,
			Content:        event.Content,
		})
		if err != nil {
			c.send <- ServerEvent{Type: "error", Data: err.Error()}
			continue
		}
		c.hub.broadcast(ServerEvent{Type: "message", Data: message})
		c.hub.broadcast(ServerEvent{Type: "conversations", Data: c.hub.store.ListConversations()})
	}
}

func (c *Client) writePump() {
	for event := range c.send {
		payload, err := json.Marshal(event)
		if err != nil {
			continue
		}
		if err := c.conn.WriteText(payload); err != nil {
			return
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
