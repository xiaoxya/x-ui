# Hysteria2 服务端端口跳跃

`xui-porthop` 根据面板数据库中已启用的 Hysteria 入站和其 UDP 跳跃范围，生成专用 nftables NAT 表 `inet xui_porthop`。它不会清空系统的其他规则表。它检查区间重叠、其他已启用入站的端口冲突以及监听地址；有冲突时不执行任何规则变更。

在源码目录构建：

```sh
go build -o xui-porthop ./cmd/xui-porthop
./xui-porthop -db /etc/x-ui/x-ui.db
```

不带 `-apply` 时只打印计划。服务器需要安装支持 `destroy table` 的 nftables，并由具有网络管理权限的进程执行：

```sh
sudo ./xui-porthop -db /etc/x-ui/x-ui.db -apply
```

每次应用都会以一个 nftables 事务替换本工具的专用表；无启用范围时表为空。面板设置中的 UDP Port Hop 页面支持手动同步和查看结果。

需要自动同步时，在面板的 UDP 端口跳跃设置中打开自动同步开关。设置保存在数据库中，修改后无需重启；启用后启动时和每 10 秒会检查数据库中的启用入站，配置变化或上次同步失败时重新应用。关闭自动同步会保留现有规则。旧环境变量 `XUI_PORTHOP_AUTO=1` 仍受支持，但面板开关保存后的选择优先。面板需要执行 nft 的权限。若外部程序清除本工具的规则表，可以使用页面的手动同步恢复。

客户端订阅中的跳跃范围与服务器实际开放的 UDP 端口范围必须一致。

`go test ./porthop ./cmd/xui-porthop` 中的隔离测试使用用户和网络命名空间创建独立的虚拟网卡与规则，验证 IPv4/IPv6 转发、重复应用和无关表保留；环境不允许创建命名空间时会跳过该项。
