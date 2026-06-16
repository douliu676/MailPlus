# Xray 核心文件说明

这个目录内置了 xray-core v26.3.27，用于支持 VMess/VLESS 代理节点。

后端会按当前运行平台自动选择对应核心文件：

```text
Windows 本地开发     -> bin/xray/windows-amd64/xray.exe
Debian/Ubuntu x86_64 -> bin/xray/linux-amd64/xray
Docker amd64         -> bin/xray/linux-amd64/xray
Docker arm64         -> bin/xray/linux-arm64/xray
ARM64 设备           -> bin/xray/linux-arm64/xray
老款 32 位 ARM       -> bin/xray/linux-armv7/xray
```

如果需要使用其他位置的 xray 核心，也可以通过环境变量 `XRAY_BIN` 指定绝对路径。

在 Linux 设备上部署时，如果文件没有执行权限，请执行：

```bash
chmod +x bin/xray/linux-amd64/xray bin/xray/linux-arm64/xray bin/xray/linux-armv7/xray
```
