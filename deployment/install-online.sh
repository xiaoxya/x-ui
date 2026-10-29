#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
    cat <<'USAGE'
用法：bash install-online.sh [--port 端口] [--version 版本] [--download-only]
默认下载最新正式发行版，面板端口为 2053。
--download-only 只下载和校验安装包，不修改系统。
USAGE
}

port=2053
version=''
download_only=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --port|--version)
            [[ $# -ge 2 && -n "$2" ]] || { usage >&2; exit 2; }
            if [[ $1 == --port ]]; then port=$2; else version=$2; fi
            shift 2 ;;
        --download-only) download_only=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
if [[ ! $port =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
    printf '无效端口，请输入 1 到 65535 之间的整数。\n' >&2
    exit 2
fi
if [[ -n $version && ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]]; then
    printf '版本格式无效，例如 v0.1.1。\n' >&2
    exit 2
fi
if [[ $(uname -s) != Linux ]]; then printf '仅支持 Linux。\n' >&2; exit 1; fi
case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) printf '不支持当前 CPU 架构。\n' >&2; exit 1 ;;
esac
for program in curl sha256sum tar mktemp; do
    if ! command -v "$program" >/dev/null; then
        printf '缺少命令：%s，请先安装。\n' "$program" >&2
        exit 1
    fi
done
if (( ! download_only )) && (( EUID != 0 )) && ! command -v sudo >/dev/null; then
    printf '安装需要 root 权限，请安装 sudo 或使用 root。\n' >&2
    exit 1
fi
asset="x-ui-linux-$arch.tar.gz"
release_url='https://github.com/xiaoxya/x-ui/releases/latest/download'
if [[ -n $version ]]; then
    release_url="https://github.com/xiaoxya/x-ui/releases/download/$version"
fi
umask 077
save_dir=${XUI_INSTALLER_DIR:-"${HOME:?未设置 HOME}/xui-installers"}
mkdir -p -- "$save_dir"
work_dir=$(mktemp -d "$save_dir/xui-XXXXXXXX")
printf '正在下载 %s（%s）到 %s\n' "${version:-最新正式发行版}" "$arch" "$work_dir"
curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 600 --retry 2 -o "$work_dir/$asset.sha256" "$release_url/$asset.sha256"
curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 600 --retry 2 -o "$work_dir/$asset" "$release_url/$asset"
if (( $(wc -c < "$work_dir/$asset.sha256") > 4096 )); then
    printf '校验文件过大。\n' >&2
    exit 1
fi
mapfile -t checksums < "$work_dir/$asset.sha256"
if (( ${#checksums[@]} != 1 )) || [[ ${#checksums[0]} -ne $((64 + 2 + ${#asset})) || ${checksums[0]:64} != "  $asset" || ! ${checksums[0]:0:64} =~ ^[a-f0-9]{64}$ ]]; then
    printf '校验文件格式无效。\n' >&2
    exit 1
fi
printf '%s  %s\n' "${checksums[0]:0:64}" "$work_dir/$asset" | sha256sum -c -
tar -xzf "$work_dir/$asset" -C "$work_dir" --no-same-owner --no-same-permissions
installer="$work_dir/xui-package/install.sh"
if [[ ! -f $installer || ! -x $installer || -L $installer ]]; then
    printf '安装包缺少有效的安装脚本。\n' >&2
    exit 1
fi
printf '安装包已保存到：%s\n' "$work_dir/xui-package"
if (( download_only )); then exit 0; fi
if (( EUID == 0 )); then
    "$installer" "$port"
else
    sudo "$installer" "$port"
fi
printf '安装完成。查看初始凭据：sudo cat /etc/x-ui/initial-credentials\n'
