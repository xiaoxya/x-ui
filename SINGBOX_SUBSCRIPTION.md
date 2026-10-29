# sing-box 订阅

启用现有订阅服务后，在通用订阅地址的订阅 ID 后追加 `/singbox`，例如 `https://example.com:端口/sub/订阅ID/singbox`。入站二维码窗口也提供复制与二维码入口。该地址包含订阅凭据，应妥善保管。

输出是完整 sing-box JSON，包含只监听 127.0.0.1:2080 的 mixed 入站、直接出站和节点选择器。当前实现覆盖部分 VLESS、VMess、Trojan、Shadowsocks 和 Hysteria2 的 TCP、WebSocket、gRPC、HTTPUpgrade 和 TLS 组合。订阅中存在无法转换的启用节点时返回 HTTP 422，避免客户端静默丢失节点；XHTTP、部分 Reality 参数、端口跳跃等尚未支持。

使用 `SINGBOX_TEST_BINARY=/path/to/sing-box go test ./sub` 可在测试中调用客户端的 `check` 命令。配置语法已使用 sing-box 1.14.4 检查；尚未验证所有客户端版本或代理端到端连接。

已使用面板生成的 Hysteria2 链接分别通过 Mihomo 和 sing-box 完成本地 TCP 与 UDP 往返测试；测试使用临时自签名证书及客户端的跳过证书验证选项。

Hysteria2 订阅支持将面板的端口范围和跳跃间隔转换为客户端配置，已使用本地用户态 UDP 转发器验证两个客户端切换入口后的 TCP/UDP 连接；尚未验证实际防火墙转发规则。

sing-box 的跳跃间隔最小为 5 秒；更短的间隔会拒绝生成。
