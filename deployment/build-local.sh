#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
    printf 'Usage: %s OUTPUT_DIRECTORY XRAY_BINARY XRAY_ASSETS_DIRECTORY\n' "$0" >&2
}

if [[ $# -ne 3 ]]; then usage; exit 2; fi
output=$1
xray_binary=$2
assets=$3
if [[ -e "$output" || -L "$output" ]]; then printf 'Output already exists: %s\n' "$output" >&2; exit 1; fi
if [[ ! -x "$xray_binary" || ! -f "$xray_binary" ]]; then printf 'Xray binary is not executable\n' >&2; exit 1; fi
for name in geoip.dat geosite.dat; do
    if [[ ! -s "$assets/$name" ]]; then printf 'Missing asset: %s\n' "$assets/$name" >&2; exit 1; fi
done
version=$(
    "$xray_binary" version
)
if [[ "$version" != Xray\ * ]]; then printf 'Invalid Xray binary version output\n' >&2; exit 1; fi
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
if [[ $(go env GOOS) != linux ]]; then printf 'Only native Linux builds are supported\n' >&2; exit 1; fi
arch=$(go env GOARCH)
case "$arch" in amd64|arm64|arm|386|s390x) ;; *) printf 'Unsupported architecture: %s\n' "$arch" >&2; exit 1 ;; esac
case "$(uname -m):$arch" in
    x86_64:amd64|aarch64:arm64|armv*:arm|i?86:386|s390x:s390x) ;;
    *) printf 'Cross builds are not supported by this packager\n' >&2; exit 1 ;;
esac

mkdir -- "$output"
output=$(cd -- "$output" && pwd -P)
success=0
cleanup() { if [[ $success -eq 0 ]]; then rm -rf -- "$output"; fi; }
trap cleanup EXIT
mkdir -- "$output/bin"
(cd -- "$root" && go build -buildvcs=false -ldflags '-w -s' -o "$output/x-ui" . && go build -buildvcs=false -ldflags '-w -s' -o "$output/xui-porthop" ./cmd/xui-porthop && go build -buildvcs=false -ldflags '-w -s' -o "$output/xui-db-snapshot" ./cmd/xui-db-snapshot && go build -buildvcs=false -ldflags '-w -s' -o "$output/xui-upgrade" ./cmd/xui-upgrade)
cp -- "$xray_binary" "$output/bin/xray-linux-$arch"
chmod 0755 "$output/bin/xray-linux-$arch"
for name in geoip.dat geosite.dat geoip_IR.dat geosite_IR.dat; do
    if [[ -f "$assets/$name" ]]; then cp -- "$assets/$name" "$output/bin/$name"; fi
done
cp -- "$root/x-ui.sh" "$root/x-ui.service" "$root/LICENSE" "$root/CLOUDFLARE_TUNNEL.md" "$root/PORTHOP.md" "$output/"
cp -- "$root/deployment/manage-local.sh" "$output/xui-manage-local"
cp -- "$root/deployment/install-package.sh" "$output/install.sh"
chmod 0755 "$output/x-ui.sh" "$output/xui-manage-local" "$output/install.sh"
printf '%s\n' "$version" > "$output/XRAY_VERSION.txt"
files=(install.sh x-ui x-ui.sh x-ui.service xui-porthop xui-db-snapshot xui-upgrade xui-manage-local LICENSE CLOUDFLARE_TUNNEL.md PORTHOP.md XRAY_VERSION.txt "bin/xray-linux-$arch" bin/geoip.dat bin/geosite.dat)
for name in bin/geoip_IR.dat bin/geosite_IR.dat; do
    if [[ -f "$output/$name" ]]; then files+=("$name"); fi
done
(cd -- "$output" && sha256sum -- "${files[@]}" > MANIFEST.sha256)
success=1
printf 'Built local package: %s\n' "$output"
