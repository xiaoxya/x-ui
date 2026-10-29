# Clash/Mihomo 订阅

启用订阅后，在通用订阅地址的订阅 ID 后追加 `/mihomo`；面板的客户端二维码窗口也提供复制入口。地址含订阅凭据，应妥善保管。

返回完整 Mihomo 配置，mixed 代理端口为 2080，仅监听本机；通过节点选择组选择出站。输出为 JSON 格式的 YAML 文档。当前支持部分 VLESS、VMess、Trojan、Shadowsocks 和 Hysteria2 的 TCP、WebSocket、gRPC、HTTPUpgrade 与 TLS 组合；不支持的节点或参数会返回 HTTP 422。基础 VLESS XHTTP 和 Reality 已支持；XHTTP 扩展参数、部分 Reality 参数等尚未覆盖。

运行 `MIHOMO_TEST_BINARY=/path/to/mihomo go test ./sub` 可用客户端验证生成配置。当前语法检查使用 Mihomo v1.19.33；已验证本地 VLESS XHTTP + TLS 端到端连接，其余协议仍需验证。

已使用面板生成的 Hysteria2 链接分别通过 Mihomo 和 sing-box 完成本地 TCP 与 UDP 往返测试；测试使用临时自签名证书及客户端的跳过证书验证选项。

Hysteria2 订阅支持将面板的端口范围和跳跃间隔转换为客户端配置，已使用本地用户态 UDP 转发器验证两个客户端切换入口后的 TCP/UDP 连接；尚未验证实际防火墙转发规则。
