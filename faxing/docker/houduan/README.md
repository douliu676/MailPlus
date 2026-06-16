# 后端说明

这是邮箱管理系统的 Go 后端，主要技术栈为 Go + Gin + Ent + PostgreSQL。

后端启动时会自动检查并创建需要的数据表，包括用户、系统设置、邮箱管理、微软邮箱管理、代理系统等相关表。

首次初始化后的默认管理员账号：

```text
admin / admin123
```

## 启动方式

请先确认 PostgreSQL 已启动，并创建好对应数据库。默认数据库连接为：

```text
postgres://postgres:postgres@localhost:5432/mail_admin?sslmode=disable
```

开发时建议复制一份 `.env`：

```powershell
Copy-Item .env.example .env
go run .
```

后端启动时会自动读取当前目录的 `.env`。也可以通过环境变量 `DATABASE_URL` 指定其他数据库地址：

```powershell
$env:DATABASE_URL="postgres://postgres:postgres@localhost:5432/mail_admin?sslmode=disable"
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
