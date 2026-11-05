#!/usr/bin/env bash
# ykt 端到端冒烟测试（独立 QA 用，可重复运行）
#
# 覆盖：
#   1. 纯 Go CLI 构建（go build -tags cli）、go vet、linux/windows 交叉编译
#   2. version / status 输出与脚本内嵌一致性
#   3. 后台服务生命周期：start --no-open → server.json → /health → stop → 端口释放
#   4. HTTP 路由：/health、/ykt.user.js、/、/favicon.ico、404、405
#   5. /api/config 安全：无 X-YKT-Client 403、带头 200、同步关闭不泄露 Key、
#      configured 语义（无配置 / 只有 settings / 有 ai）、CORS 白名单
#   6. 安装记录 state.json、文件权限 0600/0700、日志不落 Key
#   7. 边界：未知命令/参数退出码、端口范围拒绝、过期 server.json 清理、
#      端口顺延、前台 serve 的 SIGTERM 优雅退出
#
# 约定：
#   - 全部状态写入 mktemp 临时目录（YKT_HOME），绝不接触真实 ~/.ykt
#   - 只用 --no-open / serve 等不弹浏览器的调用；不启动 GUI
#   - PASS/FAIL/SKIP 统计；FAIL > 0 时以非零码退出
#   - 需要：bash、go、curl、lsof、shasum 或 sha256sum；端口顺延用例需要 python3（缺失则 SKIP）
#
# 用法：bash tests/smoke.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
SKIP=0

ok()   { PASS=$((PASS + 1)); printf 'PASS  %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL  %s\n' "$1"; }
skip() { SKIP=$((SKIP + 1)); printf 'SKIP  %s\n' "$1"; }

finish() {
  printf '\n========================================\n'
  printf '冒烟结果：PASS=%d  FAIL=%d  SKIP=%d\n' "$PASS" "$FAIL" "$SKIP"
  printf '临时目录（已清理）：%s\n' "$WORK"
  printf '========================================\n'
  if [ "$FAIL" -gt 0 ]; then
    exit 1
  fi
  exit 0
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/ykt-smoke.XXXXXX")"
WORK="$(cd "$WORK" && pwd -P)"
export YKT_HOME="$WORK/home"
mkdir -p "$YKT_HOME"
BIN="$WORK/ykt"
MAIN_PID=""
FG_PID=""
DUMMY_PID=""

# health_ok <port>：输出 /health 响应体（失败输出空）
health_body() {
  curl -s --max-time 2 "http://127.0.0.1:$1/health" 2>/dev/null || true
}

# cleanup：只终止由本脚本临时 YKT_HOME 记录、且 /health 自报为 ykt 的进程
cleanup() {
  set +e
  if [ -n "$FG_PID" ] && kill -0 "$FG_PID" 2>/dev/null; then
    kill -TERM "$FG_PID" 2>/dev/null
  fi
  if [ -n "$DUMMY_PID" ] && kill -0 "$DUMMY_PID" 2>/dev/null; then
    kill "$DUMMY_PID" 2>/dev/null
  fi
  if [ -f "$YKT_HOME/server.json" ]; then
    local port pid
    port="$(json_int "$YKT_HOME/server.json" port)"
    pid="$(json_int "$YKT_HOME/server.json" pid)"
    case "$(health_body "$port")" in
      *'"app":"ykt"'*)
        [ -n "$pid" ] && kill -TERM "$pid" 2>/dev/null
        ;;
    esac
  fi
  [ -n "$MAIN_PID" ] && kill -TERM "$MAIN_PID" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------- 小工具
json_int() { # json_int <file> <key>
  sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$1" 2>/dev/null | head -n 1
}
json_str() { # json_str <file> <key>
  sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$1" 2>/dev/null | head -n 1
}
sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}
file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}
port_listening() { # 0 = 有 LISTEN
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}
port_free() { ! port_listening "$1"; }
http_status() { # http_status <url> [curl args...]
  local url="$1"; shift
  curl -s -o "$WORK/http-body.txt" -w '%{http_code}' --max-time 3 "$@" "$url" || echo '000'
}
header_has() { # header_has <file> <name> <value-substr>
  grep -i "^$2:" "$1" 2>/dev/null | grep -qi -- "$3"
}
body_has() { grep -q -- "$1" "$WORK/http-body.txt" 2>/dev/null; }
wait_health() { # wait_health <port> <tenths>
  local port="$1" tries="${2:-40}" i=0
  while [ "$i" -lt "$tries" ]; do
    case "$(health_body "$port")" in
      *'"app":"ykt"'*) return 0 ;;
    esac
    sleep 0.1
    i=$((i + 1))
  done
  return 1
}
find_free_port() {
  local p
  for p in $(seq 8899 8910); do
    if port_free "$p"; then
      echo "$p"
      return 0
    fi
  done
  return 1
}

