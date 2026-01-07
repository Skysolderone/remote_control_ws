// 渲染进程脚本：负责 UI 和与 preload 暴露的 remote API 交互

const relayUrlInput = document.getElementById('relayUrl');
const refreshHostsBtn = document.getElementById('refreshHostsBtn');
const hostsList = document.getElementById('hostsList');
const connectBtn = document.getElementById('connectBtn');
const disconnectBtn = document.getElementById('disconnectBtn');
const statusBadge = document.getElementById('statusBadge');
const resolutionLabel = document.getElementById('resolutionLabel');
const inputLabel = document.getElementById('inputLabel');
const inputToggle = document.getElementById('inputToggle');
const desktopCanvas = document.getElementById('desktopCanvas');
const desktopContainer = document.getElementById('desktopContainer');
const desktopPlaceholder = document.getElementById('desktopPlaceholder');
const logContainer = document.getElementById('logContainer');
const clearLogBtn = document.getElementById('clearLogBtn');
const clipboardSyncBtn = document.getElementById('clipboardSyncBtn');
const saveScreenshotBtn = document.getElementById('saveScreenshotBtn');
const sendFileBtn = document.getElementById('sendFileBtn');
const fileInput = document.getElementById('fileInput');

let isConnected = false;
let inputEnabled = false;
let lastFrameImage = null;
let remoteCursor = null; // 远程鼠标位置 { x, y, visible }
let screenWidth = 0; // 远程屏幕宽度
let screenHeight = 0; // 远程屏幕高度
let frameScale = 1; // 画面缩放比例
let frameOffsetX = 0; // 画面在 canvas 上的 X 偏移
let frameOffsetY = 0; // 画面在 canvas 上的 Y 偏移
let selectedHostId = null; // 选中的主机 ID
let relayBaseUrl = ''; // 中继服务器基础 URL

const ctx = desktopCanvas.getContext('2d');

function appendLog(message) {
  const time = new Date().toLocaleTimeString();
  const row = document.createElement('div');
  row.className = 'log-row';
  row.textContent = `[${time}] ${message}`;
  logContainer.appendChild(row);
  logContainer.scrollTop = logContainer.scrollHeight;
}

function setStatus(connected, text) {
  isConnected = connected;
  const dot = statusBadge.querySelector('.dot');
  const label = statusBadge.querySelector('.status-text');

  if (connected) {
    dot.classList.remove('offline');
    dot.classList.add('online');
  } else {
    dot.classList.remove('online');
    dot.classList.add('offline');
  }

  label.textContent = text || (connected ? '已连接' : '未连接');

  connectBtn.disabled = connected;
  disconnectBtn.disabled = !connected;
}

function resizeCanvasToContainer() {
  const rect = desktopContainer.getBoundingClientRect();
  desktopCanvas.width = rect.width;
  desktopCanvas.height = rect.height;

  if (lastFrameImage) {
    drawFrame(lastFrameImage);
  }
}

function drawFrame(dataUrl) {
  lastFrameImage = dataUrl;
  const img = new Image();
  img.onload = () => {
    resizeCanvasToContainer();
    const scale = Math.min(
      desktopCanvas.width / img.width,
      desktopCanvas.height / img.height
    );
    const drawWidth = img.width * scale;
    const drawHeight = img.height * scale;
    const offsetX = (desktopCanvas.width - drawWidth) / 2;
    const offsetY = (desktopCanvas.height - drawHeight) / 2;

    // 保存缩放和偏移信息，用于绘制鼠标
    frameScale = scale;
    frameOffsetX = offsetX;
    frameOffsetY = offsetY;
    screenWidth = img.width;
    screenHeight = img.height;

    ctx.clearRect(0, 0, desktopCanvas.width, desktopCanvas.height);
    ctx.drawImage(img, offsetX, offsetY, drawWidth, drawHeight);

    // 绘制远程鼠标
    drawRemoteCursor();

    resolutionLabel.textContent = `${img.width} x ${img.height}`;
    desktopPlaceholder.style.display = 'none';
  };
  img.onerror = () => {
    appendLog('收到的画面数据无法显示');
  };
  img.src = dataUrl;
}

