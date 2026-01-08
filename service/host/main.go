//go:build windows
// +build windows

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	"golang.org/x/image/draw"
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Text    string          `json:"text,omitempty"`
	Image   string          `json:"image,omitempty"`
	Cursor  *CursorInfo     `json:"cursor,omitempty"`
}

type CursorInfo struct {
	X       float64 `json:"x"` // 归一化坐标 0~1
	Y       float64 `json:"y"`
	Visible bool    `json:"visible"`
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

// 输入事件负载
type InputPayload struct {
	Type     string  `json:"type"`  // mouse | keyboard
	Event    string  `json:"event"` // move|down|up|wheel|down/up(键盘)
	X        float64 `json:"x"`     // 相对坐标 0~1
	Y        float64 `json:"y"`
	Button   int     `json:"button"` // 0:左 1:中 2:右
	DeltaY   float64 `json:"deltaY"` // 滚轮
	Key      string  `json:"key"`    // 键值
	Code     string  `json:"code"`   // 物理键位（优先使用）
	AltKey   bool    `json:"altKey"`
	CtrlKey  bool    `json:"ctrlKey"`
	ShiftKey bool    `json:"shiftKey"`
	MetaKey  bool    `json:"metaKey"`
}

// Ping 负载
type PingPayload struct {
	ClientTs int64 `json:"clientTs"`
}

var (
	relayURL  = "wss://wws741.top/remote/ws" // 中继服务器地址（通过 Caddy 代理）
	hostName  = ""                           // 机器名称，自动获取 IP 地址
	conn      *websocket.Conn
	connMutex sync.Mutex
	// 最近一次输入事件时间，用于动态调整帧率
	lastInputTime time.Time
	lastInputMu   sync.Mutex
)

// getLocalIP 获取本机 IP 地址
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Printf("获取本机 IP 失败: %v，使用 localhost", err)
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func main() {
	// 从环境变量或命令行参数读取配置
	if url := os.Getenv("RELAY_URL"); url != "" {
		relayURL = url
	}

	// 如果没有通过环境变量设置名称，则使用 IP 地址
	if name := os.Getenv("HOST_NAME"); name != "" {
		hostName = name
	} else {
		hostName = getLocalIP()
	}

	log.Printf("被控制端启动，连接到中继服务器: %s", relayURL)
	log.Printf("机器标识: %s", hostName)

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
	// 如果域名解析失败，尝试使用 IP 地址
	url := relayURL
	if strings.Contains(relayURL, "wws741.top") {
		// 尝试解析域名
		addrs, err := net.LookupHost("wws741.top")
		if err != nil {
			log.Printf("DNS 解析失败，尝试使用 IP 地址: %v", err)
			// 使用 IP 地址（需要 Caddy 支持 IP 访问）
			url = strings.Replace(relayURL, "wws741.top", "8.218.201.224", 1)
			log.Printf("使用 IP 地址连接: %s", url)
		} else {
			log.Printf("DNS 解析成功: %v", addrs)
		}
	}

	dialer := websocket.Dialer{}

	// 如果使用 IP 地址连接 wss，需要跳过证书验证（仅用于测试）
	if strings.HasPrefix(url, "wss://") && strings.Contains(url, "8.218.201.224") {
		log.Printf("警告: 使用 IP 地址连接 wss，跳过证书验证")
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // 跳过证书验证
		}
	}

	c, _, err := dialer.Dial(url, nil)
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
	bounds := screenshot.GetDisplayBounds(0)
	screenWidth := bounds.Dx()
	screenHeight := bounds.Dy()

	// 立即发送第一帧
	if frameData, err := captureScreen(); err == nil {
		cursor := getCursorPosition(screenWidth, screenHeight)
		sendFrame(frameData, cursor)
	}

	for {
		// 根据最近输入时间动态调整间隔：
		// 有操作时更高帧率（16ms = 60fps），无操作时降到 500ms
		interval := 500 * time.Millisecond
		lastInputMu.Lock()
		last := lastInputTime
		lastInputMu.Unlock()
		if !last.IsZero() && time.Since(last) < 1500*time.Millisecond {
			interval = 16 * time.Millisecond // 60 FPS
		}

		time.Sleep(interval)

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
		case "ping":
			handlePing(c, msg.Payload)
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
	var p InputPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 input payload 失败:", err)
		return
	}

	// 更新最近输入时间
	lastInputMu.Lock()
	lastInputTime = time.Now()
	lastInputMu.Unlock()

	log.Printf("收到输入事件: type=%s event=%s x=%.3f y=%.3f button=%d deltaY=%.2f key=%s code=%s",
		p.Type, p.Event, p.X, p.Y, p.Button, p.DeltaY, p.Key, p.Code)

	switch p.Type {
	case "mouse":
		handleMouseInput(p)
	case "keyboard":
		handleKeyboardInput(p)
	default:
		log.Println("未知 input 类型:", p.Type)
	}

	// 为了降低“操作到画面更新”的主观延迟，在每次输入后立即抓取并推送一帧
	go func() {
		frameData, err := captureScreen()
		if err != nil {
			log.Printf("即时截图失败: %v", err)
			return
		}
		// 使用当前屏幕分辨率获取鼠标位置
		bounds := screenshot.GetDisplayBounds(0)
		cursor := getCursorPosition(bounds.Dx(), bounds.Dy())
		sendFrame(frameData, cursor)
	}()
}

