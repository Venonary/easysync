package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", ":7788", "HTTP listen address")
	dataDir := flag.String("data", "data", "data directory")
	frontendDir := flag.String("frontend", filepath.Join("frontend", "dist"), "built frontend directory")
	flag.Parse()

	store, err := NewStore(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	hub := NewHub(store)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.State())
	})
	mux.HandleFunc("/api/conversations", conversationsHandler(store, hub))
	mux.HandleFunc("/api/conversations/", conversationHandler(store, hub))
	mux.HandleFunc("/api/messages/", messageHandler(store, hub))
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.Handle("/", spaHandler(*frontendDir))

	printStartup(*addr, *frontendDir, *dataDir)
	log.Fatal(http.ListenAndServe(*addr, logRequests(mux)))
}

func messageHandler(store *Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/messages/")
		id = strings.Trim(id, "/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var body struct {
				DeviceID string `json:"deviceId"`
				Content  string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json body", http.StatusBadRequest)
				return
			}
			message, err := store.UpdateMessage(id, body.DeviceID, body.Content)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "message_updated", Data: message})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			writeJSON(w, message)
		case http.MethodDelete:
			if err := store.DeleteMessage(id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{
				Type: "message_deleted",
				Data: map[string]string{"id": id},
			})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func conversationsHandler(store *Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, store.ListConversations())
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json body", http.StatusBadRequest)
				return
			}
			conversation, err := store.CreateConversation(body.Title)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "conversation_created", Data: conversation})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			writeJSON(w, conversation)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func conversationHandler(store *Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
		id = strings.Trim(id, "/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json body", http.StatusBadRequest)
				return
			}
			conversation, err := store.RenameConversation(id, body.Title)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "conversation_updated", Data: conversation})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			writeJSON(w, conversation)
		case http.MethodDelete:
			if err := store.DeleteConversation(id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "conversation_deleted", Data: map[string]string{"id": id}})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func spaHandler(dir string) http.HandlerFunc {
	fileServer := http.FileServer(http.Dir(dir))
	return func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		path := filepath.Join(dir, cleanPath)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "frontend is not built; run npm.cmd --prefix frontend run build", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, index)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func printStartup(addr, frontendDir, dataDir string) {
	port := addr
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	}

	fmt.Println("EasySync started")
	fmt.Printf("  Local:    http://127.0.0.1:%s\n", port)
	for _, ip := range lanIPv4s() {
		fmt.Printf("  LAN:      http://%s:%s\n", ip, port)
	}
	fmt.Printf("  Frontend: %s\n", frontendDir)
	fmt.Printf("  Data:     %s\n", dataDir)
}

func lanIPv4s() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}
