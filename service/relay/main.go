package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 连接类型
const (
	ConnTypeHost   = "host"   // 被控制端
	ConnTypeClient = "client" // 控制端
)

// Connection 封装 WebSocket 连接和元数据
type Connection struct {
	ID       string // 连接唯一标识
	Type     string // host 或 client
	Conn     *websocket.Conn
	HostID   string      // 如果是 client，指定要控制的 host ID
	HostName string      // 如果是 host，机器名称
	Send     chan []byte // 发送消息通道
	LastPing time.Time   // 最后心跳时间
}

// RelayServer 中继服务器
type RelayServer struct {
	// 所有连接：ID -> Connection
	connections map[string]*Connection
	// Host 连接：HostID -> Connection
	hosts map[string]*Connection
	// Client 连接：ClientID -> Connection
	clients map[string]*Connection
	// 保护并发访问
	mu sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewRelayServer() *RelayServer {
	return &RelayServer{
		connections: make(map[string]*Connection),
		hosts:       make(map[string]*Connection),
		clients:     make(map[string]*Connection),
	}
}

func (s *RelayServer) addConnection(conn *Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[conn.ID] = conn
	if conn.Type == ConnTypeHost {
		s.hosts[conn.ID] = conn
		log.Printf("[Host] %s (%s) 已注册", conn.ID, conn.HostName)
	} else {
		s.clients[conn.ID] = conn
		log.Printf("[Client] %s 已连接，目标 Host: %s", conn.ID, conn.HostID)
	}
}

func (s *RelayServer) removeConnection(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conn, ok := s.connections[id]
	if !ok {
		return
	}
	delete(s.connections, id)
	if conn.Type == ConnTypeHost {
		delete(s.hosts, id)
		log.Printf("[Host] %s 已断开", id)
		// 通知所有连接到该 host 的 client
		for _, client := range s.clients {
			if client.HostID == id {
				s.sendToClient(client, map[string]interface{}{
					"type":    "log",
					"message": "被控制端已断开连接",
				})
			}
		}
	} else {
		delete(s.clients, id)
		log.Printf("[Client] %s 已断开", id)
	}
	close(conn.Send)
}

func (s *RelayServer) getHost(id string) *Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hosts[id]
}

func (s *RelayServer) getClient(id string) *Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients[id]
}

func (s *RelayServer) listHosts() []map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hosts := make([]map[string]string, 0, len(s.hosts))
	for _, host := range s.hosts {
		hosts = append(hosts, map[string]string{
			"id":   host.ID,
			"name": host.HostName,
		})
	}
	return hosts
}

func (s *RelayServer) sendToHost(host *Connection, msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("序列化消息失败: %v", err)
		return
	}
	select {
	case host.Send <- data:
	default:
		log.Printf("Host %s 发送通道已满", host.ID)
	}
}

func (s *RelayServer) sendToClient(client *Connection, msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("序列化消息失败: %v", err)
		return
	}
	select {
	case client.Send <- data:
	default:
		log.Printf("Client %s 发送通道已满", client.ID)
	}
}

// 处理 WebSocket 连接
func (s *RelayServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}

	// 读取第一条消息，确定连接类型
	var firstMsg map[string]interface{}
	if err := conn.ReadJSON(&firstMsg); err != nil {
		log.Println("读取初始消息失败:", err)
		conn.Close()
		return
	}

	connType, _ := firstMsg["type"].(string)
	if connType != "register" {
		log.Println("无效的初始消息类型")
		conn.Close()
		return
	}

	role, _ := firstMsg["role"].(string)
	connID := generateID()
	connection := &Connection{
		ID:       connID,
		Type:     role,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		LastPing: time.Now(),
	}

	if role == ConnTypeHost {
		// 被控制端注册
		hostName, _ := firstMsg["name"].(string)
		if hostName == "" {
			hostName = "Unknown"
		}
		connection.HostName = hostName
		s.addConnection(connection)

		// 发送注册成功消息
		conn.WriteJSON(map[string]interface{}{
			"type":    "registered",
			"id":      connID,
			"message": "注册成功",
		})

	} else if role == ConnTypeClient {
		// 控制端连接，需要指定要控制的 host ID
		hostID, _ := firstMsg["hostId"].(string)
		if hostID == "" {
			conn.WriteJSON(map[string]interface{}{
				"type":    "error",
				"message": "请指定 hostId",
			})
			conn.Close()
			return
		}

		// 检查 host 是否存在
		host := s.getHost(hostID)
		if host == nil {
			conn.WriteJSON(map[string]interface{}{
				"type":    "error",
				"message": "目标主机不存在或已断开",
			})
			conn.Close()
			return
		}

		connection.HostID = hostID
		s.addConnection(connection)

		// 发送连接成功消息
		conn.WriteJSON(map[string]interface{}{
			"type":    "connected",
			"id":      connID,
			"hostId":  hostID,
			"message": "已连接到目标主机",
		})

	} else {
		conn.WriteJSON(map[string]interface{}{
			"type":    "error",
			"message": "无效的角色类型",
		})
		conn.Close()
		return
	}

	// 启动读写 goroutine
	go s.writePump(connection)
	s.readPump(connection)
}

// 读取消息并转发
func (s *RelayServer) readPump(conn *Connection) {
	defer func() {
		s.removeConnection(conn.ID)
		conn.Conn.Close()
	}()

	conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.Conn.SetPongHandler(func(string) error {
		conn.LastPing = time.Now()
		conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, data, err := conn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("连接错误: %v", err)
			}
			break
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("解析消息失败: %v", err)
			continue
		}

		// 转发消息
		if conn.Type == ConnTypeHost {
			// Host 发送的消息（画面、鼠标等）转发给所有连接到它的 Client
			for _, client := range s.clients {
				if client.HostID == conn.ID {
					s.sendToClient(client, msg)
				}
			}
		} else if conn.Type == ConnTypeClient {
			// Client 发送的消息（输入事件、剪贴板、文件等）转发给对应的 Host
			host := s.getHost(conn.HostID)
			if host != nil {
				s.sendToHost(host, msg)
			} else {
				s.sendToClient(conn, map[string]interface{}{
					"type":    "log",
					"message": "目标主机已断开",
				})
			}
		}
	}
}

// 写入消息
func (s *RelayServer) writePump(conn *Connection) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		conn.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-conn.Send:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := conn.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// 批量发送其他待发送消息
			n := len(conn.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-conn.Send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// 获取在线主机列表（HTTP API）
func (s *RelayServer) handleListHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	hosts := s.listHosts()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"hosts": hosts,
	})
}

func generateID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(6)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := range b {
		b[i] = letters[seed%int64(len(letters))]
		seed = seed*1103515245 + 12345 // 简单线性同余生成器
	}
	return string(b)
}

func main() {
	server := NewRelayServer()

	http.HandleFunc("/ws", server.handleWS)
	http.HandleFunc("/api/hosts", server.handleListHosts)

	addr := ":8095"
	log.Printf("中继服务器已启动，监听 %s", addr)
	log.Printf("WebSocket 路径: /ws")
	log.Printf("API 路径: /api/hosts")
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("ListenAndServe error:", err)
	}
}
