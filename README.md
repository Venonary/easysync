# EasySync

EasySync 是一个局域网文本同步工具。一台 Windows 或 Linux 设备运行 Go 服务端，其他设备通过浏览器访问同一端口即可发送和查看文本消息。

## 当前功能

- 多设备浏览器访问
- WebSocket 实时文本同步
- JSON 文件持久化历史
- 多会话创建、切换、重命名、删除
- 设备备注和在线设备列表
- 粘贴并发送、复制消息
- Go 服务端托管 Vue 构建产物

## 开发运行

先安装前端依赖并构建：

```powershell
npm.cmd --prefix frontend install
npm.cmd --prefix frontend run build
```

启动服务端：

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go run . -addr :7788
```

服务启动后会打印本机和局域网访问地址，例如：

```text
http://127.0.0.1:7788
http://192.168.1.23:7788
```

## 数据目录

默认数据写入 `data/store.json`。可以通过 `-data` 指定其他目录：

```powershell
go run . -data D:\easysync-data
```

## 下一步建议

- 增加 HTTPS 和自签名证书生成
- 增加 PIN 码认证和设备授权
- 增加图片粘贴、文件上传和下载
- 将 Vue 构建产物 embed 到 Go 可执行文件中
