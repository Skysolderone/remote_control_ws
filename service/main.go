package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Text    string          `json:"text,omitempty"`   // 用于 clipboard 下行
	Image   string          `json:"image,omitempty"`  // 用于 frame 下行
	Cursor  *CursorInfo     `json:"cursor,omitempty"` // 用于 cursor 下行
}

// CursorInfo 鼠标位置和状态信息
type CursorInfo struct {
	X       int  `json:"x"`       // 屏幕绝对坐标 X
	Y       int  `json:"y"`       // 屏幕绝对坐标 Y
	Visible bool `json:"visible"` // 鼠标是否可见
}

// 对应客户端 sendClipboard({ text })
type ClipboardPayload struct {
	Text string `json:"text"`
}

// 对应客户端 sendFile({ name, size, mime, dataUrl })
type FilePayload struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime"`
	DataURL string `json:"dataUrl"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// 简单示例：允许任意来源
		return true
	},
}

func main() {
	http.HandleFunc("/ws", handleWS)

	addr := ":9000"
	log.Printf("WebSocket 服务已启动，监听 %s，路径 /ws", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("ListenAndServe error:", err)
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Println("客户端已连接")

	// 启动一个 goroutine 周期性抓取屏幕并推送真实画面
	go func() {
		// 默认每 200ms 抓一次屏（约 5 FPS），可根据需要调整
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		// 获取屏幕分辨率，用于计算鼠标相对位置
		bounds := screenshot.GetDisplayBounds(0)
		screenWidth := bounds.Dx()
		screenHeight := bounds.Dy()

		// 连接后立即发送第一帧和鼠标位置
		if frameData, err := captureScreen(); err == nil {
			cursor := getCursorPosition(screenWidth, screenHeight)
			msg := Message{
				Type:   "frame",
				Image:  frameData,
				Cursor: cursor,
			}
			if err := conn.WriteJSON(msg); err != nil {
				log.Println("发送首帧失败:", err)
			}
		}

		for range ticker.C {
			frameData, err := captureScreen()
			if err != nil {
				log.Printf("抓取屏幕失败: %v\n", err)
				continue
			}

			// 获取当前鼠标位置
			cursor := getCursorPosition(screenWidth, screenHeight)

			msg := Message{
				Type:   "frame",
				Image:  frameData,
				Cursor: cursor,
			}
			if err := conn.WriteJSON(msg); err != nil {
				log.Println("发送 frame 失败:", err)
				return
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Println("读取消息失败:", err)
			break
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Println("解析消息失败:", err)
			continue
		}

		switch msg.Type {
		case "clipboard":
			handleClipboard(conn, msg.Payload)
		case "file":
			handleFile(conn, msg.Payload)
		case "input":
			handleInput(msg.Payload)
		default:
			log.Println("未知消息类型:", msg.Type)
		}
	}
}

func handleClipboard(conn *websocket.Conn, payload json.RawMessage) {
	var p ClipboardPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 clipboard payload 失败:", err)
		return
	}
	log.Println("收到剪贴板内容:", p.Text)

	// 写入远程机器的系统剪贴板（Windows 需在桌面会话内运行）
	if err := clipboard.WriteAll(p.Text); err != nil {
		log.Println("写入系统剪贴板失败:", err)
	} else {
		log.Println("已写入系统剪贴板")
	}

	// 可选：再推回客户端，做“远程 → 本地”同步
	resp := Message{
		Type: "clipboard",
		Text: p.Text,
	}
	if err := conn.WriteJSON(resp); err != nil {
		log.Println("推送远程剪贴板给客户端失败:", err)
	}
}

func handleFile(conn *websocket.Conn, payload json.RawMessage) {
	var p FilePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 file payload 失败:", err)
		return
	}

	log.Printf("收到文件：name=%s size=%d mime=%s\n", p.Name, p.Size, p.Mime)

	// 解析 dataURL，取逗号后面的 base64
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

	// 存到当前目录下的 uploads 文件夹
	_ = os.MkdirAll("uploads", 0755)
	savePath := filepath.Join("uploads", sanitizeFilename(p.Name))
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		log.Println("保存文件失败:", err)
		return
	}

	log.Println("文件已保存到:", savePath)

	// 回一条日志消息给客户端
	type logMsg struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	_ = conn.WriteJSON(logMsg{
		Type:    "log",
		Message: fmt.Sprintf("服务端已保存文件：%s", savePath),
	})
}

func handleInput(payload json.RawMessage) {
	// 这里只是打印，方便你后续接 Windows API 做真实鼠标键盘事件
	log.Println("收到 input 事件:", string(payload))

	// 你后面可以在这里调用：
	// - Windows API: SendInput / mouse_event / keybd_event
	// - 或者用第三方库，模拟鼠标键盘操作
}

func sanitizeFilename(name string) string {
	// 简单处理，避免路径穿越等问题
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "" {
		name = fmt.Sprintf("file_%d", time.Now().Unix())
	}
	return name
}

// captureScreen 抓取主显示器屏幕并转换为 base64 data URL
func captureScreen() (string, error) {
	// 获取主显示器（索引 0）的边界
	bounds := screenshot.GetDisplayBounds(0)

	// 抓取屏幕
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return "", fmt.Errorf("截图失败: %w", err)
	}

	// 将图片编码为 PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("PNG 编码失败: %w", err)
	}

	// 转换为 base64 data URL
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	dataURL := fmt.Sprintf("data:image/png;base64,%s", b64)

	return dataURL, nil
}

// getCursorPosition 获取当前鼠标位置（屏幕绝对坐标）
func getCursorPosition(screenWidth, screenHeight int) *CursorInfo {
	var pt win.POINT
	win.GetCursorPos(&pt)

	// 检查鼠标是否在屏幕范围内
	visible := pt.X >= 0 && pt.X < int32(screenWidth) && pt.Y >= 0 && pt.Y < int32(screenHeight)

	return &CursorInfo{
		X:       int(pt.X),
		Y:       int(pt.Y),
		Visible: visible,
	}
}