printf 'ykt 冒烟测试\n'
printf '仓库：%s\n' "$ROOT"
printf 'Go：%s\n' "$(go version 2>/dev/null || echo '未找到')"
printf '临时 YKT_HOME：%s\n\n' "$YKT_HOME"

# ---------------------------------------------------------------- 1. 构建
if go build -tags cli -ldflags '-s -w' -o "$BIN" . >"$WORK/build.log" 2>&1; then
  ok "[01] go build -tags cli 构建纯 Go CLI"
else
  bad "[01] go build -tags cli 构建失败（日志见下）"
  sed 's/^/      /' "$WORK/build.log" || true
  finish
fi

if go vet -tags cli ./... >"$WORK/vet.log" 2>&1; then
  ok "[02] go vet -tags cli ./..."
else
  bad "[02] go vet -tags cli ./... 失败"
  sed 's/^/      /' "$WORK/vet.log" || true
fi

if GOOS=linux GOARCH=amd64 go build -tags cli -o "$WORK/ykt-linux-amd64" . >"$WORK/build-linux.log" 2>&1; then
  ok "[03] 交叉编译 linux/amd64"
else
  bad "[03] 交叉编译 linux/amd64 失败"
  sed 's/^/      /' "$WORK/build-linux.log" || true
fi

if GOOS=windows GOARCH=amd64 go build -tags cli -o "$WORK/ykt-windows-amd64.exe" . >"$WORK/build-windows.log" 2>&1; then
  ok "[04] 交叉编译 windows/amd64"
else
  bad "[04] 交叉编译 windows/amd64 失败"
  sed 's/^/      /' "$WORK/build-windows.log" || true
fi

# ---------------------------------------------------------------- 2. version / status
SCRIPT_VERSION="$(sed -n 's|^// @version[[:space:]]*||p' yuketang.js | head -n 1)"
SCRIPT_HASH="$(sha256_of yuketang.js)"
SCRIPT_SHORT="${SCRIPT_HASH:0:8}"
VERSION_RC=0
VERSION_OUT="$("$BIN" version 2>&1)" || VERSION_RC=$?
if [ "$VERSION_RC" -eq 0 ]; then
  ok "[05] ykt version 退出码 0"
else
  bad "[05] ykt version 退出码 $VERSION_RC（期望 0）"
fi
case "$VERSION_OUT" in
  *"脚本 v$SCRIPT_VERSION"*) ok "[06] version 输出脚本版本 v$SCRIPT_VERSION" ;;
  *) bad "[06] version 缺少脚本版本 v$SCRIPT_VERSION（实际：$VERSION_OUT）" ;;
esac
case "$VERSION_OUT" in
  *"指纹 $SCRIPT_SHORT"*) ok "[07] version 输出指纹短码 $SCRIPT_SHORT" ;;
  *) bad "[07] version 缺少指纹短码 $SCRIPT_SHORT（实际：$VERSION_OUT）" ;;
esac

STATUS_RC=0
STATUS_OUT="$("$BIN" status 2>&1)" || STATUS_RC=$?
if [ "$STATUS_RC" -eq 0 ]; then
  ok "[08] ykt status（未启动服务）退出码 0"
else
  bad "[08] ykt status 退出码 $STATUS_RC（期望 0）"
fi
case "$STATUS_OUT" in
  *"未运行"*) ok "[09] status 显示本地服务未运行" ;;
  *) bad "[09] status 未显示「未运行」" ;;
esac
case "$STATUS_OUT" in
  *"$YKT_HOME"*) ok "[10] status 使用临时 YKT_HOME（不碰真实 ~/.ykt）" ;;
  *) bad "[10] status 配置路径未指向临时 YKT_HOME" ;;