// 处理 ping：原样回显 clientTs，用于测量 RTT
func handlePing(c *websocket.Conn, payload json.RawMessage) {
	var p PingPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Println("解析 ping payload 失败:", err)
		return
	}
	resp := map[string]interface{}{
		"type":     "pong",
		"clientTs": p.ClientTs,
	}
	if err := c.WriteJSON(resp); err != nil {
		log.Println("发送 pong 失败:", err)
	}
}

// 处理鼠标事件
func handleMouseInput(p InputPayload) {
	screenW := win.GetSystemMetrics(win.SM_CXSCREEN)
	screenH := win.GetSystemMetrics(win.SM_CYSCREEN)
	if screenW <= 0 || screenH <= 0 {
		log.Println("获取屏幕分辨率失败")
		return
	}

	x := int(float64(screenW-1) * p.X)
	y := int(float64(screenH-1) * p.Y)

	// 移动到指定位置
	win.SetCursorPos(int32(x), int32(y))

	switch p.Event {
	case "move":
		return
	case "down":
		mouseButtonEvent(p.Button, true)
	case "up":
		mouseButtonEvent(p.Button, false)
	case "wheel":
		mouseWheelEvent(p.DeltaY)
	default:
		log.Println("未知鼠标事件:", p.Event)
	}
}

func mouseButtonEvent(btn int, down bool) {
	var flag uint32
	switch btn {
	case 0: // 左键
		if down {
			flag = win.MOUSEEVENTF_LEFTDOWN
		} else {
			flag = win.MOUSEEVENTF_LEFTUP
		}
	case 1: // 中键
		if down {
			flag = win.MOUSEEVENTF_MIDDLEDOWN
		} else {
			flag = win.MOUSEEVENTF_MIDDLEUP
		}
	case 2: // 右键
		if down {
			flag = win.MOUSEEVENTF_RIGHTDOWN
		} else {
			flag = win.MOUSEEVENTF_RIGHTUP
		}
	default:
		// 默认左键
		if down {
			flag = win.MOUSEEVENTF_LEFTDOWN
		} else {
			flag = win.MOUSEEVENTF_LEFTUP
		}
	}
	sendMouseInput(flag, 0)
}

func mouseWheelEvent(deltaY float64) {
	if deltaY == 0 {
		return
	}
	// 浏览器的 deltaY 向下为正，这里取反符合 Windows 习惯
	wheel := int32(-120)
	if deltaY < 0 {
		wheel = 120
	}
	sendMouseInput(win.MOUSEEVENTF_WHEEL, wheel)
}

// 处理键盘事件
func handleKeyboardInput(p InputPayload) {
	vk := mapCodeToVK(p.Code, p.Key)
	if vk == 0 {
		log.Printf("未映射的键: code=%s key=%s", p.Code, p.Key)
		return
	}

	sendKeyboardInput(vk, p.Event == "up")
}

