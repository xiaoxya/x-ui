# x-ui-m

## 安装教程

以下步骤适用于全新安装到使用 systemd 的 Linux 服务器。构建机器需要 Go（版本符合 `go.mod` 的要求）、GCC、Bash 和 `sha256sum`。构建机器与目标服务器须使用相同的 CPU 架构；当前构建产物依赖系统 glibc，建议在与目标服务器相同或较旧的兼容系统上构建。

1. 获取源码，并从 [Xray 官方发布页](https://github.com/XTLS/Xray-core/releases)准备适合目标架构的 Xray 可执行文件，以及 `geoip.dat` 和 `geosite.dat`。以下路径需要替换为实际路径：

   ```bash
   git clone https://github.com/xiaoxya/x-ui.git
   cd x-ui
   chmod +x /path/to/xray
   ```

2. 构建部署包。输出目录必须事先不存在；资源目录中应包含上述两个 `.dat` 文件：

   ```bash
   bash deployment/build-local.sh \
     /path/to/xui-package \
     /path/to/xray \
     /path/to/xray-assets
   ```

   可以在性能较好的 Linux 机器构建后，将整个部署包上传到目标服务器。目标服务器无需安装 Go 或 GCC。建议将部署包保存在磁盘目录，留作中断恢复使用。

3. 在目标服务器上安装。下面以包所在位置 `/home/user/xui-package` 和面板端口 `2053` 为例：

   ```bash
   sudo /home/user/xui-package/xui-upgrade \
     -install -package /home/user/xui-package -port 2053
   ```

   安装会拒绝覆盖已有的面板、数据库、管理命令或同名 systemd 服务。请按需在服务器防火墙和云平台安全组放行面板 TCP 端口。

4. 查看随机生成的登录凭据和访问路径：

   ```bash
   sudo cat /etc/x-ui/initial-credentials
   ```

   使用服务器地址、输出的端口和路径访问面板。首次安装默认使用 HTTP；在可信网络以外使用前，请配置 HTTPS。保存凭据后可删除该凭据文件。

5. 管理面板：

   ```bash
   sudo x-ui          # 打开管理菜单
   sudo x-ui status   # 查看状态
   sudo x-ui restart  # 重启面板
   sudo x-ui log      # 查看日志
   ```

面板程序默认安装在 `/usr/local/x-ui/`，数据库位于 `/etc/x-ui/x-ui.db`。

## 原项目与许可

本项目修改自 [alireza0/x-ui](https://github.com/alireza0/x-ui)，保留原项目的版权声明，并遵循 GNU General Public License v3.0。完整许可见 [LICENSE](LICENSE)。