esac

# ---------------------------------------------------------------- 3. 启动后台服务
PORT="$(find_free_port || true)"
if [ -z "$PORT" ]; then
  bad "[11] 8899-8910 没有空闲端口，无法继续端到端用例"
  finish
fi

START_RC=0
START_OUT="$("$BIN" start --no-open -p "$PORT" 2>&1)" || START_RC=$?
if [ "$START_RC" -eq 0 ]; then
  ok "[11] ykt start --no-open -p $PORT 退出码 0"
else
  bad "[11] ykt start --no-open -p $PORT 退出码 $START_RC"
  printf '%s\n' "$START_OUT" | sed 's/^/      /'
fi
case "$START_OUT" in
  *'[no-open]'*) ok "[12] --no-open 只打印地址，不调用浏览器" ;;
  *) bad "[12] --no-open 输出缺少 [no-open] 标记" ;;
esac

# 等待 server.json 出现（最多 5 秒）
i=0
while [ ! -f "$YKT_HOME/server.json" ] && [ "$i" -lt 50 ]; do
  sleep 0.1
  i=$((i + 1))
done
if [ -f "$YKT_HOME/server.json" ] && grep -q '"owner": "daemon"' "$YKT_HOME/server.json"; then
  ok "[13] server.json 已写入（owner=daemon）"
else
  bad "[13] server.json 未写入或缺 owner=daemon"
  finish
fi

ACTUAL_PORT="$(json_int "$YKT_HOME/server.json" port)"
MAIN_PID="$(json_int "$YKT_HOME/server.json" pid)"
if [ "$ACTUAL_PORT" = "$PORT" ]; then
  ok "[14] 服务监听请求端口 $PORT"
else
  bad "[14] 服务端口 = ${ACTUAL_PORT:-空}，期望 $PORT"
fi
if [ -n "$MAIN_PID" ] && kill -0 "$MAIN_PID" 2>/dev/null; then
  ok "[15] server.json 记录的 PID $MAIN_PID 存活"
else
  bad "[15] server.json 记录的 PID 不存活（pid=${MAIN_PID:-空}）"
fi
HOME_MODE="$(file_mode "$YKT_HOME")"
if [ "$HOME_MODE" = "700" ]; then
  ok "[16] YKT_HOME 目录权限 0700"
else
  bad "[16] YKT_HOME 目录权限 = $HOME_MODE（期望 700）"
fi
SERVER_MODE="$(file_mode "$YKT_HOME/server.json")"
if [ "$SERVER_MODE" = "600" ]; then
  ok "[17] server.json 权限 0600"
else
  bad "[17] server.json 权限 = $SERVER_MODE（期望 600）"
fi

# ---------------------------------------------------------------- 4. HTTP 接口
HEALTH_STATUS="$(curl -s -D "$WORK/health-headers.txt" -o "$WORK/health-body.txt" -w '%{http_code}' --max-time 3 "http://127.0.0.1:$PORT/health" || echo '000')"
HEALTH_BODY="$(cat "$WORK/health-body.txt")"
if [ "$HEALTH_STATUS" = "200" ]; then
  ok "[18] GET /health 200"
else
  bad "[18] GET /health = $HEALTH_STATUS（期望 200）"
fi
case "$HEALTH_BODY" in
  *'"app":"ykt"'*) ok "[19] /health app=ykt" ;;
  *) bad "[19] /health 缺少 app=ykt" ;;
esac
case "$HEALTH_BODY" in
  *"\"hash\":\"$SCRIPT_HASH\""*) ok "[20] /health 指纹与仓库 yuketang.js 一致" ;;
  *) bad "[20] /health 指纹与仓库 yuketang.js 不一致" ;;
esac
case "$HEALTH_BODY" in
  *"\"port\":$PORT"*) ok "[21] /health 回显端口 $PORT" ;;
  *) bad "[21] /health 端口不是 $PORT" ;;
esac
if header_has "$WORK/health-headers.txt" 'Content-Type' 'application/json'; then
  ok "[22] /health Content-Type 为 JSON"
else
  bad "[22] /health Content-Type 非 JSON"
fi