func mapCodeToVK(code, key string) uint16 {
	code = strings.TrimSpace(code)
	key = strings.TrimSpace(key)

	// 优先按 code 识别
	if strings.HasPrefix(code, "Key") && len(code) == 4 {
		ch := code[3]
		if ch >= 'A' && ch <= 'Z' {
			return uint16(ch) // VK_A ~ VK_Z
		}
	}
	if strings.HasPrefix(code, "Digit") && len(code) == 6 {
		ch := code[5]
		if ch >= '0' && ch <= '9' {
			return uint16(ch) // VK_0 ~ VK_9
		}
	}

	switch code {
	case "Space":
		return win.VK_SPACE
	case "Enter":
		return win.VK_RETURN
	case "Tab":
		return win.VK_TAB
	case "Backspace":
		return win.VK_BACK
	case "Escape":
		return win.VK_ESCAPE
	case "ArrowUp":
		return win.VK_UP
	case "ArrowDown":
		return win.VK_DOWN
	case "ArrowLeft":
		return win.VK_LEFT
	case "ArrowRight":
		return win.VK_RIGHT
	case "ShiftLeft", "ShiftRight":
		return win.VK_SHIFT
	case "ControlLeft", "ControlRight":
		return win.VK_CONTROL
	case "AltLeft", "AltRight":
		return win.VK_MENU
	}

	// 退化到 key，适配一些常见值
	switch strings.ToLower(key) {
	case " ", "space":
		return win.VK_SPACE
	case "enter":
		return win.VK_RETURN
	case "tab":
		return win.VK_TAB
	case "backspace":
		return win.VK_BACK
	case "escape", "esc":
		return win.VK_ESCAPE
	case "arrowup", "up":
		return win.VK_UP
	case "arrowdown", "down":
		return win.VK_DOWN
	case "arrowleft", "left":
		return win.VK_LEFT
	case "arrowright", "right":
		return win.VK_RIGHT
	case "shift":
		return win.VK_SHIFT
	case "control", "ctrl":
		return win.VK_CONTROL
	case "alt":
		return win.VK_MENU
	}

	// 未识别返回 0
	return 0
}

// 发送鼠标输入（使用 mouse_event）
func sendMouseInput(flags uint32, data int32) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	mouseEvent := user32.NewProc("mouse_event")
	mouseEvent.Call(uintptr(flags), 0, 0, uintptr(uint32(data)), 0)
}

// 发送键盘输入（使用 keybd_event）
func sendKeyboardInput(vk uint16, keyUp bool) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	keybdEvent := user32.NewProc("keybd_event")
	flags := uint32(0)
	if keyUp {
		flags = win.KEYEVENTF_KEYUP
	}
	keybdEvent.Call(uintptr(vk), 0, uintptr(flags), 0)
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

	// 将原始截图按比例缩放到不超过 1280x720，减小单帧数据量
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()
	targetW := srcW
	targetH := srcH
	const maxW = 800  // 进一步降低分辨率以最小化延迟
	const maxH = 450
	if srcW > maxW || srcH > maxH {
		scaleW := float64(maxW) / float64(srcW)
		scaleH := float64(maxH) / float64(srcH)
		scale := scaleW
		if scaleH < scaleW {
			scale = scaleH
		}
		targetW = int(float64(srcW) * scale)
		targetH = int(float64(srcH) * scale)
	}

	var scaled image.Image = img
	if targetW != srcW || targetH != srcH {
		dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		// 使用最快的缩放算法以降低CPU消耗和延迟
		draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		scaled = dst
	}

	var buf bytes.Buffer
	// 使用 JPEG 压缩，减小单帧体积（低质量以最小化延迟）
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 50}); err != nil {
		return "", fmt.Errorf("JPEG 编码失败: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	dataURL := fmt.Sprintf("data:image/jpeg;base64,%s", b64)
	return dataURL, nil
}

func getCursorPosition(screenWidth, screenHeight int) *CursorInfo {
	var pt win.POINT
	win.GetCursorPos(&pt)

	visible := pt.X >= 0 && pt.X < int32(screenWidth) && pt.Y >= 0 && pt.Y < int32(screenHeight)

	// 归一化到 0~1，避免与缩放后分辨率不一致
	nx := float64(pt.X) / float64(screenWidth)
	ny := float64(pt.Y) / float64(screenHeight)
	if nx < 0 {
		nx = 0
	} else if nx > 1 {
		nx = 1
	}
	if ny < 0 {
		ny = 0
	} else if ny > 1 {
		ny = 1
	}

	return &CursorInfo{
		X:       nx,
		Y:       ny,
		Visible: visible,
	}
}
