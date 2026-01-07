const { app, BrowserWindow, ipcMain, dialog, nativeImage } = require('electron');
const path = require('path');
const fs = require('fs');

let mainWindow = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 1024,
    minHeight: 640,
    backgroundColor: '#0f172a',
    title: 'Remote Control Client',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

app.whenReady().then(() => {
  createWindow();

  ipcMain.handle('save-screenshot', async (event, dataUrl) => {
    try {
      if (!dataUrl || typeof dataUrl !== 'string') {
        return { ok: false, message: '无效的图片数据' };
      }

      const win = BrowserWindow.getFocusedWindow() || mainWindow;
      if (!win) {
        return { ok: false, message: '窗口不存在' };
      }

      const { canceled, filePath } = await dialog.showSaveDialog(win, {
        title: '保存远程桌面截屏',
        defaultPath: 'remote-desktop.png',
        filters: [
          { name: 'PNG Image', extensions: ['png'] },
          { name: 'JPEG Image', extensions: ['jpg', 'jpeg'] },
        ],
      });

      if (canceled || !filePath) {
        return { ok: false, message: '用户取消保存' };
      }

      // 从 data URL 解析图片
      const image = nativeImage.createFromDataURL(dataUrl);
      let buffer;
      const ext = path.extname(filePath).toLowerCase();
      if (ext === '.jpg' || ext === '.jpeg') {
        buffer = image.toJPEG(90);
      } else {
        buffer = image.toPNG();
      }

      fs.writeFileSync(filePath, buffer);
      return { ok: true, message: '保存成功', path: filePath };
    } catch (e) {
      return { ok: false, message: e.message || '保存失败' };
    }
  });

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on('window-all-closed', () => {
  // 只考虑 Windows，关闭所有窗口后退出应用
  app.quit();
});