# 下载内嵌脚本并校验
SCRIPT_RC=0
curl -s -D "$WORK/script-headers.txt" -o "$WORK/downloaded.js" --max-time 5 "http://127.0.0.1:$PORT/ykt.user.js" || SCRIPT_RC=$?
if [ "$SCRIPT_RC" -eq 0 ] && [ -s "$WORK/downloaded.js" ]; then
  ok "[23] GET /ykt.user.js 返回非空内容"
else
  bad "[23] GET /ykt.user.js 失败（rc=$SCRIPT_RC）"
fi
if [ "$(sha256_of "$WORK/downloaded.js")" = "$SCRIPT_HASH" ]; then
  ok "[24] 内嵌脚本与仓库 yuketang.js 完全一致"
else
  bad "[24] 内嵌脚本与仓库 yuketang.js 不一致"
fi
if header_has "$WORK/script-headers.txt" 'Content-Type' 'application/javascript'; then
  ok "[25] /ykt.user.js Content-Type 为 application/javascript"
else
  bad "[25] /ykt.user.js Content-Type 错误"
fi
if header_has "$WORK/script-headers.txt" 'Cache-Control' 'no-store'; then
  ok "[26] /ykt.user.js Cache-Control: no-store"
else
  bad "[26] /ykt.user.js 缺少 Cache-Control: no-store"
fi

# 安装记录
if [ -f "$YKT_HOME/state.json" ]; then
  ok "[27] 拉取脚本后写入 state.json 安装记录"
  STATE_HASH="$(json_str "$YKT_HOME/state.json" hash)"
  STATE_VERSION="$(json_str "$YKT_HOME/state.json" version)"
  if [ "$STATE_HASH" = "$SCRIPT_HASH" ] && [ "$STATE_VERSION" = "$SCRIPT_VERSION" ]; then
    ok "[28] state.json 记录当前脚本版本与指纹"
  else
    bad "[28] state.json 版本/指纹不符（version=$STATE_VERSION hash=${STATE_HASH:0:8}）"
  fi
  if [ "$(file_mode "$YKT_HOME/state.json")" = "600" ]; then
    ok "[29] state.json 权限 0600"
  else
    bad "[29] state.json 权限非 0600"
  fi
else
  bad "[27] 拉取脚本后未写入 state.json"
fi

# /api/config：无客户端头必须 403
API_STATUS="$(http_status "http://127.0.0.1:$PORT/api/config")"
if [ "$API_STATUS" = "403" ]; then
  ok "[30] /api/config 无 X-YKT-Client 头返回 403"
else
  bad "[30] /api/config 无头 = $API_STATUS（期望 403）"
fi
if body_has '"error":"forbidden"'; then
  ok "[31] 403 响应 JSON 含 forbidden"
else
  bad "[31] 403 响应不是预期的 JSON"
fi

# /api/config：带头 200，初始未配置
API_STATUS="$(curl -s -D "$WORK/api-headers.txt" -o "$WORK/http-body.txt" -w '%{http_code}' --max-time 3 \
  -H 'X-YKT-Client: userscript' "http://127.0.0.1:$PORT/api/config" || echo '000')"
if [ "$API_STATUS" = "200" ]; then
  ok "[32] /api/config 带头返回 200"
else
  bad "[32] /api/config 带头 = $API_STATUS（期望 200）"
fi
if body_has '"app":"ykt"' && body_has '"sync":{"enabled":true}' && body_has '"configured":false' \
  && body_has '"hasAI":false' && body_has '"hasFeatures":false'; then
  ok "[33] 未保存配置时 configured/hasAI/hasFeatures 均为 false 且 sync.enabled=true"
else
  bad "[33] 初始 /api/config 字段不符合预期"
fi
if body_has '"ai":{"url":"","key":""' || body_has '"key":""'; then
  ok "[34] 未保存配置时不返回 AI Key"
else
  bad "[34] 未保存配置时响应异常"
fi
if header_has "$WORK/api-headers.txt" 'Cache-Control' 'no-store'; then
  ok "[35] /api/config Cache-Control: no-store"
else
  bad "[35] /api/config 缺少 Cache-Control: no-store"
fi

