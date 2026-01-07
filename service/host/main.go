package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Text    string          `json:"text,omitempty"`
	Image   string          `json:"image,omitempty"`
	Cursor  *CursorInfo     `json:"cursor,omitempty"`
}

type CursorInfo struct {
	X       int  `json:"x"`
	Y       int  `json:"y"`
	Visible bool `json:"visible"`
}

type ClipboardPayload struct {
	Text string `json:"text"`
}

type FilePayload struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime"`
	DataURL string `json:"dataUrl"`
}

var (
	relayURL  = "ws://localhost:9000/ws" // 中继服务器地址
	hostName  = "Windows-PC"              // 机器名称，可通过环境变量或参数设置
	conn      *websocket.Conn
	connMutex sync.Mutex
)

func main() {
	// 从环境变量或命令行参数读取配置
	if url := os.Getenv("RELAY_URL"); url != "" {
		relayURL = url
	}
	if name := os.Getenv("HOST_NAME"); name != "" {
		hostName = name
	}

	log.Printf("被控制端启动，连接到中继服务器: %s", relayURL)
	log.Printf("机器名称: %s", hostName)

	// 连接到中继服务器
	for {
		if err := connectToRelay(); err != nil {
			log.Printf("连接失败: %v，5秒后重试...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// 启动屏幕抓取和消息处理
		go startScreenCapture()
		handleMessages()
	}
}

func connectToRelay() error {
	dialer := websocket.Dialer{}
	c, _, err := dialer.Dial(relayURL, nil)
	if err != nil {
		return fmt.Errorf("连接中继服务器失败: %w", err)
	}

	// 发送注册消息
	registerMsg := map[string]interface{}{
		"type": "register",
		"role": "host",
		"name": hostName,
	}
	if err := c.WriteJSON(registerMsg); err != nil {
		c.Close()
		return fmt.Errorf("发送注册消息失败: %w", err)
	}

	// 读取注册响应
	var resp map[string]interface{}
	if err := c.ReadJSON(&resp); err != nil {
		c.Close()
		return fmt.Errorf("读取注册响应失败: %w", err)
	}

	if resp["type"] != "registered" {
		c.Close()
		return fmt.Errorf("注册失败: %v", resp)
	}

	connMutex.Lock()
	conn = c
	connMutex.Unlock()

	log.Println("已成功注册到中继服务器")
	return nil
}

func startScreenCapture() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	bounds := screenshot.GetDisplayBounds(0)
	screenWidth := bounds.Dx()
	screenHeight := bounds.Dy()

	// 立即发送第一帧
	if frameData, err := captureScreen(); err == nil {
		cursor := getCursorPosition(screenWidth, screenHeight)
		sendFrame(frameData, cursor)
	}

	for range ticker.C {
		frameData, err := captureScreen()
		if err != nil {
			log.Printf("抓取屏幕失败: %v\n", err)
			continue
		}

		cursor := getCursorPosition(screenWidth, screenHeight)
		sendFrame(frameData, cursor)
	}
}

func sendFrame(frameData string, cursor *CursorInfo) {
	connMutex.Lock()
	defer connMutex.Unlock()

	if conn == nil {
		return
	}

	msg := Message{
		Type:   "frame",
		Image:  frameData,
		Cursor: cursor,
	}
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("发送 frame 失败: %v", err)
	}
}

func handleMessages() {
	for {
		connMutex.Lock()
		c := conn
		connMutex.Unlock()

		if c == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		_, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("读取消息失败: %v，尝试重连...", err)
			connMutex.Lock()
			conn = nil
			connMutex.Unlock()
			c.Close()
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("解析消息失败: %v", err)
			continue
		}

		switch msg.Type {
		case "clipboard":
			handleClipboard(msg.Payload)
		case "file":
			handleFile(msg.Payload)
		case "input":
			handleInput(msg.Payload)
		default:
			log.Printf("未知消息类型: %s", msg.Type)
		}
	}
}

func handleClipboard(payload json.RawMessage) {
	var p ClipboardPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 clipboard payload 失败:", err)
		return
	}
	log.Println("收到剪贴板内容:", p.Text)

	if err := clipboard.WriteAll(p.Text); err != nil {
		log.Println("写入系统剪贴板失败:", err)
	} else {
		log.Println("已写入系统剪贴板")
	}
}

func handleFile(payload json.RawMessage) {
	var p FilePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 file payload 失败:", err)
		return
	}

	log.Printf("收到文件：name=%s size=%d mime=%s\n", p.Name, p.Size, p.Mime)

	comma := strings.Index(p.DataURL, ",")
	if comma < 0 {
		log.Println("无效的 dataUrl")
		return
	}
	b64Data := p.DataURL[comma+1:]

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		log.Println("base64 解码失败:", err)
		return
	}

	_ = os.MkdirAll("uploads", 0755)
	savePath := filepath.Join("uploads", sanitizeFilename(p.Name))
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		log.Println("保存文件失败:", err)
		return
	}

	log.Println("文件已保存到:", savePath)
}

func handleInput(payload json.RawMessage) {
	log.Println("收到 input 事件:", string(payload))
	// TODO: 实现真实的鼠标键盘控制
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "" {
		name = fmt.Sprintf("file_%d", time.Now().Unix())
	}
	return name
}

func captureScreen() (string, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return "", fmt.Errorf("截图失败: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("PNG 编码失败: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	dataURL := fmt.Sprintf("data:image/png;base64,%s", b64)
	return dataURL, nil
}

func getCursorPosition(screenWidth, screenHeight int) *CursorInfo {
	var pt win.POINT
	win.GetCursorPos(&pt)

	visible := pt.X >= 0 && pt.X < int32(screenWidth) && pt.Y >= 0 && pt.Y < int32(screenHeight)

	return &CursorInfo{
		X:       int(pt.X),
		Y:       int(pt.Y),
		Visible: visible,
	}
}
