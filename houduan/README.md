# 后端说明

这是邮箱管理系统的 Go 后端，主要技术栈为 Go + Gin + Ent + PostgreSQL。

后端启动时会自动检查并创建需要的数据表，包括用户、系统设置、邮箱管理、微软邮箱管理、代理系统等相关表。

首次初始化后的默认管理员账号：

```text
admin / admin123
```

首次登录会强制修改初始密码。

## 启动方式

请先确认 PostgreSQL 已启动，并创建好对应数据库。数据库连接示例：

```text
postgres://mail_admin:CHANGE_ME_STRONG_PASSWORD@localhost:5432/mail_admin?sslmode=disable
```

`CHANGE_ME_STRONG_PASSWORD` 必须替换为真实强密码，不能使用 `postgres/postgres` 或空密码。

开发时建议复制一份 `.env`：

```powershell
Copy-Item .env.example .env
go run .
```

后端启动时会自动读取当前目录的 `.env`。也可以通过环境变量 `DATABASE_URL` 指定其他数据库地址：

```powershell
$env:DATABASE_URL="postgres://mail_admin:CHANGE_ME_STRONG_PASSWORD@localhost:5432/mail_admin?sslmode=disable"
go run .
```

默认监听端口为 `4400`。如需修改端口，可以设置：

```powershell
$env:PORT="4401"
go run .
```

系统环境变量优先级高于 `.env`；Windows 启动器和 Docker Compose 注入的环境变量不会被 `.env` 覆盖。

## 代理系统

后端支持 HTTP、SOCKS5、VMess、VLESS 代理节点。HTTP/SOCKS5 会直接作为代理端点使用；VMess/VLESS 会通过内置 xray 核心转换为本地 SOCKS5 端口。

xray 核心文件放在：

```text
bin/xray/
```

当前已内置以下平台：

```text
Windows amd64 -> bin/xray/windows-amd64/xray.exe
Linux amd64   -> bin/xray/linux-amd64/xray
Linux arm64   -> bin/xray/linux-arm64/xray
Linux armv7   -> bin/xray/linux-armv7/xray
```

如果需要使用其他位置的 xray，可以设置环境变量 `XRAY_BIN` 指向绝对路径。

## 快速取件性能与超时

快速取件页面先批量获取邮件列表，点击邮件时再读取正文，并在当前页面会话内缓存成功结果。取消获取、切换账号或模式、退出页面会取消对应请求；单封正文失败时保留列表并提供重试。

- `POST /api/admin/quick-mail/{imap|outlook}/receive` 新增可选 `include_body`。`false` 返回摘要；省略或 `true` 保持原来的完整正文响应。数量仍按原规则分别应用于收件箱和垃圾箱。
- `POST /api/admin/quick-mail/{imap|outlook}/detail` 按需读取正文，使用与收件接口相同的管理员会话或取件秘钥校验。IMAP 请求携带 `uid`、`uid_validity`、`mailbox`、`folder`；Outlook 携带 `message_id`、`folder`。
- IMAP 列表使用一个连接批量取邮件头并查询实际存在的垃圾箱。完整正文接口最多复用两个连接并发读取，结果保持原顺序；同一账号的并发连接也受限。POP3 保持完整响应并使用单连接读取正文，避免会话锁冲突和跨会话序号变化。
- 整批请求时限为 90 秒，单封正文请求为 30 秒；新的 IMAP 会话及复用正文连接的拨号、TLS/代理握手限时为 10 秒，协议读写按剩余请求时间设置截止时间，最多 30 秒。取消请求会中断 IMAP 正文读取。垃圾箱读取失败时保留已经获取的收件箱，并返回 `warnings`。
- HTTP CONNECT 和 SOCKS5 代理的握手过程也受超时、取消控制；HTTP CONNECT 成功后保持隧道可用，避免在关闭响应体时等待隧道结束。
- 根目录前端请求最多等待 120 秒，Vite 开发代理为 110 秒。发布目录中的代理、源码副本和二进制需在准备发布时另行同步。

后端回归测试使用模拟 IMAP 服务，覆盖 10/30/50 封摘要、50 封正文的连接复用与排序、取消、UIDVALIDITY 变化、部分失败和接口鉴权：

```powershell
go test ./...
```

前端在 `qianduan` 目录使用 Node.js 22.18 或更新版本验证请求超时、取消、正文缓存、重试及旧请求隔离：

```powershell
npm run test:quick-mail
npm run build
```

上述回归测试使用合成邮件与模拟请求，不需要真实邮箱或数据库。实际服务器的响应时间与连接限制仍需在相应环境验证。
