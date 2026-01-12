# Host Agent - 被控端程序

## 杀毒软件误报说明

由于本程序需要以下功能，可能被杀毒软件误报为恶意软件：
- 屏幕截图
- 鼠标键盘模拟
- 网络通信
- 剪贴板访问

**这是正常的远程控制软件功能，非恶意行为。**

## 解决方案

### 方法1：添加到Windows Defender白名单（推荐）

以**管理员身份**运行PowerShell，执行：

```powershell
# 添加整个bin目录到排除项
Add-MpPreference -ExclusionPath "D:\remote_control_electron\bin"

# 或只添加host.exe
Add-MpPreference -ExclusionPath "D:\remote_control_electron\bin\host.exe"
```

### 方法2：通过GUI添加排除项

1. 打开 **Windows 安全中心**
2. 点击 **病毒和威胁防护**
3. 点击 **管理设置**
4. 向下滚动到 **排除项**
5. 点击 **添加或删除排除项**
6. 点击 **添加排除项** → **文件夹**
7. 选择 `D:\remote_control_electron\bin` 文件夹

### 方法3：暂时关闭实时保护（不推荐）

仅在编译时使用，编译完成后立即重新启用。

### 方法4：添加版本信息（可选）

安装go-winres工具可以为exe添加版本信息，降低误报率：

```powershell
go install github.com/tc-hib/go-winres@latest
```

然后重新编译：
```powershell
.\build.ps1 build-host
```

## 为什么被标记为威胁？

**敏感API调用：**
- `screenshot.CaptureDisplay()` - 屏幕截图
- `keybd_event()` - 键盘模拟
- `mouse_event()` - 鼠标模拟
- `SetCursorPos()` - 移动鼠标
- `clipboard.WriteAll()` - 剪贴板访问

**网络行为：**
- WebSocket持续连接
- 发送屏幕截图数据
- 自动重连机制

这些都是**合法的远程控制功能**，但行为模式和某些恶意软件相似。

## 其他杀毒软件

如果使用第三方杀毒软件（如卡巴斯基、诺顿等），请查阅其文档添加白名单/信任列表。
