const { contextBridge, ipcRenderer } = require('electron');

class RemoteClient {
  constructor() {
    this._ws = null;
    this._listeners = {
      status: [],
      frame: [],
      cursor: [],
      pong: [],
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
            } else if (data.type === 'pong') {
              this._emit('pong', data);
            } else if (data.type === 'error') {
              this._emit('error', new Error(data.message || '未知错误'));
              this._emit('log', `错误：${data.message || '未知错误'}`);
            } else if (data.type === 'connected') {
              this._emit('status', { connected: true, message: '已连接到目标主机' });
              this._emit('log', data.message || '已连接到目标主机');
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

  // 连接到中继服务器并指定要控制的 host
  connectWithHostId(url, hostId) {
    return new Promise((resolve, reject) => {
      if (this._ws) {
        this._ws.close();
        this._ws = null;
      }

      try {
        this._emit('status', { connected: false, message: '正在连接...' });
        this._emit('log', `尝试连接到中继服务器 ${url}`);

        const ws = new WebSocket(url);
        this._ws = ws;

        ws.onopen = () => {
          // 发送注册消息，指定要连接的 hostId
          const registerMsg = {
            type: 'register',
            role: 'client',
            hostId: hostId,
          };
          ws.send(JSON.stringify(registerMsg));
          this._emit('log', '已发送注册消息，等待服务器响应...');
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

        let registered = false;
        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data);
            
            // 处理注册响应
            if (!registered) {
              if (data.type === 'connected') {
                registered = true;
                this._emit('status', { connected: true, message: '已连接到目标主机' });
                this._emit('log', data.message || '已连接到目标主机');
                resolve();
              } else if (data.type === 'error') {
                this._emit('error', new Error(data.message || '连接失败'));
                this._emit('log', `连接失败：${data.message}`);
                reject(new Error(data.message || '连接失败'));
                ws.close();
              }
              return;
            }

            // 处理正常消息
            if (data.type === 'frame') {
              this._emit('frame', data.image);
              if (data.cursor) {
                this._emit('cursor', data.cursor);
              }
            } else if (data.type === 'clipboard') {
              if (typeof data.text === 'string') {
                this._emit('log', '收到远程剪贴板内容');
                this._emit('clipboard', data.text);
              }
            } else if (data.type === 'log') {
              this._emit('log', data.message || '');
            } else if (data.type === 'pong') {
              this._emit('pong', data);
            } else if (data.type === 'error') {
              this._emit('error', new Error(data.message || '未知错误'));
              this._emit('log', `错误：${data.message || '未知错误'}`);
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

  // 发送 ping，用于测量延迟
  sendPing() {
    if (!this._ws || this._ws.readyState !== WebSocket.OPEN) {
      return;
    }
    try {
      const payload = { clientTs: Date.now() };
      this._ws.send(JSON.stringify({ type: 'ping', payload }));
    } catch (e) {
      this._emit('error', e);
    }
  }
}

const client = new RemoteClient();

contextBridge.exposeInMainWorld('remote', {
  connect: (url) => client.connect(url),
  connectWithHostId: (url, hostId) => client.connectWithHostId(url, hostId),
  disconnect: () => client.disconnect(),
  sendInput: (payload) => client.sendInput(payload),
  sendClipboard: (payload) => client.sendClipboard(payload),
  sendFile: (payload) => client.sendFile(payload),
  sendPing: () => client.sendPing(),
  onStatus: (handler) => client.on('status', handler),
  onFrame: (handler) => client.on('frame', handler),
  onCursor: (handler) => client.on('cursor', handler),
  onLog: (handler) => client.on('log', handler),
  onClipboard: (handler) => client.on('clipboard', handler),
  onPong: (handler) => client.on('pong', handler),
  onError: (handler) => client.on('error', handler),
  /**
   * 请求主进程保存当前画面为本地图片文件
   */
  saveScreenshot: (dataUrl) => ipcRenderer.invoke('save-screenshot', dataUrl),
});

