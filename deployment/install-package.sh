#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
    printf '用法：sudo %s [面板端口]\n默认端口：2053\n' "$0"
}

if [[ $# -gt 1 ]]; then usage >&2; exit 2; fi
if [[ ${1:-} == --help || ${1:-} == -h ]]; then usage; exit 0; fi
port=${1:-2053}
if [[ ! $port =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
    printf '无效端口，请输入 1 到 65535 之间的整数。\n' >&2
    exit 2
fi
package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
if [[ ! -f "$package_dir/xui-upgrade" || ! -x "$package_dir/xui-upgrade" ]]; then
    printf '未找到安装工具，请将此脚本与完整安装包放在同一目录。\n' >&2
    exit 1
fi
exec "$package_dir/xui-upgrade" -install -package "$package_dir" -port "$port"
