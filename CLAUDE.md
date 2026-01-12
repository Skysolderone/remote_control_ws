# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## memory

使用中文回答,非必要不输出文档

## Project Overview

This is a remote desktop control system built with Electron (client UI) and Go (backend services). The system allows controlling remote Windows machines through a relay server architecture.

**Key Components:**

- **Electron Client** (`main.js`, `preload.js`, `renderer/`): Desktop UI for viewing and controlling remote machines
- **Relay Server** (`service/relay/`): WebSocket-based relay server that brokers connections between hosts and clients
- **Host Agent** (`service/host/`): Windows-only agent that runs on machines to be controlled

## Architecture

### Three-Tier Architecture

1. **Client (Electron)** - User-facing desktop application

   - Connects to relay server via WebSocket
   - Displays remote desktop frames as canvas images
   - Sends mouse/keyboard input events to remote host
   - Features: clipboard sync, file transfer, screenshot saving, latency measurement

2. **Relay Server (Go)** - Central message broker

   - Maintains WebSocket connections from both hosts and clients
   - Routes messages between matched host-client pairs
   - Exposes HTTP API at `/api/hosts` to list online hosts
   - Does not process frame data, only relays binary/JSON messages

3. **Host Agent (Go, Windows-only)** - Runs on controlled machines
   - Captures screen using `github.com/kbinani/screenshot`
   - Sends JPEG frames over WebSocket to relay
   - Receives input events and simulates mouse/keyboard via Windows API
   - Uses dynamic frame rate: faster when input is active, slower when idle

### Communication Flow

```
Client ←→ Relay Server ←→ Host Agent
```

- Host registers with relay server using unique ID
- Client queries `/api/hosts`, selects a host, then registers with relay specifying `hostId`
- Relay creates a bidirectional tunnel between the matched client-host pair
- All subsequent messages (frames, input events, clipboard, files) flow through relay

### Message Protocol

All WebSocket messages are JSON with a `type` field:

**Client → Host (via relay):**

- `input`: Mouse/keyboard events with normalized coordinates (0-1)
- `clipboard`: Clipboard text to sync to remote
- `file`: File transfer with base64 data URL
- `ping`: Latency measurement with client timestamp

**Host → Client (via relay):**

- `frame`: JPEG image as data URL + cursor position
- `clipboard`: Remote clipboard text to sync to local
- `pong`: Echo of ping with client timestamp
- `log`: Status messages
- `error`: Error messages

**Registration (Client/Host → Relay):**

- `register`: Client sends `{type: "register", role: "client", hostId: "..."}` after WebSocket open
- Host sends `{type: "register", role: "host", hostName: "..."}` after WebSocket open

## Development Commands

### Electron Client

```bash
# Install dependencies (using Yarn Berry)
yarn install

# Run the Electron client
yarn start
# or
npm start
```

### Go Services

**Build all services:**

```powershell
# Windows (PowerShell)
.\build.ps1 build-all
```

```bash
# Linux/Mac
make build-all
```

**Build individual components:**

```powershell
.\build.ps1 build-relay   # Builds Linux binary at bin/relay.linux
.\build.ps1 build-host    # Builds host agent at bin/host.exe
.\build.ps1 build-client  # (placeholder, not implemented)
```

```bash
make build-relay
make build-host
make build-client
```

**Deploy relay server:**

```powershell
.\build.ps1 scp-relay  # Builds and uploads to configured server
```

```bash
make scp-relay
```

### Running Services

**Relay Server:**

```bash
cd service/relay
go run main.go
# Listens on :8080 for WebSocket connections at /ws
# Provides HTTP API at /api/hosts
```

**Host Agent (Windows only):**

```bash
cd service/host
go run main.go
# Connects to wss://wws741.top/remote/ws (hardcoded)
# Registers as host with local IP as name
```

## Key Implementation Details

### Electron Client (renderer/renderer.js)

- **Relay URL**: Hardcoded to `https://wws741.top/remote` (converted to WSS for WebSocket)
- **Connection flow**:
  1. Fetch hosts list from `/api/hosts`
  2. User selects a host
  3. Connect to `/ws` and send registration message with `hostId`
- **Auto-reconnect**: Enabled by default, retries after 2 seconds on disconnect
- **Input control**: Toggle-based, sends normalized coordinates (0-1 range)
- **Latency**: Pings every 5 seconds when connected
- **Canvas rendering**: Scales remote frame to fit container while preserving aspect ratio