# 只有 settings（契约要求 configured 必须为 false）
cat >"$YKT_HOME/config.json" <<'JSON'
{
  "settings": {
    "port": 8899,
    "defaultUrl": "https://yuketang.cn/",
    "autoStart": true,
    "syncEnabled": true,
    "appearance": "system"
  }
}
JSON
chmod 600 "$YKT_HOME/config.json"
http_status "http://127.0.0.1:$PORT/api/config" -H 'X-YKT-Client: userscript' >/dev/null
if body_has '"configured":false' && body_has '"hasAI":false' && body_has '"hasFeatures":false'; then
  ok "[36] config.json 只有 settings 时 configured/hasAI/hasFeatures 均为 false"
else
  bad "[36] 只有 settings 时 configured/hasAI/hasFeatures 均应为 false"
fi

# 有 ai + features、但同步关闭：configured=true 且不得泄露 Key
cat >"$YKT_HOME/config.json" <<'JSON'
{
  "settings": {
    "port": 8899,
    "defaultUrl": "https://changjiang.yuketang.cn/",
    "autoStart": true,
    "syncEnabled": false,
    "appearance": "dark"
  },
  "ai": {
    "url": "https://api.deepseek.com/chat/completions",
    "key": "sk-smoke-should-not-leak",
    "model": "deepseek-chat",
    "apiFormat": "openai",
    "authMethod": "bearer"
  },
  "features": { "autoAI": true, "autoComment": true }
}
JSON
chmod 600 "$YKT_HOME/config.json"
http_status "http://127.0.0.1:$PORT/api/config" -H 'X-YKT-Client: userscript' >/dev/null
if body_has '"configured":true' && body_has '"hasAI":true' && body_has '"hasFeatures":true' \
  && body_has '"sync":{"enabled":false}'; then
  ok "[37] 已保存配置且同步关闭时 configured/hasAI/hasFeatures=true、sync.enabled=false"
else
  bad "[37] 已保存配置的 configured/hasAI/hasFeatures/sync 字段不符合预期"
fi
if body_has 'sk-smoke-should-not-leak'; then
  bad "[38] 同步关闭时响应泄露了 AI Key"
else
  ok "[38] 同步关闭时响应不泄露 AI Key"
fi
if body_has '"autoAI":false' && body_has '"autoComment":false'; then
  ok "[39] 同步关闭时功能开关返回零值"
else
  bad "[39] 同步关闭时功能开关未返回零值"
fi

# 打开同步：Key 应随配置下发
sed -i.bak 's/"syncEnabled": false/"syncEnabled": true/' "$YKT_HOME/config.json"
rm -f "$YKT_HOME/config.json.bak"
http_status "http://127.0.0.1:$PORT/api/config" -H 'X-YKT-Client: userscript' >/dev/null
if body_has 'sk-smoke-should-not-leak' && body_has '"autoAI":true'; then
  ok "[40] 开启同步后 /api/config 下发 AI Key 与功能开关"
else
  bad "[40] 开启同步后未下发配置"
fi

# CORS 白名单
curl -s -D "$WORK/cors-ok.txt" -o /dev/null --max-time 3 \
  -H 'X-YKT-Client: userscript' -H 'Origin: https://www.yuketang.cn' \
  "http://127.0.0.1:$PORT/api/config" || true
if header_has "$WORK/cors-ok.txt" 'Access-Control-Allow-Origin' 'https://www.yuketang.cn'; then
  ok "[41] CORS 允许的 Origin 原样回写 ACAO"
else
  bad "[41] CORS 允许的 Origin 未回写 ACAO"
fi
if header_has "$WORK/cors-ok.txt" 'Vary' 'Origin'; then
  ok "[42] CORS 响应带 Vary: Origin"
else
  bad "[42] CORS 响应缺少 Vary: Origin"
fi
curl -s -D "$WORK/cors-bad.txt" -o /dev/null --max-time 3 \
  -H 'X-YKT-Client: userscript' -H 'Origin: https://evil.example.com' \
  "http://127.0.0.1:$PORT/api/config" || true
if grep -qi '^Access-Control-Allow-Origin:' "$WORK/cors-bad.txt"; then
  bad "[43] 非白名单 Origin 不应回写 ACAO"
else
  ok "[43] 非白名单 Origin 不回写 ACAO"
fi

