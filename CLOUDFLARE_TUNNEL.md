# Cloudflare Tunnel

面板设置中的 Cloudflare Tunnel 页支持同时管理一个临时隧道和一个远程管理的命名隧道。服务器需自行安装 `cloudflared` 并确保面板进程能在 PATH 找到它；也可用环境变量 `XUI_CLOUDFLARED_BINARY` 指向可执行文件。

在 Debian 或 Ubuntu 上，可运行 `sudo x-ui`，选择“Cloudflare 隧道程序管理”，再选择安装最新版或更新到最新版。菜单使用 Cloudflare 官方 APT 软件源；若检测到非 APT 管理的 `cloudflared`，会提示先检查现有安装。更新后，请在面板停止并重新启动正在运行的隧道。其他发行版请参照 Cloudflare 官方安装说明。

临时隧道填写本地 WebSocket 入站端口，转发到 `http://127.0.0.1:端口`。需要在节点中使用无 TLS 的 WebSocket，状态页会显示 cloudflared 输出的临时域名。临时域名可能随进程重启改变。

命名隧道填写 Cloudflare 提供的 tunnel token；域名及上游服务需要先在 Cloudflare 控制台配置。面板将 token 存于数据库目录的私有文件，并通过环境变量传递给 cloudflared，不在状态接口返回。停止隧道会删除对应配置文件；面板重启时会恢复已启用的隧道，进程异常退出后定期尝试恢复。

临时隧道运行且已取得域名时，面板会为同端口、可通过 IPv4 回环访问的无 TLS WebSocket 入站添加订阅节点，支持 VLESS、VMess 和 Trojan；原节点继续保留。普通链接、Xray JSON、sing-box 和 Mihomo 订阅均可包含该节点。若入站限制了不同的 WebSocket Host、使用 TLS 或监听其他地址，则不会自动添加。隧道停止或临时域名变化后，客户端需刷新订阅。

命名隧道的域名和上游映射由 Cloudflare 控制台管理，面板不会自动把它加入订阅。真实 Cloudflare 临时隧道已建立连接，但当前测试环境未能解析其临时域名，外网端到端访问仍待验证。

当前 Xray 内核不再接受客户端配置中的 `allowInsecure: true`。Xray JSON 订阅中使用自签名证书时，应在入站 TLS 客户端设置里配置实际服务端证书的 `pinnedPeerCertSha256`；若启用了旧选项但未配置指纹，订阅会返回错误。指纹可使用 `xray tls hash --cert server.crt` 计算，证书更新后需要同步更新指纹。