// 绘制远程鼠标图标
function drawRemoteCursor() {
  if (!remoteCursor || !remoteCursor.visible) return;

  // 将远程屏幕坐标转换为 canvas 坐标
  const canvasX = frameOffsetX + remoteCursor.x * frameScale;
  const canvasY = frameOffsetY + remoteCursor.y * frameScale;

  // 绘制鼠标图标（简单的箭头形状）
  ctx.save();
  ctx.translate(canvasX, canvasY);
  ctx.scale(frameScale, frameScale);

  // 绘制鼠标箭头（白色填充，黑色边框）
  ctx.strokeStyle = '#000000';
  ctx.lineWidth = 1.5;
  ctx.fillStyle = '#ffffff';
  ctx.beginPath();
  // 箭头形状：从 (0,0) 开始
  ctx.moveTo(0, 0);
  ctx.lineTo(8, 6);
  ctx.lineTo(5, 6);
  ctx.lineTo(5, 12);
  ctx.lineTo(2, 12);
  ctx.lineTo(2, 6);
  ctx.lineTo(0, 6);
  ctx.closePath();
  ctx.fill();
  ctx.stroke();

  ctx.restore();
}

function getRelativePosition(event) {
  const rect = desktopCanvas.getBoundingClientRect();
  const x = event.clientX - rect.left;
  const y = event.clientY - rect.top;
  const rx = x / rect.width;
  const ry = y / rect.height;
  return { rx, ry };
}

function handleMouseEvent(type, event) {
  if (!isConnected || !inputEnabled) return;
  const { rx, ry } = getRelativePosition(event);
  window.remote.sendInput({
    type: 'mouse',
    event: type,
    x: rx,
    y: ry,
    button: event.button,
    deltaY: event.deltaY,
  });
}

function handleKeyEvent(type, event) {
  if (!isConnected || !inputEnabled) return;
  // 避免在输入时触发浏览器默认快捷键
  event.preventDefault();
  window.remote.sendInput({
    type: 'keyboard',
    event: type,
    key: event.key,
    code: event.code,
    altKey: event.altKey,
    ctrlKey: event.ctrlKey,
    shiftKey: event.shiftKey,
    metaKey: event.metaKey,
  });
}