# 其他路由/方法
if [ "$(http_status "http://127.0.0.1:$PORT/favicon.ico")" = "204" ]; then
  ok "[44] GET /favicon.ico 204"
else
  bad "[44] GET /favicon.ico 非 204"
fi
if [ "$(http_status "http://127.0.0.1:$PORT/")" = "200" ] && body_has '本地控制台'; then
  ok "[45] GET / 返回本地控制台页面"
else
  bad "[45] GET / 页面异常"
fi
if [ "$(http_status "http://127.0.0.1:$PORT/no-such-path")" = "404" ]; then
  ok "[46] 未知路径返回 404"
else
  bad "[46] 未知路径未返回 404"
fi
if [ "$(http_status "http://127.0.0.1:$PORT/api/config" -X POST -H 'X-YKT-Client: userscript')" = "405" ]; then
  ok "[47] POST /api/config 返回 405"
else
  bad "[47] POST /api/config 未返回 405"
fi

# 日志：JSON Lines、不落 Key、0600
if [ -f "$YKT_HOME/server.log" ] && [ "$(file_mode "$YKT_HOME/server.log")" = "600" ]; then
  ok "[48] server.log 存在且权限 0600"
else
  bad "[48] server.log 缺失或权限非 0600"
fi
if grep -q 'sk-smoke-should-not-leak' "$YKT_HOME/server.log" 2>/dev/null; then
  bad "[49] server.log 泄露了 AI Key"
else
  ok "[49] server.log 不含 AI Key"
fi
FIRST_LOG_LINE="$(head -n 1 "$YKT_HOME/server.log" 2>/dev/null || true)"
case "$FIRST_LOG_LINE" in
  '{'*'"level"'*'"message"'*'}') ok "[50] server.log 为 JSON Lines 格式" ;;
  *) bad "[50] server.log 首行不是 JSON Lines（实际：$FIRST_LOG_LINE）" ;;
esac

# 运行中 status
STATUS_OUT="$("$BIN" status 2>&1 || true)"
case "$STATUS_OUT" in
  *"运行中"*) ok "[51] status 显示服务运行中" ;;
  *) bad "[51] 服务运行中时 status 未显示「运行中」" ;;
esac
case "$STATUS_OUT" in
  *"PID $MAIN_PID"*) ok "[52] status 显示服务 PID $MAIN_PID" ;;
  *) bad "[52] status 未显示正确 PID" ;;
esac

# ---------------------------------------------------------------- 5. stop 清理
STOP_RC=0
STOP_OUT="$("$BIN" stop 2>&1)" || STOP_RC=$?
if [ "$STOP_RC" -eq 0 ]; then
  ok "[53] ykt stop 退出码 0"
else
  bad "[53] ykt stop 退出码 $STOP_RC"
fi
case "$STOP_OUT" in
  *"已停止"*) ok "[54] stop 输出「已停止」" ;;
  *) bad "[54] stop 未输出「已停止」（实际：$STOP_OUT）" ;;
esac
sleep 0.3
if [ ! -f "$YKT_HOME/server.json" ]; then
  ok "[55] stop 后清理 server.json"
else
  bad "[55] stop 后 server.json 仍存在"
fi
if [ -n "$MAIN_PID" ] && kill -0 "$MAIN_PID" 2>/dev/null; then
  bad "[56] stop 后进程 $MAIN_PID 仍存活"
else
  ok "[56] stop 后后台进程已退出"
fi
i=0
while port_listening "$PORT" && [ "$i" -lt 30 ]; do
  sleep 0.1
  i=$((i + 1))
done
if port_free "$PORT"; then
  ok "[57] stop 后端口 $PORT 已释放"
else
  bad "[57] stop 后端口 $PORT 仍被监听"
fi
STOP2_RC=0
STOP2_OUT="$("$BIN" stop 2>&1)" || STOP2_RC=$?
case "$STOP2_OUT" in
  *"未运行"*) ok "[58] 重复 stop 幂等（未运行，退出码 $STOP2_RC）" ;;
  *) bad "[58] 重复 stop 输出异常（rc=$STOP2_RC：$STOP2_OUT）" ;;
esac

# ---------------------------------------------------------------- 6. 边界与异常
UNKNOWN_RC=0
"$BIN" no-such-command >/dev/null 2>&1 || UNKNOWN_RC=$?
if [ "$UNKNOWN_RC" -eq 2 ]; then
  ok "[59] 未知命令退出码 2"
