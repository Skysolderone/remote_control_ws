const { contextBridge, ipcRenderer } = require('electron');

class RemoteClient {
  constructor() {
    this._ws = null;
    this._listeners = {
      status: [],
      frame: [],
      cursor: [],
      log: [],
      error: [],
    };
  }

  _emit(type, payload) {
    const fns = this._listeners[type] || [];
    fns.forEach((fn) => {
      try {
        fn(payload);
      } catch (e) {
        // ignore listener errors
      }
    });
  }

  on(type, handler) {
    if (!this._listeners[type]) {
      this._listeners[type] = [];
    }
    this._listeners[type].push(handler);
    return () => {
      this._listeners[type] = this._listeners[type].filter((h) => h !== handler);
    };
  }

  connect(url) {
    return new Promise((resolve, reject) => {
      if (this._ws) {
        this._ws.close();
        this._ws = null;
      }

      try {
        this._emit('status', { connected: false, message: '正在连接...' });
        this._emit('log', `尝试连接 ${url}`);

        const ws = new WebSocket(url);
        this._ws = ws;

        ws.onopen = () => {
          this._emit('status', { connected: true, message: '已连接' });
          this._emit('log', '连接成功');
          resolve();
        };

        ws.onclose = () => {
          this._emit('status', { connected: false, message: '连接已关闭' });
          this._emit('log', '连接关闭');
          this._ws = null;
        };

        ws.onerror = (err) => {
          this._emit('error', err);
          this._emit('log', '连接错误');
          reject(err);
        };

        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data);
            if (data.type === 'frame') {
              // 约定：服务端推送 { type: 'frame', image: 'data:image/png;base64,...', cursor: {...} }
              this._emit('frame', data.image);
              // 如果包含鼠标位置信息，也触发 cursor 事件
              if (data.cursor) {
                this._emit('cursor', data.cursor);
              }
            } else if (data.type === 'clipboard') {
              // 约定：服务端可以推送 { type: 'clipboard', text: '...' }
              if (typeof data.text === 'string') {
                this._emit('log', '收到远程剪贴板内容');
                this._emit('clipboard', data.text);
              }
            } else if (data.type === 'log') {
              this._emit('log', data.message || '');
            }
          } catch (e) {
            this._emit('log', `收到无法解析的数据：${event.data}`);
          }
        };
      } catch (e) {
        this._emit('error', e);
        this._emit('log', `连接异常: ${e.message}`);
        reject(e);
      }
    });
  }

  disconnect() {
    if (this._ws) {
      this._emit('log', '主动断开连接');
      this._ws.close();
      this._ws = null;
      this._emit('status', { connected: false, message: '已断开' });
    }
  }

  /**
   * 发送输入事件（鼠标、键盘等）
   * payload 建议格式：
   * { type: 'mouse', event: 'move|down|up|wheel', x, y, button }
   * { type: 'keyboard', event: 'down|up', key, code }
   */
  sendInput(payload) {
    if (!this._ws || this._ws.readyState !== WebSocket.OPEN) {
      this._emit('log', '发送失败：未连接');
      return;
    }
    try {
      this._ws.send(JSON.stringify({ type: 'input', payload }));
    } catch (e) {
      this._emit('error', e);
      this._emit('log', `发送输入事件异常: ${e.message}`);
    }
  }

  /**
   * 剪贴板同步：将本地剪贴板内容发送到远程
   * payload: { text: string }
   */
  sendClipboard(payload) {
    if (!this._ws || this._ws.readyState !== WebSocket.OPEN) {
      this._emit('log', '发送剪贴板失败：未连接');
      return;
    }
    try {
      this._ws.send(JSON.stringify({ type: 'clipboard', payload }));
    } catch (e) {
      this._emit('error', e);
      this._emit('log', `发送剪贴板异常: ${e.message}`);
    }
  }

  /**
   * 文件传输：将本地文件发送到远程
   * payload: { name, size, mime, dataUrl }
   */
  sendFile(payload) {
    if (!this._ws || this._ws.readyState !== WebSocket.OPEN) {
      this._emit('log', '发送文件失败：未连接');
      return;
    }
    try {
      this._ws.send(JSON.stringify({ type: 'file', payload }));
      this._emit('log', `已发送文件：${payload.name} (${payload.size} bytes)`);
    } catch (e) {
      this._emit('error', e);
      this._emit('log', `发送文件异常: ${e.message}`);
    }
  }
}

const client = new RemoteClient();

contextBridge.exposeInMainWorld('remote', {
  connect: (url) => client.connect(url),
  disconnect: () => client.disconnect(),
  sendInput: (payload) => client.sendInput(payload),
  sendClipboard: (payload) => client.sendClipboard(payload),
  sendFile: (payload) => client.sendFile(payload),
  onStatus: (handler) => client.on('status', handler),
  onFrame: (handler) => client.on('frame', handler),
  onCursor: (handler) => client.on('cursor', handler),
  onLog: (handler) => client.on('log', handler),
  onClipboard: (handler) => client.on('clipboard', handler),
  onError: (handler) => client.on('error', handler),
  /**
   * 请求主进程保存当前画面为本地图片文件
   */
  saveScreenshot: (dataUrl) => ipcRenderer.invoke('save-screenshot', dataUrl),
});