// 获取在线主机列表
async function refreshHostsList() {
  const relayUrl = relayUrlInput.value.trim();
  if (!relayUrl) {
    appendLog('请先输入中继服务器地址');
    return;
  }

  try {
    // 转换为 API URL（http://...）
    const apiUrl = relayUrl.replace(/^ws:\/\//, 'http://').replace(/^wss:\/\//, 'https://') + '/api/hosts';
    const response = await fetch(apiUrl);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    
    relayBaseUrl = relayUrl;
    displayHostsList(data.hosts || []);
    appendLog(`找到 ${data.hosts.length} 台在线主机`);
  } catch (e) {
    appendLog(`获取主机列表失败：${e.message || e}`);
    hostsList.innerHTML = '<div class="hosts-placeholder">获取失败，请检查服务器地址</div>';
  }
}

// 显示主机列表
function displayHostsList(hosts) {
  if (hosts.length === 0) {
    hostsList.innerHTML = '<div class="hosts-placeholder">暂无在线主机</div>';
    connectBtn.disabled = true;
    return;
  }

  hostsList.innerHTML = '';
  hosts.forEach(host => {
    const item = document.createElement('div');
    item.className = 'host-item';
    if (selectedHostId === host.id) {
      item.classList.add('selected');
    }
    item.innerHTML = `
      <div class="host-item-name">${host.name || 'Unknown'}</div>
      <div class="host-item-id">ID: ${host.id}</div>
    `;
    item.addEventListener('click', () => {
      // 取消之前选中的
      hostsList.querySelectorAll('.host-item').forEach(el => el.classList.remove('selected'));
      item.classList.add('selected');
      selectedHostId = host.id;
      connectBtn.disabled = false;
      appendLog(`已选择主机：${host.name} (${host.id})`);
    });
    hostsList.appendChild(item);
  });
}

function setupEventBindings() {
  refreshHostsBtn.addEventListener('click', refreshHostsList);

  connectBtn.addEventListener('click', async () => {
    if (!selectedHostId) {
      appendLog('请先选择要连接的主机');
      return;
    }

    const relayUrl = relayUrlInput.value.trim();
    if (!relayUrl) {
      appendLog('请输入中继服务器地址');
      return;
    }

    // 构建 WebSocket URL，包含 hostId 参数
    const wsUrl = relayUrl.replace(/^http:\/\//, 'ws://').replace(/^https:\/\//, 'wss://') + '/ws';
    
    try {
      // 先发送注册消息，指定要连接的 hostId
      await window.remote.connectWithHostId(wsUrl, selectedHostId);
    } catch (e) {
      appendLog(`连接失败：${e.message || e}`);
      setStatus(false, '连接失败');
    }
  });

  disconnectBtn.addEventListener('click', () => {
    window.remote.disconnect();
  });

  inputToggle.addEventListener('change', () => {
    inputEnabled = inputToggle.checked;
    inputLabel.textContent = inputEnabled ? '本地已启用' : '本地禁用';
  });

  clearLogBtn.addEventListener('click', () => {
    logContainer.innerHTML = '';
  });

  clipboardSyncBtn.addEventListener('click', async () => {
    if (!navigator.clipboard || !navigator.clipboard.readText) {
      appendLog('当前环境不支持剪贴板读取');
      return;
    }
    try {
      const text = await navigator.clipboard.readText();
      if (!text) {
        appendLog('剪贴板为空');
        return;
      }
      window.remote.sendClipboard({ text });
      appendLog('已发送本地剪贴板到远程');
    } catch (e) {
      appendLog(`读取剪贴板失败：${e.message || e}`);
    }
  });

  saveScreenshotBtn.addEventListener('click', async () => {
    if (!lastFrameImage) {
      appendLog('当前没有可保存的远程画面');
      return;
    }
    try {
      const result = await window.remote.saveScreenshot(lastFrameImage);
      if (result && result.ok) {
        appendLog(`截屏已保存：${result.path}`);
      } else {
        appendLog(result && result.message ? result.message : '截屏保存失败');
      }
    } catch (e) {
      appendLog(`截屏保存异常：${e.message || e}`);
    }
  });

  sendFileBtn.addEventListener('click', () => {
    if (!isConnected) {
      appendLog('请先连接到远程服务器，再发送文件');
      return;
    }
    fileInput.value = '';
    fileInput.click();
  });

  fileInput.addEventListener('change', () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;

    const maxSize = 10 * 1024 * 1024; // 10MB
    if (file.size > maxSize) {
      appendLog('文件过大，当前示例限制为 10MB 以下');
      return;
    }

    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = reader.result;
      window.remote.sendFile({
        name: file.name,
        size: file.size,
        mime: file.type,
        dataUrl,
      });
    };
    reader.onerror = () => {
      appendLog('读取文件失败');
    };
    reader.readAsDataURL(file);
  });

  window.addEventListener('resize', resizeCanvasToContainer);

  // 鼠标事件
  desktopCanvas.addEventListener('mousemove', (e) =>
    handleMouseEvent('move', e)
  );
  desktopCanvas.addEventListener('mousedown', (e) =>
    handleMouseEvent('down', e)
  );
  desktopCanvas.addEventListener('mouseup', (e) =>
    handleMouseEvent('up', e)
  );
  desktopCanvas.addEventListener('wheel', (e) =>
    handleMouseEvent('wheel', e)
  );

  // 键盘事件：在整个窗口范围监听
  window.addEventListener('keydown', (e) => handleKeyEvent('down', e));
  window.addEventListener('keyup', (e) => handleKeyEvent('up', e));

  // remote 事件回调
  window.remote.onStatus((payload) => {
    setStatus(Boolean(payload.connected), payload.message);
  });

  window.remote.onFrame((image) => {
    appendLog('收到远程画面');
    drawFrame(image);
  });

  window.remote.onCursor((cursorInfo) => {
    // 更新远程鼠标位置
    remoteCursor = cursorInfo;
    // 如果当前有画面，重新绘制鼠标
    if (lastFrameImage) {
      const img = new Image();
      img.onload = () => {
        const scale = Math.min(
          desktopCanvas.width / img.width,
          desktopCanvas.height / img.height
        );
        const drawWidth = img.width * scale;
        const drawHeight = img.height * scale;
        const offsetX = (desktopCanvas.width - drawWidth) / 2;
        const offsetY = (desktopCanvas.height - drawHeight) / 2;

        frameScale = scale;
        frameOffsetX = offsetX;
        frameOffsetY = offsetY;

        ctx.clearRect(0, 0, desktopCanvas.width, desktopCanvas.height);
        ctx.drawImage(img, offsetX, offsetY, drawWidth, drawHeight);
        drawRemoteCursor();
      };
      img.src = lastFrameImage;
    }
  });

  window.remote.onClipboard(async (text) => {
    appendLog('尝试将远程剪贴板同步到本地');
    if (!navigator.clipboard || !navigator.clipboard.writeText) {
      appendLog('当前环境不支持写入本地剪贴板');
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      appendLog('已写入本地剪贴板');
    } catch (e) {
      appendLog(`写入本地剪贴板失败：${e.message || e}`);
    }
  });

  window.remote.onLog((msg) => {
    if (msg) appendLog(msg);
  });

  window.remote.onError((err) => {
    appendLog(`错误：${err && err.message ? err.message : '未知错误'}`);
  });
}

window.addEventListener('DOMContentLoaded', () => {
  resizeCanvasToContainer();
  inputLabel.textContent = '本地禁用';
  setupEventBindings();
  appendLog('客户端已就绪，请先配置服务器地址并点击连接');
});

