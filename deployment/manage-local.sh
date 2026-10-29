#!/usr/bin/env bash
set -Eeuo pipefail

usage() { printf 'Usage: %s install|upgrade|recover PACKAGE_DIR [PORT]\n' "$0" >&2; }
if [[ $# -lt 2 ]]; then usage; exit 2; fi
mode=$1
package_dir=$2
shift 2
case "$mode" in
    install) if [[ $# -gt 1 ]]; then usage; exit 2; fi ;;
    upgrade|recover) if [[ $# -ne 0 ]]; then usage; exit 2; fi ;;
    *) usage; exit 2 ;;
esac
if [[ ! -d "$package_dir" || ! -x "$package_dir/xui-upgrade" || ! -f "$package_dir/xui-upgrade" ]]; then
    printf 'Local package does not contain an executable xui-upgrade\n' >&2
    exit 1
fi
package_dir=$(cd -- "$package_dir" && pwd -P)
case "$mode" in
    install) exec "$package_dir/xui-upgrade" -install -package "$package_dir" -port "${1:-2053}" ;;
    upgrade) exec "$package_dir/xui-upgrade" -package "$package_dir" ;;
    recover) exec "$package_dir/xui-upgrade" -recover ;;
esac
