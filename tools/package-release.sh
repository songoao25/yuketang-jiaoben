#!/usr/bin/env bash
# 打包可发布产物（GitHub Releases 资产），供 App 自更新使用。
#
# 用法：
#   VERSION=1.1.0 bash tools/package-release.sh
# 产物：dist/release/
#   ykt-darwin-arm64.zip      macOS Apple Silicon（内含「雨课助手.app」）
#   ykt-darwin-amd64.zip      macOS Intel
#   ykt-linux-amd64.tar.gz    Linux CLI
#   ykt-windows-amd64.zip     Windows CLI
#   checksums.txt             SHA256 校验（App 更新时强制校验）
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(sed -n 's/var appVersion = "\(.*\)"/\1/p' version.go)}"
[ -n "$VERSION" ] || { echo "✖ 无法确定版本号，请设置 VERSION" >&2; exit 1; }
OUT="${OUT:-dist/release}"
case "$OUT" in /*) ;; *) OUT="$(pwd)/$OUT" ;; esac
WAILS_BIN="${WAILS_BIN:-$HOME/go/bin/wails}"

echo "› 打包版本 v$VERSION → $OUT"
rm -rf "$OUT"
mkdir -p "$OUT"

package_macos() {
  local arch="$1"
  local stage="$OUT/.stage-darwin-$arch"
  mkdir -p "$stage"
  echo "› 构建 macOS/$arch App"
  "$WAILS_BIN" build -platform "darwin/$arch" -ldflags "-X main.appVersion=$VERSION" >/dev/null
  local app="$stage/雨课助手.app"
  rm -rf "$app"
  cp -R build/bin/ykt.app "$app"
  /usr/bin/ditto -c -k --sequesterRsrc --keepParent "$app" "$OUT/ykt-darwin-$arch.zip"
  rm -rf "$stage"
}

package_cli() {
  local goos="$1" goarch="$2" name="$3"
  local stage="$OUT/.stage-$goos-$goarch"
  mkdir -p "$stage"
  echo "› 构建 CLI $goos/$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -tags cli -trimpath -ldflags "-s -w -X main.appVersion=$VERSION" -o "$stage/$name" .
  case "$goos-$goarch" in
    linux-*)
      tar -czf "$OUT/ykt-$goos-$goarch.tar.gz" -C "$stage" "$name" ;;
    *)
      (cd "$stage" && zip -q "$OUT/ykt-$goos-$goarch.zip" "$name") ;;
  esac
  rm -rf "$stage"
}

package_macos arm64
package_macos amd64
package_cli linux amd64 ykt
package_cli windows amd64 ykt.exe

echo "› 生成 checksums.txt"
(cd "$OUT" && shasum -a 256 ykt-*.zip ykt-*.tar.gz > checksums.txt)
echo "✔ 完成："
ls -lh "$OUT"