else
  bad "[59] 未知命令退出码 $UNKNOWN_RC（期望 2）"
fi
FLAG_RC=0
"$BIN" --no-such-flag >/dev/null 2>&1 || FLAG_RC=$?
if [ "$FLAG_RC" -eq 2 ]; then
  ok "[60] 未知参数退出码 2"
else
  bad "[60] 未知参数退出码 $FLAG_RC（期望 2）"
fi
RANGE_RC=0
RANGE_OUT="$("$BIN" start --no-open -p 9000 2>&1)" || RANGE_RC=$?
if [ "$RANGE_RC" -eq 1 ]; then
  ok "[61] 端口超出 8899-8910 被拒绝（退出码 1）"
else
  bad "[61] 端口 9000 退出码 $RANGE_RC（期望 1）"
fi
case "$RANGE_OUT" in
  *"端口必须"*) ok "[62] 端口越界提示明确" ;;
  *) bad "[62] 端口越界提示缺失" ;;
esac

# 过期 server.json：不存在的 PID，应视为未运行并清理
printf '{"pid": 999999, "port": %s, "startedAt": 1, "version": "0.0.0"}\n' "$PORT" >"$YKT_HOME/server.json"
STALE_STATUS="$("$BIN" status 2>&1 || true)"
case "$STALE_STATUS" in
  *"未运行"*) ok "[63] 过期 server.json 时 status 视为未运行" ;;
  *) bad "[63] 过期 server.json 时 status 仍显示运行中" ;;
esac
STALE_STOP_RC=0
STALE_STOP_OUT="$("$BIN" stop 2>&1)" || STALE_STOP_RC=$?
if [ "$STALE_STOP_RC" -eq 0 ] && [ ! -f "$YKT_HOME/server.json" ]; then
  ok "[64] stop 清理过期 server.json"
else
  bad "[64] stop 未清理过期 server.json（rc=$STALE_STOP_RC）"
fi

# 端口顺延：占住请求端口后启动，应自动选择下一个可用端口
FALLBACK_PORT=""
if [ "$PORT" -ge 8910 ]; then
  skip "[65] 端口顺延（最低空闲端口为 8910，无顺延空间）"
elif ! command -v python3 >/dev/null 2>&1; then
  skip "[65] 端口顺延（缺少 python3，无法占用端口）"
else
  python3 - "$PORT" <<'PY' >/dev/null 2>&1 &
import socket, sys, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1])))
s.listen(5)
time.sleep(30)
PY
  DUMMY_PID=$!
  sleep 0.4
  FB_RC=0
  "$BIN" start --no-open -p "$PORT" >/dev/null 2>&1 || FB_RC=$?
  i=0
  while [ ! -f "$YKT_HOME/server.json" ] && [ "$i" -lt 50 ]; do
    sleep 0.1
    i=$((i + 1))
  done
  FALLBACK_PORT="$(json_int "$YKT_HOME/server.json" port)"
  if [ -n "$FALLBACK_PORT" ] && [ "$FALLBACK_PORT" != "$PORT" ] && [ "$FALLBACK_PORT" -ge 8899 ] && [ "$FALLBACK_PORT" -le 8910 ]; then
    ok "[65] 端口 $PORT 被占用时自动顺延到 $FALLBACK_PORT"
  else
    bad "[65] 端口顺延失败（请求 $PORT，实际 ${FALLBACK_PORT:-空}，rc=$FB_RC）"
  fi
  if wait_health "$FALLBACK_PORT" 20; then
    ok "[66] 顺延端口 $FALLBACK_PORT 服务健康"
  else
    bad "[66] 顺延端口 $FALLBACK_PORT 健康检查失败"
  fi
  "$BIN" stop >/dev/null 2>&1 || true
  kill "$DUMMY_PID" 2>/dev/null || true
  wait "$DUMMY_PID" 2>/dev/null || true
  DUMMY_PID=""
fi

# 前台 serve：SIGTERM 优雅退出并清理记录
FG_RC=0
"$BIN" serve -p "$PORT" >"$WORK/serve.log" 2>&1 &
FG_PID=$!
if wait_health "$PORT" 40; then
  ok "[67] 前台 ykt serve 启动并可访问 /health"
else
  bad "[67] 前台 ykt serve 未就绪"
