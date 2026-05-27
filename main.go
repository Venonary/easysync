package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"bytes"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"strings"
)

// main 是程序入口。它解析命令行参数，初始化数据存储和 Hub，
// 并注册一组 HTTP 路由：用于健康检查、读取状态、会话管理、消息管理、
// WebSocket 实时通信以及前端静态文件托管，最后启动监听。
func main() {
	addr := flag.String("addr", ":7788", "HTTP listen address")
	dataDir := flag.String("data", "data", "data directory")
	frontendDir := flag.String("frontend", filepath.Join("frontend", "dist"), "built frontend directory")
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
		writeJSON(w, store.State())
	})
	mux.HandleFunc("/api/conversations", conversationsHandler(store, hub))
	mux.HandleFunc("/api/conversations/", conversationHandler(store, hub))
	mux.HandleFunc("/api/messages/", messageHandler(store, hub))
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.Handle("/", spaHandler(*frontendDir))

	printStartup(*addr, *frontendDir, *dataDir, *useHTTPS)
	if *useHTTPS {
		certFile, keyFile, err := ensureTLSFiles(*dataDir, *tlsCert, *tlsKey)
		if err != nil {
			log.Fatal(err)
		}
		server := &http.Server{
			Addr:    *addr,
			Handler: logRequests(mux),
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			ErrorLog: log.New(filteredErrorWriter{}, "", 0),
		}
		log.Fatal(server.ListenAndServeTLS(certFile, keyFile))
		return
	}
	log.Fatal(http.ListenAndServe(*addr, logRequests(mux)))
}

type filteredErrorWriter struct{}

func (filteredErrorWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("client sent an HTTP request to an HTTPS server")) {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}

func ensureTLSFiles(dataDir, certFile, keyFile string) (string, string, error) {
	if certFile != "" && keyFile != "" {
		return certFile, keyFile, nil
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", "", err
	}

	if certFile == "" {
		certFile = filepath.Join(dataDir, "https-cert.pem")
	}
	if keyFile == "" {
		keyFile = filepath.Join(dataDir, "https-key.pem")
	}

	if _, err := os.Stat(certFile); err == nil {
		if _, err := os.Stat(keyFile); err == nil {
			return certFile, keyFile, nil
		}
	}

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

func generateSelfSignedCert(certFile, keyFile string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{CommonName: "EasySync"},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(1, 0, 0),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	ifaces, err := net.Interfaces()
	if err == nil {
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
				if ip != nil {
					template.IPAddresses = append(template.IPAddresses, ip)
				}
			}
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pemEncode(certOut, "CERTIFICATE", derBytes); err != nil {
		return err
	}

	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pemEncode(keyOut, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(privateKey))
}

func pemEncode(file *os.File, blockType string, bytes []byte) error {
	if err := pem.Encode(file, &pem.Block{Type: blockType, Bytes: bytes}); err != nil {
		return err
	}
	return nil
}

func messageHandler(store *Store, hub *Hub) http.HandlerFunc {
	// messageHandler 处理单条消息的更新与删除操作。
	// PATCH /api/messages/{id} 允许设备按其 deviceId 修改消息内容（有权限校验）。
	// DELETE /api/messages/{id} 删除指定消息并在成功后广播变更。
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
			// 仅支持 PATCH 与 DELETE
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
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

func spaHandler(dir string) http.HandlerFunc {
	// spaHandler 用于托管前端单页应用（SPA）。
	// 它会尝试直接返回静态资源（当请求的路径对应真实文件时），
	// 否则返回 `index.html` 以让前端路由接管。若前端未构建，会返回 404 并提示构建命令。
	fileServer := http.FileServer(http.Dir(dir))
	return func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		path := filepath.Join(dir, cleanPath)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			// 请求静态文件，直接返回
			fileServer.ServeHTTP(w, r)
			return
		}
		// 否则返回 index.html 让前端路由处理
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "frontend is not built; run npm.cmd --prefix frontend run build", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, index)
	}
}

func logRequests(next http.Handler) http.Handler {
	// logRequests 是一个简单的中间件，用于记录每个入站请求的方法与路径。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func printStartup(addr, frontendDir, dataDir string, useHTTPS bool) {
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
	fmt.Printf("  Frontend: %s\n", frontendDir)
	fmt.Printf("  Data:     %s\n", dataDir)
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
