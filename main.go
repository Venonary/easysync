package main

import (
	"bytes"
	"crypto/tls"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

const maxUploadBytes = 256 << 20

// main 是程序入口。它解析命令行参数，初始化数据存储和 Hub，
// 并注册一组 HTTP 路由：用于健康检查、读取状态、会话管理、消息管理、
// WebSocket 实时通信以及前端静态文件托管，最后启动监听。
func main() {
	addr := flag.String("addr", ":7788", "HTTP listen address")
	dataDir := flag.String("data", "data", "data directory")
	useHTTPS := flag.Bool("https", false, "enable HTTPS with a self-signed certificate")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file")
	tlsKey := flag.String("tls-key", "", "TLS private key file")
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
		state, purged := store.StateAndPurge()
		removeUploadFiles(store, purged)
		writeJSON(w, state)
	})
	mux.HandleFunc("/api/ca.crt", rootCADownloadHandler(*dataDir))
	mux.HandleFunc("/api/settings", settingsHandler(store, hub))
	mux.HandleFunc("/api/conversations", conversationsHandler(store, hub))
	mux.HandleFunc("/api/conversations/", conversationHandler(store, hub))
	mux.HandleFunc("/api/messages/", messageHandler(store, hub))
	mux.HandleFunc("/api/uploads", uploadHandler(store, hub))
	mux.HandleFunc("/uploads/", serveUploadHandler(*dataDir))
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.Handle("/", spaHandler())

	printStartup(*addr, *dataDir, *useHTTPS)
	if *useHTTPS {
		material, err := EnsureTLSMaterial(*dataDir, *tlsCert, *tlsKey)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  Root CA:  %s (download /api/ca.crt)\n", material.RootCACert)
		server := &http.Server{
			Addr:    *addr,
			Handler: logRequests(mux),
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			ErrorLog: log.New(filteredErrorWriter{}, "", 0),
		}
		log.Fatal(server.ListenAndServeTLS(material.ServerCert, material.ServerKey))
		return
	}
	log.Fatal(http.ListenAndServe(*addr, logRequests(mux)))
}

func rootCADownloadHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := RootCAPath(dataDir)
		if _, err := os.Stat(path); err != nil {
			// Lazily create a root CA even when HTTPS is not currently enabled,
			// so users can download/install before flipping -https on.
			if _, err := EnsureTLSMaterial(dataDir, "", ""); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "root CA not available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="`+rootCACertFileName+`"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}

type filteredErrorWriter struct{}

func (filteredErrorWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("client sent an HTTP request to an HTTPS server")) {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}

func settingsHandler(store *Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, store.GetSettings())
		case http.MethodPut, http.MethodPatch:
			var body Settings
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json body", http.StatusBadRequest)
				return
			}
			settings, err := store.UpdateSettings(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "settings", Data: settings})
			writeJSON(w, settings)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func messageHandler(store *Store, hub *Hub) http.HandlerFunc {
	// messageHandler 处理单条消息的更新、软删除与恢复。
	// PATCH  /api/messages/{id}          修改内容
	// DELETE /api/messages/{id}          软删除（可配置撤销窗口）
	// POST   /api/messages/{id}/restore  在窗口内恢复
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/messages/")
		path = strings.Trim(path, "/")
		if path == "" {
			http.NotFound(w, r)
			return
		}

		// Support /api/messages/{id}/restore
		if strings.HasSuffix(path, "/restore") {
			id := strings.TrimSuffix(path, "/restore")
			id = strings.Trim(id, "/")
			if id == "" || r.Method != http.MethodPost {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				http.NotFound(w, r)
				return
			}
			message, purged, err := store.RestoreMessage(id)
			removeUploadFiles(store, purged)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			hub.broadcast(ServerEvent{Type: "message_restored", Data: message})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			writeJSON(w, message)
			return
		}

		id := path
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
			message, removeAttachment, purged, err := store.DeleteMessage(id)
			if err != nil {
				removeUploadFiles(store, purged)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if removeAttachment && message.Attachment != nil && message.Attachment.StoredName != "" {
				purged = append(purged, message.Attachment.StoredName)
			}
			removeUploadFiles(store, purged)
			hub.broadcast(ServerEvent{
				Type: "message_deleted",
				Data: map[string]any{
					"id":        id,
					"soft":      !removeAttachment,
					"deletedAt": message.DeletedAt,
				},
			})
			hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
			if removeAttachment {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, message)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func uploadHandler(store *Store, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(w, "invalid multipart form or file too large", http.StatusBadRequest)
			return
		}

		conversationID := r.FormValue("conversationId")
		deviceID := r.FormValue("deviceId")
		deviceName := r.FormValue("deviceName")
		deviceType := r.FormValue("deviceType")
		messageType := normalizeMessageType(r.FormValue("messageType"))
		if messageType != "image" {
			messageType = "file"
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		uploadDir := filepath.Join(uploadRootFromStore(store), "uploads")
		if err := os.MkdirAll(uploadDir, 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		storedName := fmt.Sprintf("%s%s", newID(), filepath.Ext(header.Filename))
		storedPath := filepath.Join(uploadDir, storedName)
		dst, err := os.Create(storedPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		written, copyErr := io.Copy(dst, file)
		closeErr := dst.Close()
		if copyErr != nil {
			_ = os.Remove(storedPath)
			http.Error(w, copyErr.Error(), http.StatusInternalServerError)
			return
		}
		if closeErr != nil {
			_ = os.Remove(storedPath)
			http.Error(w, closeErr.Error(), http.StatusInternalServerError)
			return
		}

		url := "/uploads/" + storedName
		attachment := &Attachment{
			URL:        url,
			Name:       header.Filename,
			StoredName: storedName,
			MimeType:   header.Header.Get("Content-Type"),
			Size:       written,
		}
		if messageType == "image" {
			attachment.URL = url
		}

		message, err := store.AddMessage(Message{
			ConversationID: conversationID,
			DeviceID:       deviceID,
			DeviceName:     deviceName,
			DeviceType:     deviceType,
			Type:           messageType,
			Content:        header.Filename,
			Attachment:     attachment,
		})
		if err != nil {
			_ = os.Remove(storedPath)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		hub.broadcast(ServerEvent{Type: "message", Data: message})
		hub.broadcast(ServerEvent{Type: "conversations", Data: store.ListConversations()})
		writeJSON(w, message)
	}
}

func serveUploadHandler(dataDir string) http.HandlerFunc {
	root := filepath.Join(dataDir, "uploads")
	fileServer := http.StripPrefix("/uploads/", http.FileServer(http.Dir(root)))
	return func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	}
}

func uploadRootFromStore(store *Store) string {
	return filepath.Dir(store.path)
}

func removeUploadFiles(store *Store, storedNames []string) {
	if len(storedNames) == 0 {
		return
	}
	root := filepath.Join(uploadRootFromStore(store), "uploads")
	for _, name := range storedNames {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			continue
		}
		_ = os.Remove(filepath.Join(root, name))
	}
}

func conversationsHandler(store *Store, hub *Hub) http.HandlerFunc {
	// conversationsHandler 提供会话列表读取与新会话创建接口：
	// GET  /api/conversations  - 列出会话
	// POST /api/conversations  - 创建新会话，并广播创建事件
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
	// conversationHandler 管理单个会话的重命名与删除：
	// PATCH /api/conversations/{id} - 重命名
	// DELETE /api/conversations/{id} - 删除（保留至少一个会话的校验）
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
	// writeJSON 将任意值编码为 JSON 并写入响应头（带 Content-Type）。
	// 错误在此处被忽略（仅记录或处理时可扩展）。
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func spaHandler() http.HandlerFunc {
	// spaHandler 用于托管嵌入在二进制内的前端单页应用（SPA）。
	// 它会尝试直接返回静态资源，否则返回 `index.html` 以让前端路由接管。
	dist, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	return func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// 检查嵌入文件系统中是否存在该路径
		if f, err := dist.Open(cleanPath); err == nil {
			if stat, err := f.Stat(); err == nil && !stat.IsDir() {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			f.Close()
		}

		// 否则，让 FileServer 处理 "/" 即相当于返回 index.html
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}

func logRequests(next http.Handler) http.Handler {
	// logRequests 是一个简单的中间件，用于记录每个入站请求的方法与路径。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func printStartup(addr, dataDir string, useHTTPS bool) {
	// printStartup 在服务启动时打印本机与局域网可访问的地址，
	// 以及前端构建目录和数据目录位置，便于用户在终端查看。
	port := addr
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	}
	scheme := "http"
	if useHTTPS {
		scheme = "https"
	}

	fmt.Println("EasySync started")
	fmt.Printf("  Local:    %s://127.0.0.1:%s\n", scheme, port)
	for _, ip := range lanIPv4s() {
		fmt.Printf("  LAN:      %s://%s:%s\n", scheme, ip, port)
	}
	fmt.Printf("  Frontend: Embedded\n")
	fmt.Printf("  Data:     %s\n", dataDir)
	fmt.Printf("  Root CA:  %s://127.0.0.1:%s/api/ca.crt\n", scheme, port)
}

func lanIPv4s() []string {
	// lanIPv4s 返回当前机器所有活动（非回环）的 IPv4 地址，供启动信息打印使用。
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
