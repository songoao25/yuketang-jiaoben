#!/usr/bin/env bash
# ykt 安装脚本：构建纯 Go CLI 并安装为全局命令。
#
# 用法：
#   bash install.sh              构建并安装 ykt（默认 ~/.local/bin）
#   bash install.sh --uninstall  卸载 ykt 命令
#
# 桌面 App 请用 `make app`（或 wails build）构建，产物在 build/bin/。
set -euo pipefail

NAME="ykt"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

remove_installed() {
  local removed=0
  for dir in "$HOME/.local/bin" "/usr/local/bin" "$HOME/bin"; do
    if [ -f "$dir/$NAME" ] || [ -L "$dir/$NAME" ]; then
      rm -f "$dir/$NAME"
      echo "✔ 已移除 $dir/$NAME"
      removed=1
    fi
  done
  if [ "$removed" = "0" ]; then
    echo "! 没有找到已安装的 $NAME 命令"
  fi
}

if [ "${1:-}" = "--uninstall" ]; then
  remove_installed
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "✖ 未找到 Go，请先安装 Go 1.26 或更高版本：https://go.dev/dl/" >&2
  exit 1
fi

cd "$SCRIPT_DIR"
echo "› 正在构建 ykt CLI（纯 Go，无需 cgo）..."
mkdir -p bin
go build -tags cli -ldflags "-s -w" -o bin/$NAME .

BIN_DIR=""
for dir in "$HOME/.local/bin" "/usr/local/bin" "$HOME/bin"; do
  if [ -d "$dir" ] && [ -w "$dir" ]; then
    BIN_DIR="$dir"
    break
  fi
done
if [ -z "$BIN_DIR" ]; then
  BIN_DIR="$HOME/.local/bin"
  mkdir -p "$BIN_DIR"
fi

rm -f "$BIN_DIR/$NAME"
install -m 0755 "bin/$NAME" "$BIN_DIR/$NAME"
echo "✔ 已安装命令：$BIN_DIR/$NAME"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    echo ""
    echo "! $BIN_DIR 不在 PATH 中，请把下面一行加入 ~/.zshrc 或 ~/.bashrc："
    echo "  export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

echo ""
echo "现在可以使用："
echo "  $NAME start    # 一键启动（安装脚本 + 打开雨课堂）"
echo "  $NAME help     # 查看全部命令"
echo ""
echo "桌面 App：make app（或 wails build），产物在 build/bin/。"