### Preload Bridge (preload.js)

**Exposed API via `window.remote`:**

- `connect(url)`: Direct WebSocket connection
- `connectWithHostId(url, hostId)`: Connect via relay to specific host
- `sendInput(payload)`, `sendClipboard(payload)`, `sendFile(payload)`, `sendPing()`
- Event listeners: `onStatus`, `onFrame`, `onCursor`, `onLog`, `onClipboard`, `onPong`, `onError`
- `saveScreenshot(dataUrl)`: IPC call to main process to save image via native dialog

### Host Agent (service/host/main.go)

- **Windows-only**: Uses `//go:build windows` build constraint
- **Screen capture**: Primary display only, resized to 800x450, JPEG quality 50%
- **Frame rate**: 5 FPS idle, 15 FPS when input is recent (within 100ms)
- **Input simulation**:
  - Mouse: `SetCursorPos`, `mouse_event` for clicks/wheel
  - Keyboard: `keybd_event` with virtual key codes mapped from JS key codes
- **Anti-packet-sticking**: Uses length-prefix protocol (4-byte header with message length)
- **Reconnect**: Retries every 5 seconds on connection failure

### Relay Server (service/relay/main.go)

- **Connection tracking**: Maintains maps of all connections, hosts by ID, clients by ID
- **Message routing**: Reads JSON messages, extracts target host/client, forwards raw bytes
- **HTTP API**: `GET /api/hosts` returns JSON list of online hosts with `id` and `name`
- **Cleanup**: Removes connections on close, notifies affected clients when host disconnects

## Common Patterns

### Adding a New Message Type

1. Define payload struct in both client (renderer.js) and host (main.go)
2. Add handler in `preload.js` WebSocket `onmessage` (client side)
3. Add handler in host agent's `handleMessage()` function
4. Relay server requires no changes (it forwards all messages)

### Modifying Frame Encoding

Frame encoding happens in `captureAndSendFrame()` in host agent:

- Adjust `jpeg.Encode` quality parameter
- Change target resolution in `draw.CatmullRom.Scale()`
- Modify frame rate logic in main loop (`idleFps`, `activeFps`)

### Extending Input Events

Input handling is in host agent's `simulateInput()`:

- Mouse events: Modify coordinate scaling and `mouse_event` flags
- Keyboard events: Update `keyCodeToVK()` mapping for new keys
- Add new event types by extending the switch statement

## File Structure

```
.
├── main.js                 # Electron main process
├── preload.js              # Context bridge API
├── renderer/
│   ├── index.html          # Client UI
│   ├── renderer.js         # Client logic
│   └── styles.css          # Client styles
├── service/
│   ├── go.mod              # Go dependencies
│   ├── relay/
│   │   └── main.go         # Relay server
│   └── host/
│       └── main.go         # Host agent (Windows)
├── build.ps1               # Build script (Windows)
├── makefile                # Build script (Unix)
└── bin/                    # Build output directory
```

## Configuration

### Hardcoded URLs

**Client** (`renderer/renderer.js:3`):

```javascript
const RELAY_BASE_URL = "https://wws741.top/remote";
```

**Host Agent** (`service/host/main.go:78`):

```go
relayURL = "wss://wws741.top/remote/ws"
```

To change the relay server, update both locations.

### Frame Quality Settings

**Host Agent** (`service/host/main.go`):

- Target resolution: 800x450 (optimized for low latency)
- JPEG quality: 50% (optimized for low latency)
- Idle FPS: 2 (500ms interval)
- Active FPS: 60 (16ms interval, optimized for smooth control)
- Scaling algorithm: NearestNeighbor (fastest)
- Input active window: 1.5 seconds

## Security Notes

- The Electron client uses `contextIsolation: true` and `sandbox: true`
- WebSocket connections use WSS (TLS) in production
- No authentication is implemented - relay server accepts all connections
- File transfer size limit: 10MB (enforced client-side)

## Antivirus False Positives

The host agent may be flagged by antivirus software due to its legitimate functionality:
- Screen capture (screenshot)
- Keyboard/mouse simulation (keybd_event, mouse_event)
- Clipboard access
- Network communication with external server

**Solution:**
Add `bin/host.exe` or the entire `bin/` directory to the antivirus exclusion list.

**Windows Defender (PowerShell as Administrator):**
```powershell
Add-MpPreference -ExclusionPath "D:\remote_control_electron\bin"
```

See `service/host/README.md` for detailed instructions.
