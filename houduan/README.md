# 后端说明

这是邮箱管理系统的 Go 后端，主要技术栈为 Go + Gin + Ent + PostgreSQL。

后端启动时会自动检查并创建需要的数据表，包括用户、系统设置、邮箱管理、微软邮箱管理、代理系统等相关表。

首次初始化后的默认管理员账号：

```text
admin / admin123
```

系统采用单管理员模式，创建用户接口保持关闭。只在空数据库首次初始化时创建默认管理员；管理员改名或改密后，重启不会恢复成默认账号。

首次登录会强制修改初始密码，成功后换发新会话并直接进入后台。后台仍可修改同一个管理员的用户名和密码，账号 ID 保持不变；管理员不能被删除或禁用。修改用户名、修改密码或重置密码会使旧会话失效，后台修改后需要重新登录。

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

VMess 使用 Mux 转发以兼容 IMAP、POP3、SMTP 由服务端先发送问候的连接，保留节点指定的加密方式；节点服务端需支持标准 VMess Mux。每个后端进程使用独立的临时配置目录，只结束自己启动的 xray，不再按进程名清理其他实例。节点排序保持稳定，避免相同配置因顺序变化反复重启核心。

## 快速取件性能与超时

IMAP、微软快速取件页面先批量获取邮件列表，再按新到旧自动缓存正文，最多同时读取两封，一封完成立即补下一封。点开的邮件优先进入空闲请求位置；已经在读取的正文复用同一个请求，已缓存正文直接显示。关闭正文或切换查看另一封邮件时，后台队列继续运行。

单封失败先跳过，继续处理其他邮件，最多自动重试两次。整批结束后用现有右上角通知显示全部成功，或已缓存数量及失败数量；页面不增加缓存进度区。点开最终失败的邮件可以再次优先重试。取消获取、重新获取、切换账号或模式、退出登录及离开页面会取消旧队列，并忽略旧响应和旧通知。

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
npm run test:mail:browser
npm run build
```

上述回归测试使用合成邮件与模拟请求，不需要真实邮箱或数据库。实际服务器的响应时间与连接限制仍需在相应环境验证。

## IMAP 邮箱续期时间

IMAP 邮箱管理沿用 `mail_accounts.last_renewed_at` 字段。启动时为缺少该字段的数据库补充可空时间列，已有字段和记录保持原值。列表和编辑响应返回续期时间，前端在添加时间下方以较小字号显示，颜色、字重和主题效果与微软邮箱续期行一致；没有记录时不显示续期行。

该字段记录最近一次成功的收件协议操作时间。单个或批量连接测试、后台收件及正文读取、快速取件及其正文接口、纯文本取件、卡密收件成功后更新实际使用的账号；不同分组中同名邮箱不会被一起更新。失败或取消的操作、普通账号和备注编辑不更新时间。

邮箱导入导出保留续期时间及其精度。导入旧格式数据或更早的续期记录时，保留数据库中已有的较新时间。

数据库回归使用下文配置的专用本机测试库，覆盖新增字段、重复升级、旧记录显示、编辑保留、成功与失败操作、同名邮箱隔离和导入导出：

```powershell
go test -run TestDatabaseMailRenewal ./...
```

## 多页面登录回归

前端将 token、过期时间和用户资料作为同一条会话记录保存，兼容迁移原有登录状态。收件页收到跨页面通知时读取当前会话；旧请求的 `401` 只会清理该请求对应的会话，不能清理之后重新登录的会话。

后台认证失败仍返回 `401`，并通过 `X-MailPlus-Auth-Error: session` 标识登录失效。快速取件秘钥失败使用 `X-MailPlus-Auth-Error: quick-mail-key`，避免被误判为后台退出登录。

刷新令牌仅允许调用 `POST /api/auth/refresh`（JSON 字段 `refresh_token`），成功后轮换访问/刷新令牌，旧令牌不可重放；不能直接用于业务接口。`POST /api/auth/logout` 使用 Bearer 访问令牌撤销当前登录及其刷新令牌。取件秘钥管理只允许已完成初始改密的有效管理员访问。

退出或切换登录时会清理管理数据缓存、查询缓存和任务状态，旧页面及旧请求不能把数据写回新会话。两类取件页继续通过同一条会话记录同步登录状态。

在 `qianduan` 目录运行：

```powershell
npm run test:auth
npm run test:auth:browser
```

浏览器测试会构建前端，启动模拟后端，并在独立浏览器配置中打开 IMAP、Outlook 收件页和后台登录页，验证重新登录及旧请求延迟返回的场景；不使用真实邮箱或数据库。需要安装 Edge/Chromium，也可以通过 `MAILPLUS_BROWSER_EXECUTABLE` 指定浏览器程序路径。

## WebDAV 本机和内网地址

管理员配置的 WebDAV 备份支持 HTTP/HTTPS 的本机、局域网和公网服务，包括 `http://localhost:5244/dav`、`http://127.0.0.1:5244/dav`、`http://[::1]:5244/dav` 和局域网 NAS 地址。连接测试、建目录、上传、列举和保留数量清理使用相同的连接策略。

地址由后端进程访问：“本机”指后端所在的系统或容器。Docker 中的 `localhost`、`127.0.0.1` 指容器自身；宿主机服务应使用容器可达的宿主机 IP，或已配置的 `host.docker.internal`。HTTPS 服务仍需使用后端信任的证书。

本地 WebDAV 回归使用临时 HTTP 服务和合成备份文件，验证连接测试、上传、列举与清理；不访问真实 NAS 或数据库：

```powershell
go test -run TestWebDAV ./...
```

WebDAV 密码保存在后端，设置接口返回空密码及 `backup_webdav_password_configured` 状态，浏览器不持久缓存密码。设置页留空保留已保存密码，输入新值替换；清除需要选择“清除已保存密码”并保存。手动备份和连接测试可在相同 URL、用户名下复用已保存密码。

## 数据一致性与回归

后台 POP3 邮件详情使用 POP3 正文读取。IMAP/微软邮箱批量添加使用事务，任意行格式或数据库校验失败时整批回滚，并报告错误行号。卡密在数据库行锁内校验额度、重复消息和扣次，冷却登记同样是原子操作，远程收件期间不持有行锁。余额变化与流水在同一事务中写入，单独修改密码或用户名不会覆盖未提交的余额字段。

手动、定时备份和恢复使用数据库级互斥锁。每份自动保存的备份有唯一文件名，先写临时文件，完整生成后才成为可下载备份；任务记录对应文件名。保留数量清理只处理系统生成的已完成备份。

普通 `go test ./...` 不访问业务数据库；本机具备内置 xray 时会运行本地协议回归。数据库事务测试需显式配置专用测试库，库名必须以 `_test` 结尾且位于本机；每个用例只创建和清理自己的随机 schema：

```powershell
$env:MAILPLUS_TEST_DATABASE_URL="postgres://mailplus_test:CHANGE_ME_TEST_PASSWORD@127.0.0.1:5432/mailplus_fix_test?sslmode=disable"
go test ./...
```

数据库用例覆盖单管理员首次改密、改名、旧会话撤销、禁止删除/禁用管理员、批量导入回滚、并发卡密额度与冷却、余额及流水回滚、备份互斥与取消。前端构建和源码对应的 Docker/Windows 发布副本在发布时另行同步。