fi
# /health 在 cmdServe 注册 SIGTERM 处理器之前就已可达（见 docs/qa-report.md 低危问题），
# 留出稳定窗口，保证本用例验证的是「优雅退出」而不是启动竞态。
sleep 0.5
if kill -0 "$FG_PID" 2>/dev/null; then
  kill -TERM "$FG_PID" 2>/dev/null || true
fi
wait "$FG_PID" 2>/dev/null || FG_RC=$?
FG_PID=""
if [ "$FG_RC" -eq 0 ]; then
  ok "[68] 前台 serve 收到 SIGTERM 后退出码 0"
else
  bad "[68] 前台 serve SIGTERM 退出码 $FG_RC（期望 0）"
fi
if [ ! -f "$YKT_HOME/server.json" ]; then
  ok "[69] 前台 serve 退出后清理 server.json"
else
  bad "[69] 前台 serve 退出后 server.json 仍存在"
fi
i=0
while port_listening "$PORT" && [ "$i" -lt 30 ]; do
  sleep 0.1
  i=$((i + 1))
done
if port_free "$PORT"; then
  ok "[70] 前台 serve 退出后端口已释放"
else
  bad "[70] 前台 serve 退出后端口仍被监听"
fi

# 原子写不应残留临时文件
if ls -a "$YKT_HOME" 2>/dev/null | grep -q '^\.tmp-'; then
  bad "[71] YKT_HOME 残留原子写临时文件"
  ls -la "$YKT_HOME" | sed 's/^/      /'
else
  ok "[71] 原子写未残留临时文件"
fi

# ---------------------------------------------------------------- 7. M5：owner=gui 时 CLI 必须拒绝杀进程
"$BIN" start --no-open -p "$PORT" >/dev/null 2>&1 || true
i=0
while [ ! -f "$YKT_HOME/server.json" ] && [ "$i" -lt 50 ]; do
  sleep 0.1
  i=$((i + 1))
done
GUI_PID="$(json_int "$YKT_HOME/server.json" pid)"
if [ -n "$GUI_PID" ] && kill -0 "$GUI_PID" 2>/dev/null; then
  ok "[72] 为 M5 用例启动的守护进程就绪（PID $GUI_PID）"
else
  bad "[72] M5 用例无法启动守护进程"
fi
sed -i.bak 's/"owner": "daemon"/"owner": "gui"/' "$YKT_HOME/server.json"
rm -f "$YKT_HOME/server.json.bak"
GUI_STOP_RC=0
GUI_STOP_OUT="$("$BIN" stop 2>&1)" || GUI_STOP_RC=$?
if [ "$GUI_STOP_RC" -eq 1 ] && printf '%s' "$GUI_STOP_OUT" | grep -q '托管'; then
  ok "[73] owner=gui 时 ykt stop 拒绝并提示（退出码 1）"
else
  bad "[73] owner=gui 时 stop 未按预期拒绝（rc=$GUI_STOP_RC：$GUI_STOP_OUT）"
fi
if [ -n "$GUI_PID" ] && kill -0 "$GUI_PID" 2>/dev/null && [ -f "$YKT_HOME/server.json" ]; then
  ok "[74] owner=gui 时进程未被杀、server.json 保留"
else
  bad "[74] owner=gui 时进程被误杀或记录被清理"
fi
sed -i.bak 's/"owner": "gui"/"owner": "daemon"/' "$YKT_HOME/server.json"
rm -f "$YKT_HOME/server.json.bak"
GUI_STOP2_RC=0
"$BIN" stop >/dev/null 2>&1 || GUI_STOP2_RC=$?
sleep 0.3
if [ "$GUI_STOP2_RC" -eq 0 ] && { [ -z "$GUI_PID" ] || ! kill -0 "$GUI_PID" 2>/dev/null; }; then
  ok "[75] 恢复 owner=daemon 后 stop 正常终止进程"
else
  bad "[75] 恢复 owner=daemon 后 stop 失败（rc=$GUI_STOP2_RC）"
fi
if port_free "$PORT" && [ ! -f "$YKT_HOME/server.json" ]; then
  ok "[76] M5 用例结束后端口释放、server.json 清理"
else
  bad "[76] M5 用例结束后端口/server.json 未清理"
fi

finish
