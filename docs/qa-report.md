# ykt 后端 / CLI / 用户脚本独立 QA 报告

- 审查角色：独立 QA（只读审查 + 只写 `tests/` 与 `docs/qa-report.md`，不参与实现、不修改产品代码）
- 首轮审查：2026-11-04 21:26–21:46（Asia/Shanghai），以 **21:46 工作树快照** 为准（首轮发现 M1–M5、L1–L19）
- 第二轮复审：2026-11-04 22:00–22:12，复核 Lead 对 M1–M5 与低危项的修复；以 **22:10 工作树快照** 为准（源文件 md5 漂移检查见 §0.5）
- 第三轮小修复核：2026-11-04 22:13–22:20，复核 R1/R2/L11/L12 修复与回归；以 **22:13 工作树快照** 为准
- 环境：macOS 15.7.1 / Apple Silicon（arm64）/ Go 1.27.0 / Wails v2.17.1 / Node v26.7.0
- 仓库：`<repo>`，HEAD 提交 `d4170d7`（2026-09-02 3.0.6）；整套 Go+Wails 实现为**未提交工作树**
- 范围：`internal/config`、`internal/script`、`internal/server`、`internal/browser`、`internal/cli`、根包 `app.go` / `main_cli.go` / `main_gui.go`、`yuketang.js` 的 AppSync 改动、旧 `bin/ykt.js` 行为对照、仓库卫生。GUI 前端不在本轮范围（未审、未启动）。
- 约束遵守：未修改 `internal/**`、`app.go`、`main.go`、`frontend/**`、`yuketang.js`、`docs/CONTRACT.md`；未 `git add/commit/push`；所有运行都使用 `mktemp` 临时 `YKT_HOME`，未写真实 `~/.ykt`；未启动 GUI、未打开浏览器（仅 `curl` 本机 HTTP）。
- 本轮交付物：`tests/smoke.sh`（端到端冒烟，**76 项**）、`tests/appsync_probe.mjs`（AppSync 隔离探针，**15 个用例全 PASS**）、本报告。

---

## 0. 第二/三轮复审（修复复核）摘要

Lead 于 21:45–22:03 按首轮报告修复 M1–M5 与大部分低危项，QA 于 22:00–22:12 用**真实实验 + 独立复跑**复核。结论：

> **M1–M5 全部修复并通过真实实验验证；L1–L8、L10–L17 已修复；R1（旧 Node 守护进程升级兼容）与 R2（GUI/CLI 共用入口的 `.tmp-*` 清理）已在第三轮修复并验证；仍遗留 L9（日志轮转）、L18（`.gitingore`）及 L19 提示项——均为低危，不阻塞合并。** 无严重问题。

### 0.1 中危修复复核（真实实验，仅用临时 `YKT_HOME`）

| 问题 | 修复实现（文件） | QA 复核实验 | 结果 |
| --- | --- | --- | --- |
| M1 并发双启动 | `internal/cli/cli.go` `withLock`/`acquireLock`（O_CREATE\|O_EXCL + 陈旧锁 20s）、`ensureServiceLocked` 锁内二次 `DetectRunning` | 两个 `ykt start --no-open -p 8899` 并发 | ✅ 两个进程均 exit 0（第二个输出「本地服务已在运行」），8899–8910 仅 1 个守护进程；`stop` 后无残留监听、`server.lock` 已释放 |
| M2 健康探测身份 | `internal/server/discover.go`：`CheckHealth` 校验 `app=="ykt"`；`DetectRunning` 以 `/health` PID 为准，不一致视为过期记录并清理、不杀进程 | (a) 假 `/health {"ok":true,"pid":victim}`（无 `app`）+ stale `server.json`；(b) 假 `/health app=ykt` 但 PID 与记录不一致 | ✅ (a)(b) 均 `status=未运行`；过期记录被清理；`stop` 后 victim 进程仍存活 |
| M3 分段同步 | `config.HasConfigSection`；`SyncConfig.hasAI/hasFeatures`；`yuketang.js` `apply()` 按段覆盖、旧响应回退 `configured`；`panel.refreshSettings()` | 真实服务 4 组配置 + `tests/appsync_probe.mjs` 15 用例 | ✅ settings-only：`configured/hasAI/hasFeatures=false`；仅 `ai`：`hasAI=true/hasFeatures=false`；仅 `features`：反向；两段都有且同步关闭：字段为 true 但 `ai/features` 零值不泄露。探针「仅 AI 段不覆盖网页功能开关」「仅 features 段不覆盖网页 AI」「旧服务响应按旧语义」全部 PASS |
| M4 旧 Node 数据迁移 | `LoadSettings` 兼容旧扁平 `url/port`；`LoadInstallState` 兼容 `openedHash` | 旧格式 `config.json` + `state.json`，执行 `status`/`start` | ✅ 默认地址恢复为 `changjiang.yuketang.cn`、迁移端口 8900 生效、安装记录显示旧 hash 且「与当前脚本一致」 |
| M5 owner 语义 | `server.json` 增加 `owner`（gui/daemon）；`cmdStopLocked` 遇 `owner=gui` 拒绝；GUI `StopService` 不误杀外部 gui 服务 | 构造 `owner=gui` 的 `server.json` + 假 ykt `/health` 后执行 `ykt stop` | ✅ exit 1，输出「本地服务由桌面 App 托管，请在 App 窗口内停止」；进程未被杀、`server.json` 保留；恢复 `owner=daemon` 后 `stop` 正常 |

### 0.2 低危修复状态

| 编号 | 状态 | 复核依据（第二/三轮） |
| --- | --- | --- |
| L1 SIGTERM 启动竞态 | ✅ 已修 | `cmdServe` 在 `Start` 前注册信号 + 清理陈旧 `.tmp-*`；10/10 次「启动即 SIGTERM」均 rc=0，无 `.tmp-*` 残留 |
| L2 SIGKILL 兜底 | ✅ 已修 | 构造忽略 SIGTERM 的假 ykt 服务：`stop` 3s 后 SIGKILL，exit 0、进程消失、记录清理 |
| L3 stop 使用 `status.Port` | ✅ 已修 | 代码复核 `cmdStopLocked`（`port := status.Port`） |
| L4 派生前误清记录 | ✅ 已修 | 代码复核：`ensureServiceLocked` 不再预先 `ClearServerInfo` |
| L5 启动超时子进程残留 | ✅ 已修 | 代码复核：超时 `Process.Kill()` 并回收 `waitCh` |
| L6 日志去重过宽 | ✅ 已修 | 代码复核：key = `level+"\x00"+message`，`logSeen` 超阈值清理；单测覆盖 |
| L7 handler 写共享 `s.meta` | ✅ 已修 | 代码复核：兜底移到 `server.New()`，handler 不再赋值 |
| L8 OPTIONS 预检 | ✅ 已修 | curl：白名单 Origin → 204 + `ACAO/ACAM/ACAH/Max-Age`；非白名单 → 403 |
| L9 server.log 无轮转 | ❌ 仍遗留 | 未实现大小上限/轮转 |
| L10 12 路探测无缓存 | ✅ 已修 | 新增 `ykt_app_port` 端口缓存并优先探测；探针 2 条用例 PASS |
| L11 面板/同步时序 | ✅ 已修（第三轮） | `boot()` 自动恢复改为 `Promise.race([syncDone, sleep(800)])` 后再启动；`panel.refreshSettings()` 保留；浏览器真机时序仍未验证（§11） |
| L12 CONTRACT:9 旧描述 | ✅ 已修（第三轮） | 第 9 行已重写为「两个构建目标：GUI / 纯 Go CLI」并说明无参数行为 |
| L13 Go 版本文档 | ✅ 已修 | README:23、install.sh:34 已改 Go 1.26+，go.mod 为 `go 1.26.0` |
| L14 无参数运行说明 | ✅ 已修 | README 新增「入口差异：纯 CLI 无参数=start；GUI 无参数=窗口」 |
| L15 package.json 旧元数据 | ✅ 已修 | 已删除 |
| L16 bin/ykt.js 旧 CLI | ✅ 已修 | 已删除 |
| L17 cmd/、tools/ 空目录 | ✅ 已修 | 已删除 |
| L18 .gitingore | ❌ 仍遗留 | 仍为仓库跟踪的拼写错误垃圾文件 |
| L19 wails.json 邮箱 | ➖ 提示项 | 未变；非缺陷 |

### 0.3 第二轮新发现（均低危）

| 编号 | 位置 | 问题 | 影响 / 建议 |
| --- | --- | --- | --- |
| R1（第三轮已修复，见 §0.6） | `internal/server/discover.go:19-50`（`CheckHealth` 严格要求 `app=="ykt"`） | M2 修复后，升级前已由旧 Node CLI 启动的守护进程（旧 `/health` 没有 `app` 字段）不再被识别：`status` 显示未运行，`stop` 只清理记录、不杀旧进程；随后 `start` 会在下一个端口另起新服务（旧进程继续占着 8899） | 实测：旧服务残留、新服务起在 8900；AppSync 仍能工作（8899 的 `/api/config` 404 → 探测到 8900），但旧进程要等重启/手工清理。建议 `stop` 路径做「仅停止」的迁移兜底：`health.App=="" && health.PID==info.PID` 时按旧 Node 服务处理（仍需 PID 匹配）；或在 releaseLog 提示「升级前先执行一次 ykt stop」 |
| R2（第三轮已修复，见 §0.6） | `internal/cli/cli.go`（`cleanupStaleTempFiles` 仅在 `cmdServe` 调用） | GUI 内置服务启动路径（`app.go ensureServerLocked` → `server.Start`）不清理陈旧 `.tmp-*`，GUI-only 用户可能保留早期异常退出留下的原子写临时文件 | 建议在 `config.OpenAt` 或 `server.Start` 前统一清理（保留 30s 年龄阈值），或在 `app.startup` 调用一次 |

### 0.4 第二/三轮最终验证结果

| 检查 | 结果 |
| --- | --- |
| `bash tests/smoke.sh` | **PASS=76 FAIL=0 SKIP=0**，退出码 0，耗时 5.27s（新增 `hasAI/hasFeatures`、`owner=daemon`、M5 `owner=gui` 拒绝共 5 项回归检查） |
| `node tests/appsync_probe.mjs` | **PASS=15 FAIL=0**（无 KNOWN-ISSUE；旧服务兼容场景改为正式断言） |
| `go test ./internal/... -count=1` | **126 个用例**全部通过（后端新增 29 个回归用例） |
| `go test -race ./internal/... -count=1` | 全部通过 |
| `CGO_LDFLAGS="-framework UniformTypeIdentifiers" go test -tags desktop,production -count=1 .` | 5 个 App 集成用例通过 |
| `go vet ./internal/...` / `go vet -tags cli ./...` | 通过 |
| `CGO_ENABLED=0 go build -tags cli` / linux、windows 交叉编译 | 通过 |
| `node --check yuketang.js` | 通过 |
| M1–M5 + L1/L2 真实实验（第二轮，23 项断言） | **23/23 PASS**（详见 §0.1/§0.2、§12） |
| R1/R2 真实实验（第三轮，15 项断言） | **15/15 PASS**（详见 §0.6） |

### 0.5 快照与漂移

- 第二轮：`internal/**`、`app.go`、`main_*.go`、`yuketang.js` 的 md5 与 22:05 快照一致；`internal/cli/cli.go` 最后修改 22:01:54，实验使用 22:05 重建的 CLI 二进制。
- 第三轮：Lead 于 22:10–22:12 修改 `internal/server/discover.go`、`internal/cli/cli.go`、`internal/config/config.go`、`docs/CONTRACT.md`、`yuketang.js`（新增 `Probe`/`legacyService`/`OpenAt` 清理/boot 时序）；QA 用 22:12 重建的二进制复核，期间源文件未再变化。
- 本轮 QA 只修改 `tests/**` 与 `docs/qa-report.md`，未改产品代码；实验产生的守护进程/假服务均已清理，8899–8910 无残留监听。

### 0.6 第三轮小修复核（R1/R2 + L11/L12）

Lead 于 22:10–22:12 补齐 R1/R2/L11/L12，QA 于 22:13–22:20 独立复核（临时 `YKT_HOME` + python 假旧版服务，结束已清理）。

| 项 | 实现 | 复核实验 | 结果 |
| --- | --- | --- | --- |
| R1 旧版服务迁移 | `server.Probe`（原始 `/health`，不校验 `app`）+ CLI `legacyService`（要求 `health.PID == server.json.PID` **且** `health.App == ""`）；`stop` 停止旧版并清理记录；`ensureService` 检测到旧版先停后接管；`status` 显示「旧版 ykt 服务运行中 · 端口 N · 执行 ykt stop 可停止并升级」 | A) 假旧版 `/health`（自报 PID、无 app）+ 匹配记录 → `status` 识别；B) `stop` → exit 0「已停止旧版 ykt 服务」、旧进程终止、记录清理；C) `start --no-open` → 先杀旧版，新版接管同端口 8899（`/health` app=ykt、owner=daemon）；D) `install --no-open` 同样自动接管；E) 安全反向：`/health` PID 与记录不一致 → `status=未运行`，`stop` 不杀 victim、也不误杀假旧版 | ✅ A–E 全部通过（第三轮实验 15 项断言） |
| R2 陈旧 tmp 清理 | `config.OpenAt` 统一清理 >30s 的 `.tmp-*`（GUI 与 CLI 共用入口） | 临时 `YKT_HOME` 预置 2 分钟前的 `.tmp-old` 与刚创建的 `.tmp-fresh`，执行 `ykt status`（触发 `config.Open`） | ✅ `.tmp-old` 被清理、`.tmp-fresh` 保留（30s 阈值正确） |
| L11 启动时序 | `boot()` 自动恢复改为 `Promise.race([syncDone, sleep(800)])` 后再启动 | 代码复核 + `node --check`；浏览器真机时序未验证 | ✅ 已修（静态复核；真机见 §11） |
| L12 契约 | `docs/CONTRACT.md` 第 9 行重写为「两个构建目标：GUI / 纯 Go CLI」 | 文档复核 | ✅ 已修 |

**第三轮回归**：`tests/smoke.sh` **76/76**、`tests/appsync_probe.mjs` **15/15**、`go test ./internal/...` **126 用例**、root GUI 测试 **5 用例**、`go vet ./internal/...` / `go vet -tags cli ./...`、`node --check yuketang.js` 全部通过；R1/R2 真实实验 **15/15 PASS**。第二、三轮实验累计 **38/38**。

---

## 1. 结论摘要（首轮，保留追溯）

**首轮未发现阻断性（严重）缺陷；发现 5 个中危、19 个低危。** 第二、三轮复核后：**5 个中危全部修复并验证**；低危 **16 项已修（L1–L8、L10–L17）**，遗留 **L9（日志轮转）、L18（`.gitingore`）**，L19 为提示项；第三轮新发现的 R1/R2 也已修复并验证。后端核心契约行为：配置原子写与 0600/0700 权限、端口顺延、stale `server.json` 清理、`/api/config` 的 403/CORS/同步关闭不泄露 Key、安装记录、stop 后端口释放等均已真实验证通过。

| 级别 | 首轮数量 | 第二轮状态 |
| --- | --- | --- |
| 严重 | 0 | 仍为 0（首轮快照曾发现 `app.go SaveSettings` 双重 Unlock，21:29 已修复并验证） |
| 中 | 5（M1–M5） | ✅ 5/5 已修复并通过真实实验（§0.1） |
| 低 | 19（L1–L19） | **16 已修（L1–L8、L10–L17）**；2 遗留（L9、L18）；L19 提示项 |
| 新发现 | — | R1 旧 Node 守护进程升级兼容、R2 陈旧 `.tmp-*` 清理 —— **✅ 第三轮均已修复并验证（§0.6）** |

首轮验证结果速览（保留）：smoke 71 PASS；probe 11 PASS + 1 KNOWN-ISSUE；internal 97 用例；根包 5 用例；`-race` 通过。**第二轮最终数据见 §0.4。**

**建议（第三轮后）**：无阻塞项。合并前可选处理：L9（日志轮转）、L18（删除 `.gitingore`，需 Lead 授权删除跟踪文件）；其余均已修复。

---

## 2. 严重问题

无（当前快照）。

> 说明：审查第一轮快照（21:26）中 `app.go` 的 `SaveSettings` 存在 `defer a.mu.Unlock()` 与函数体内 `a.mu.Unlock()` 重复解锁，任何一次成功/失败的设置保存都会 panic（`sync: unlock of unlocked mutex`）。Lead 在 21:29:12 的改版中已修复，当前代码为显式解锁、路径完整；根包 5 个集成用例通过。此事记录在 §6，不作为遗留问题。

---

## 3. 中危问题（首轮记录，第二轮状态见 §0.1）

> 第二轮状态：M1–M5 已全部修复并通过真实实验（详见 §0.1）；以下保留首轮原文以便追溯。

### M1 并发启动无互斥，会残留孤儿守护进程

> **第二轮状态：** ✅ 已修复并实验验证（互斥锁 + 锁内二次探测；见 §0.1）

> 以下为首轮问题记录，保留原文以便追溯。

- 位置：
  - `internal/cli/cli.go:614-656`（`ensureService`：`DetectRunning` 失败 → `ClearServerInfo` → 派生 `serve --daemon`）
  - `internal/cli/cli.go:629-630`（派生前的无条件清理）
  - `internal/server/server.go:140-148`（`Start` 成功后写 `server.json`，后写者覆盖前者）
  - `internal/cli/cli.go:439-480`（`cmdStop` 只按 `server.json` 停一个进程）
- 问题：两个 CLI 进程同时执行 `ykt start` 时，会各自判定「服务未运行」并各派生一个守护进程；两者分别监听不同端口，`server.json` 被最后一个覆盖。`ykt stop` 只停记录里的那一个，另一个变成无记录的孤儿（PPID=1、脱离终端）继续监听。
- 复现（已实测）：

  ```bash
  BIN=$(mktemp -d)/ykt && go build -tags cli -o "$BIN" .
  export YKT_HOME=$(mktemp -d)/home && mkdir -p "$YKT_HOME"
  "$BIN" start --no-open -p 8899 & "$BIN" start --no-open -p 8899 &
  wait
  lsof -nP -iTCP:8899-8910 -sTCP:LISTEN   # 实测同时有 8899、8900 两个 ykt 进程
  "$BIN" stop                              # 只停 8899，8900 的进程残留
  ```

  实测残留进程：`pid 934 / PPID 1 / ykt serve --daemon --port 8899`，`/health` 仍返回 `{"ok":true,"app":"ykt",...}`（QA 已清理，非用户环境残留）。
- 影响：① 进程泄漏，重复双开会越积越多；② 用户脚本 AppSync 按 8899→8910 顺序探测、取**最先响应**的端口，可能同步到孤儿进程里的旧配置；③ 后续 `ykt stop` 无法清理孤儿。
- 修复建议：跨进程互斥（`~/.ykt/server.lock` 的 `flock`/`O_EXCL` 锁包住 `ensureService` 与 `stop`）；`ensureService` 启动前在锁内二次 `DetectRunning`；`cmdStop` 可扫描 8899..8910 的 `/health`，清理所有 `app=ykt` 的实例；脚本侧优先选择 `/health` 中版本更高/启动更新者。

### M2 健康探测不校验服务身份，`stop` 可能 SIGTERM 无关进程

> **第二轮状态：** ✅ 已修复并实验验证（校验 `app=ykt` + PID 一致性；见 §0.1）

> 以下为首轮问题记录，保留原文以便追溯。

- 位置：
  - `internal/server/discover.go:19-50`（`CheckHealth` 只校验 HTTP 200、JSON 可解析、`ok=true`，**不校验 `app` 字段**）
  - `internal/server/discover.go:54-81`（`DetectRunning` 信任 `health.PID`，缺省才回退 `server.json` 的 PID）
  - `internal/cli/cli.go:445-467`（`cmdStop` 直接 `KillPID(status.PID)`）
  - `app.go:109-128`（GUI `StopService` 同样路径）
- 问题：`server.json` 过期/损坏，而记录端口恰好被另一个本地服务占用且其 `/health` 返回 `{"ok":true}`（很多开发服务如此）时，CLI/GUI 会把它误判为 ykt 服务并 `SIGTERM` 它；若该响应没有 `pid` 字段，则回退到过期记录里的旧 PID，PID 被复用时可能杀掉任意进程（Windows 上是 `TerminateProcess`，更粗暴）。
- 复现（已实测，用自建的 sleep 进程作为「无关进程」）：

  ```bash
  sleep 300 & VICTIM=$!
  # 在 127.0.0.1:8899 起一个 /health 返回 {"ok":true,"pid":$VICTIM} 的假服务
  printf '{"pid":999999,"port":8899,"startedAt":1,"version":"0.0.0"}\n' > "$YKT_HOME/server.json"
  "$BIN" status   # 实测显示「运行中 … PID $VICTIM」
  "$BIN" stop     # 实测 VICTIM 进程被 SIGTERM 杀死
  ```

- 影响：误杀用户其他本地开发服务/进程；属于「杀掉非本进程」类安全问题。触发需要 stale 记录 + 端口复用 + `ok:true` 健康响应，概率不高但后果明确。
- 修复建议：`CheckHealth` 或 `DetectRunning` 校验 `health.App == "ykt"`（兼容旧 Node 服务可要求 `app=="ykt" || (app=="" && version!="")`）；kill 前再校验 `saved.PID == health.PID`；更稳妥的是给服务加 `POST /admin/stop`（带 `X-YKT-Client`/一次性 token），CLI 只请求自停、不直接按 PID 杀进程。

### M3 `configured` 粒度过粗：App 只配 AI 时，网页功能开关被零值覆盖

> **第二轮状态：** ✅ 已修复并实验验证（`hasAI/hasFeatures` 按段覆盖；见 §0.1）

> 以下为首轮问题记录，保留原文以便追溯。

- 位置：
  - `yuketang.js:318-334`（`AppSync.apply` 无条件 `Store.setFeatureConf({autoAI: Boolean(...), autoComment: Boolean(...)})`）
  - `yuketang.js:335-349`（`sync` 只判断整体 `configured`）
  - `internal/config/config.go:273-287`（`HasUserConfig` 只要存在 `ai` 或 `features` 键就为 true）
  - `internal/server/server.go:445-456`（`configured=true` 时整段返回 AI/Features）
  - `internal/model/model.go:128-137`（`SyncConfig` 只有一个 `configured` 布尔）
- 问题：用户在 App 里只保存了 AI 配置（`config.json` 只有 `"ai"` 键）时，`/api/config` 返回 `configured=true` 且 `features` 恒为 `{"autoAI":false,"autoComment":false}`。脚本无法区分「App 没配置功能开关」和「App 显式关闭」，于是每次打开页面都会把网页面板里用户开着的 `autoAI/autoComment` 重置为 false —— 用户改回去，下次加载又会被覆盖。
- 复现（已实测）：

  ```bash
  # config.json 只写 "ai"，不写 "features"
  curl -s -H 'X-YKT-Client: userscript' http://127.0.0.1:$PORT/api/config
  # → {"configured":true,"ai":{...真实Key...},"features":{"autoAI":false,"autoComment":false}}
  node tests/appsync_probe.mjs
  # → KNOWN-ISSUE coarse configured 导致网页功能开关被零值覆盖（面板原值 autoAI=true/autoComment=true 被重置为 false）
  ```

- 影响：网页端功能开关无法持久开启，属于用户配置被误覆盖；若将来 App 把更多字段塞进同一结构，同类问题会扩散。
- 修复建议：给 `SyncConfig` 增加 `hasAI` / `hasFeatures`（或把 `configured` 拆成两段式），脚本仅在对应段存在时覆盖；只保存 `settings` 时仍整体 `configured=false`（当前已正确）。契约 `docs/CONTRACT.md:109-115` 需同步修改，并补 `HasUserConfig` 分段测试。

### M4 旧 Node CLI 的 `~/.ykt` 数据不兼容，升级后默认地址/端口静默丢失

> **第二轮状态：** ✅ 已修复并实验验证（旧扁平字段/`openedHash` 迁移；见 §0.1）

> 以下为首轮问题记录，保留原文以便追溯。

- 位置：
  - `internal/config/config.go:129-134`（`fileConfig` 只认 `settings/ai/features` 新结构）
  - `internal/config/config.go:168-197`（`LoadSettings` 对缺失 `settings` 直接返回默认值）
  - `internal/model/model.go:26-33`（`InstallState` 字段与旧 `openedHash` 不兼容）
  - `bin/ykt.js:138-148`（旧 CLI 的 `config.json` 是扁平 `{"url","port"}`）、`bin/ykt.js:427-431`（旧 `state.json` 是 `openedHash`）
  - `README.md` / `releaseLog.md` 未提示迁移或破坏性变更
- 问题：新旧 CLI 共用 `~/.ykt`，但 `config.json` 结构不同。旧用户升级后：默认地址从自己的学校域名回退到 `https://yuketang.cn/`、端口回退 8899；旧 `state.json` 的安装记录也读不出来（`status` 显示「暂无」，首次 `start` 会重开一次安装页）。
- 复现（已实测）：

  ```bash
  printf '{"url":"https://changjiang.yuketang.cn/","port":8900}\n' > "$YKT_HOME/config.json"
  "$BIN" status
  # → 默认地址 https://yuketang.cn/   （旧值被静默丢弃）
  printf '{"openedHash":"...","version":"3.0.7","openedAt":1793799093000}\n' > "$YKT_HOME/state.json"
  "$BIN" status
  # → 安装记录 暂无
  ```

- 影响：老用户升级后打开的不是自己的雨课堂入口；新旧 CLI 若同时使用会按各自格式反复覆写 `state.json`。
- 修复建议：`LoadSettings` 兼容旧扁平 `url/port` 字段（存在时作为迁移来源，首次保存后写新结构）；`LoadInstallState` 兼容 `openedHash`；在 `releaseLog.md` 1.0.0 条目里写明「配置格式升级，旧配置自动迁移/或需重新设置默认网址」。

### M5 CLI `stop` / `restart` 会终止正在运行的 GUI App（静态推断，未真机验证）

> **第二轮状态：** ✅ 已修复并实验验证（`owner=gui` 时拒绝；见 §0.1；真实 GUI 场景仍未验证，见 §11）

> 以下为首轮问题记录，保留原文以便追溯。

- 位置：
  - `internal/server/server.go:140-145`（GUI 内置服务写 `server.json` 时 PID 为 App 进程）
  - `internal/cli/cli.go:439-480`（`cmdStop` 按 PID 直接 `SIGTERM`）
  - `app.go:109-128`、`app.go:327-350`（GUI 复用/持有服务）
- 问题：App 运行时本地服务跑在 App 进程内，`server.json` 记录的是 App 的 PID。此时执行 `ykt stop` 会把整个桌面 App `SIGTERM` 掉（App 没有信号处理器，窗口直接消失）；`ykt restart` 更会把 GUI 服务换成后台 CLI 守护进程。命令语义上「停服务」不应等于「退 App」。
- 影响：用户边用 App 边敲 `ykt stop` 时桌面窗口意外退出；CLI 与 GUI 生命周期交叉语义混乱。
- 修复建议：`server.json` 增加 `owner: app|cli`（或单独 `servicePid`）；`cmdStop` 检测到 `app` 时改为提示「请从 App 内停止」或调用内部停服端点；App 注册 `SIGTERM/SIGINT` 走 Wails 优雅退出。
- 备注：本轮不启动 GUI，故为静态推断；需要 Lead 在 GUI 实测时确认。

---

## 4. 低危问题（首轮记录，第二轮状态见 §0.2）

> 第二/三轮状态：L1–L8、L10–L17 已修复；L9、L18 仍遗留；L19 为提示项；R1/R2 已修复（§0.6）。以下保留首轮原文。

| 编号 | 位置 | 问题 | 影响 / 建议 |
| --- | --- | --- | --- |
| L1 | `internal/cli/cli.go:406-435`、`708-713`；`internal/server/server.go:134-148` | `cmdServe` 在 `srv.Start()` 返回后才注册 SIGTERM 处理器，而 `/health` 在 `Start()` 内部就已可访问；该窗口内收到 SIGTERM 会走默认终止，`server.json` 原子写被打断留下 `.tmp-*` 垃圾文件 | 12 次「启动即 SIGTERM」试验：2 次退出码 143、2 次残留 `.tmp-*`；`ykt start` 后立刻 `ykt stop` 也可能命中。建议 `cmdServe` 一开始就 `signal.Notify`（启动前注册），并在 `OpenAt`/写文件前清理陈旧 `.tmp-*` |
| L2 | `internal/cli/cli.go:465-479` | `stop` 只发 SIGTERM，3 秒后不再升级为 SIGKILL，也不校验进程确实退出；旧 Node CLI 有 SIGKILL 兜底（`bin/ykt.js:405-408`） | 进程忽略 SIGTERM 时仍打印「已停止」。建议 unix 上超时后 SIGKILL，或明确报错保留记录 |
| L3 | `internal/cli/cli.go:470` | `stop` 等待健康检查用的是 `server.json` 的 `info.Port`，而 kill 用的 PID 来自 `/health` 的 `status.Port`；两者不一致时会等错端口 | 极端 stale 场景下等待 3 秒空转。改为 `status.Port` |
| L4 | `internal/cli/cli.go:629-630` | `ensureService` 在派生新进程前无条件 `ClearServerInfo` | 若另一实例健康检查因 800ms 超时被误判为未运行，会删掉其有效记录并起第二个服务（M1 的放大器）。建议锁内二次确认 |
| L5 | `internal/cli/cli.go:643-655` | 派生后 4s 内未就绪即报错，但不终止子进程；旧实现会 kill 失败子进程（`bin/ykt.js:385`） | 子进程稍后成功时会「先报错、后可用」，记录由子进程补写。建议超时清理子进程或延长等待并给出明确提示 |
| L6 | `internal/server/server.go:264-292` | 日志 5 秒去重按 `message` 全局生效（含错误、生命周期日志），且 `logSeen` 无过期清理 | 契约只要求配置同步日志去重（`docs/CONTRACT.md:115`）。相同文本不同级别会被吞；长跑内存缓慢增长。建议仅对 sync 日志去重或 key 用 `level+message` |
| L7 | `internal/server/server.go:582-584` | `handleIndex` 在请求处理中给共享字段 `s.meta.Name` 赋值，`/health` 同时读 `s.meta` | 仅当内嵌脚本头异常（`New` 解析失败被忽略）时触发，属理论数据竞态。建议初始化时兜底，不在 handler 写共享状态 |
| L8 | `internal/server/server.go:334-342`、`421-461` | `/api/config` 有 CORS 白名单但未处理 `OPTIONS` 预检（返回 405，且无 `Access-Control-Allow-Headers`/`Allow-Methods`） | 当前用户脚本只用 `GM_xmlhttpRequest`，不触发预检，因此实际安全不受影响；但任何浏览器 `fetch` 路线都会失败。若无意支持 fetch，建议在契约注明「仅 GM_xhr」；否则补预检响应 |
| L9 | `internal/server/server.go:295-309`、`discover.go:84-109` | `server.log` 只追加、无大小上限/轮转（旧 Node 同样） | 长期运行文件无限增长。建议加大小上限或按启动轮转 |
| L10 | `yuketang.js:38-42`、`299-317` | 每次页面加载固定并发探测 12 个端口，成功后也不取消其余请求 | 无服务时会有 12 次本机 GM 请求开销/扩展日志。建议缓存上次成功端口优先探测，失败再全扫 |
| L11 | `yuketang.js:786-791`、`2374-2390` | 面板字段在 `createPanel` 时先读 localStorage，AppSync 在其后异步应用；若用户在同步完成前打开设置或自动恢复刷课（1200ms 后），可能短暂使用旧配置 | 打开设置时会重新 `loadAIConf`（`yuketang.js:788-791`）已缓解。建议 `await AppSync.sync()` 后再开放启动/自动恢复，或 apply 后通知面板刷新 |
| L12 | `docs/CONTRACT.md:9` | 仍写「同一份代码的 CLI 二进制用 `CGO_LDFLAGS=... -tags desktop,production` 构建」，与现行 `main_cli.go`（`-tags cli`，纯 Go）、目录表及文末验证命令矛盾 | 文档自相矛盾，建议更新该条为「GUI 二进制（含 CLI 子命令）+ 纯 Go CLI 二进制」两行 |
| L13 | `README.md:23`、`install.sh:34`、`go.mod:3` | README/install.sh 声称 Go 1.21+，而 `go.mod` 为 `go 1.27.0` | 按文档装 Go 1.21 会因版本指令直接构建失败。建议统一为实际最低版本（1.27） |
| L14 | `README.md:52`、`main_cli.go:20-29` | README 称「无参数运行 ykt 等价于打开桌面管理台」，但纯 CLI 二进制无参数实际等同 `start`（会拉服务、开浏览器）；工作树里同时存在 `bin/ykt`（CLI）与 `build/bin/ykt.app`（GUI），容易混淆 | 建议写明两个入口的区别：CLI 版无参数=start，GUI 版无参数=窗口 |
| L15 | `package.json:2-12` | 仍是旧 Node CLI 元数据：`version 3.0.6`、`bin.ykt → bin/ykt.js`、`scripts.ykt = node bin/ykt.js`、`engines.node`；与 README「纯 Go、不依赖 Node」「前端无 npm」冲突 | 未跟踪文件，若随提交会误导贡献者，且 `npm link` 会把旧 Node CLI 装成 `ykt`。建议删除，或改造成仅承载元数据的版本号文件 |
| L16 | `bin/ykt.js`（工作树，被 `/bin/` 忽略） | 旧 Node CLI 仍留在本地；`git add -A` 不会提交它，但打包/发版时可能混入 | 建议删除或移入 `.private/`；releaseLog 记录已被 Go CLI 取代 |
| L17 | `cmd/`、`tools/`（空目录） | 空目录不会被 git 跟踪；契约明确「`go build ./cmd/ykt` 不提供」 | 建议删除，避免贡献者误解 |
| L18 | `.gitingore`（仓库已跟踪文件） | 拼写错误的垃圾文件，内容仅 `something.md` | 建议删除（跟踪文件操作需 Lead 授权） |
| L19 | `wails.json:11` | 含作者个人邮箱 `songoao25@gmail.com` | 仓库历史提交已含同一邮箱，影响有限；若希望减少公开信息可移除（非阻塞，仅提示） |

> 另有两点非缺陷观察：① `yuketang.js:14` 新增的 `@connect 127.0.0.1` 在 `yuketang.js:20` 已有 `@connect *` 的前提下是冗余的（无害）；② `/api/config` 的 403 响应也会对白名单 Origin 回写 ACAO（只泄露「请求被拒」这一非敏感信息，可忽略）。

---

## 5. CLI 行为差异清单（旧 `bin/ykt.js` → 新 Go CLI）

对照方式：通读旧实现 + 在新旧两版上执行只读命令（`version`/`help`）+ 在临时 `YKT_HOME` 中执行新 CLI；未运行旧 CLI 的 `start/serve`（避免写真实 `~/.ykt`）。第二轮后补充了 owner/锁/迁移相关行。

| # | 项目 | 旧 Node CLI | 新 Go CLI | 判断 / 建议 |
| --- | --- | --- | --- | --- |
| 1 | 无参数运行 | `start`（拉服务 + 装脚本 + 开雨课堂） | GUI 构建：开窗口；纯 CLI 构建：仍是 `start` | 设计如此；README 已写清入口差异（L14 已修） |
| 2 | 配置文件格式 | `~/.ykt/config.json` = `{"url","port"}` 扁平 | `{"settings","ai","features"}` 分段 | 读取时自动迁移旧扁平 `url/port`（M4 已修），下次保存写新结构 |
| 3 | 安装记录 `state.json` | `{"openedHash","version","openedAt"}` | `{"installed","inSync","version","hash","openedAt","note"}` | 读取时兼容 `openedHash`（M4 已修）；升级后不丢安装记录 |
| 4 | `server.json` | `{pid,port,startedAt,root}` | `{pid,port,startedAt,version,owner}` | 共同字段兼容；但新 CLI 要求 `/health` 返回 `app=ykt`，**旧 Node 服务不再被识别/停止**（M2 的安全收紧副作用，见 R1） |
| 5 | `-p/--port` 范围 | 任意 1–65535，端口顺延 12 次（`bin/ykt.js:37-39,61-64,356-364`） | 限定 8899–8910，越界报错退出 1 | 有意收紧；旧帮助里的 `--port 9000` 示例失效，README 已改 |
| 6 | `YKT_PORT` 环境变量 | 支持（默认端口来源，`bin/ykt.js:37`） | 不支持 | 低风险差异；如要保留 CI 习惯可补 |
| 7 | `YKT_NO_OPEN=1` | 支持（`bin/ykt.js:160-162`） | 不支持，只有 `--no-open` | 低风险差异 |
| 8 | `start --no-open` 的安装记录 | 仍写入 `openedHash`（`bin/ykt.js:427-434` 无条件写） | 不写安装记录（`openInstallPage` 在 noOpen 时提前返回） | 新行为更诚实（没打开就不算装过），但与旧版不同 |
| 9 | `-u/--url` 持久化 | `start -u URL` 保存 url；**且同一次会把 `-p` 指定的端口一起存进 config**（`bin/ykt.js:421`） | `start -u URL` 只保存 URL，`-p` 永不持久化 | 低风险差异；若要 `-p` 记忆端口需 `SaveSettings` |
| 10 | `stop` 语义 | 健康/存活不校验，SIGTERM→3s→SIGKILL 兜底，清理 `server.json` | `/health` 门控（stale 记录清理）+ SIGTERM→3s→SIGKILL 兜底 + `owner=gui` 拒绝；失败如实报错保留记录 | 已对齐旧版兜底能力并增加安全约束（M2/M5/L2 已修） |
| 11 | `version` 输出 | `3.0.7 3910c81e`（脚本版本 + 短指纹） | `App v1.0.0 · 脚本 v3.0.7 · 指纹 3910c81e` | 契约要求新格式；脚本化解析旧输出的用户需适配 |
| 12 | `status` 字段 | 含「仓库目录」，脚本文件为仓库相对路径 | 改为「内置（随 App 打包）」+ App 版本 + 日志文件，去掉仓库目录 | 合理（脚本已内嵌）；无解析兼容承诺 |
| 13 | 参数语法 | 不支持 `--port=8900`；未知参数退出 2 | 支持 `--port=`/`--url=`；未知参数退出 2 | 向后兼容，只是新增能力 |
| 14 | 别名 | `i`/`ui`/`st` | `i`/`ui`/`st` | 一致 |
| 15 | 退出码约定 | 成功 0、运行失败 1、用法错误 2；错误 `✖` 到 stderr | 相同；`stop` 遇 `owner=gui` 返回 1 | 一致（smoke 已断言未知命令/参数 2、越界端口 1、owner=gui 拒绝 1） |
| 16 | 脚本来源 | 运行时读仓库 `yuketang.js`（改文件即生效） | 编译期 `//go:embed`（换脚本要重新构建） | 设计变化；README/契约已说明 |
| 17 | 服务日志格式 | 纯文本一行 | JSON Lines（`server.log`） | 契约要求；旧日志行会被 `ReadLogFile` 跳过 |
| 18 | `serve` 前台输出 | 3 行（启动/安装地址/Ctrl+C） | 多一行脚本版本指纹 | 仅输出差异 |
| 19 | 健康响应 | 无 `app`/`port` 字段 | 增加 `app:"ykt"`、`port` | CLI 已利用 `app` 校验（M2 已修） |
| 20 | 新增路由/能力 | 无 | `/api/config` 配置同步、`/index.html`、HEAD 兼容、`--daemon` 显式参数 | 新功能，无需对照 |
| 21 | 服务归属 `owner` | 无 | `server.json.owner = gui/daemon`；CLI 遇 `gui` 拒绝直接停止 | 新增语义（M5）；真实 GUI 场景待验证 |
| 22 | 并发互斥 | 无锁，并发 `start` 会双开（旧版同样有此问题） | `~/.ykt/server.lock` O_CREATE\|O_EXCL + 锁内二次探测 | 新增（M1）；陈旧锁 20s 清理 |
| 23 | `/api/config` 分段标记 | 无 | 增加 `hasAI`/`hasFeatures`（按 config.json 段存在与否） | 新增（M3）；旧脚本忽略新字段也可用 |
| 24 | CORS 预检 | 无 | 白名单 Origin 的 `OPTIONS` 返回 204 + ACAO/ACAM/ACAH | 新增（L8）；GM_xhr 场景非必需 |

---

## 6. 审查期间仓库变化与已修复项

审查期间 Lead/后端成员在并行改仓库；为避免误报，本报告内部包首轮基线为 21:12–21:21 的稳定版本，根包与文档以 21:46 快照为准；第二轮以 22:10 快照为准（见 §0.5）。

**首轮期间（21:29–21:31）**：

- 21:29：`main.go` 拆分为 `main_cli.go`（`//go:build cli`，纯 Go CLI）+ `main_gui.go`（`//go:build !cli`，Wails）+ `version.go`；`app.go`/`app_test.go` 加 `//go:build !cli`。
- 21:29：**修复** `app.go` `SaveSettings` 双重 `a.mu.Unlock()`（首轮快照中的崩溃点；当前为显式解锁，5 个根包集成用例通过）。
- 21:29–21:31：`Makefile` 增加 `cross-cli`、`install-cli`；`README.md` 重写为 GUI/CLI/纯脚本三种用法；`install.sh` 改为构建并安装纯 Go CLI；`.gitignore` 增加 `/dist/`。
- 21:30：`docs/CONTRACT.md` 目录表与验证命令更新为 `-tags cli` 双入口（第 9 行遗留旧描述，见 L12）。

**第二轮修复期间（21:45–22:03）**：

- `internal/model/model.go`：新增 `ServiceStatus.Owner`、`ServerInfo.Owner`、`SyncConfig.HasAI/HasFeatures`（由 Lead 加）。
- `internal/config`：`LoadSettings` 旧扁平迁移、`LoadInstallState` 旧 `openedHash` 迁移、`HasConfigSection`/`HasUserConfig` 分段判断；新增迁移与分段单测。
- `internal/server`：`CheckHealth` 校验 `app=ykt`；`DetectRunning` PID 一致性检查与过期记录清理；`server.json` 写 `owner`；`/api/config` 返回 `hasAI/hasFeatures` + OPTIONS 预检；日志去重 key=`level+message` + 过期清理；handler 不再写 `s.meta`。
- `internal/cli`：`server.lock` 跨进程互斥（`withLock/acquireLock`）；`cmdServe` 启动前注册信号并清理陈旧 `.tmp-*`；`stop` owner=gui 拒绝、SIGKILL 兜底、使用 `status.Port`；启动超时终止子进程。
- `app.go`：GUI 启动服务时 `Owner: "gui"`，`StopService/SaveSettings` 不误杀外部 gui 服务。
- `yuketang.js`：AppSync 改为按段覆盖（`hasAI/hasFeatures`，旧响应回退 `configured`）；新增端口缓存 `ykt_app_port`；新增 `panel.refreshSettings()`；同步日志改为「✅ 已从本机 App 同步配置：AI与功能开关（端口 N）」。
- 文档/卫生：README/install.sh 的 Go 版本改为 1.26+、go.mod 改 `go 1.26.0`；README 澄清 CLI/GUI 入口；删除 `package.json`、`bin/ykt.js`、`cmd/`、`tools/`；`releaseLog.md` 增加旧数据迁移说明。
- 已独立复跑：`go test ./internal/...`（126 用例）、`-race`、根包 5 用例、`go vet`、`node --check`、交叉编译、`tests/smoke.sh` 76/76、`tests/appsync_probe.mjs` 15/15。

---

## 7. 仓库卫生结论（第二轮更新）

**覆盖情况**（`git check-ignore` 实测，`.gitignore` 已含 `/dist/`）：

| 路径 | 是否忽略 | 结论 |
| --- | --- | --- |
| `build/bin/`、`bin/`、`dist/` | 是 | 构建产物不会误提交 |
| `frontend/wailsjs/` | 是 | 与契约「Wails 生成绑定无需提交」一致 |
| `.private/`、`tmp/`、`*.log` | 是 | 私有/临时材料有落点 |
| `build/appicon.png`、`build/darwin/*.plist` | 否 | 正确：Wails 脚手架资产应随仓库提交 |
| `package.json`、`bin/ykt.js`、`cmd/`、`tools/` | — | ✅ 已删除（L15/L16/L17 已修） |
| `.gitingore` | 否（已跟踪） | ❌ 仍遗留：拼写错误的垃圾文件（L18） |

**其他结论**：

- 敏感信息：扫描 `*.go`/`*.js`/`*.json`/`*.sh` 未发现真实 Key/token/私钥/本机绝对路径；AI Key 只出现在测试临时目录，运行后已清理。`wails.json` 含作者邮箱（L19，仅提示）。
- 版本一致性：脚本 `yuketang.js` 3.0.7 = `releaseLog.md` 3.0.7 条目；App 1.0.0 = `version.go` = `wails.json`；`package.json` 已删除。
- 文档一致性：README 已对齐新实现（L13/L14 已修）；`releaseLog.md` 已补旧数据迁移说明（M4）；`docs/CONTRACT.md:9` 已在第三轮修正为 GUI/CLI 双构建目标（L12 已修）；新增 `docs/DESIGN.md`（Lead 文档）。
- 需随本次提交纳入：`docs/qa-report.md`、`tests/smoke.sh`、`tests/appsync_probe.mjs`；`.gitingore` 建议删除后提交（需 Lead 授权）。
- 旧资产处理：`bin/ykt.js`/`package.json`/`cmd/`/`tools/` 已删除；`install.sh` 已改为 Go CLI 构建安装（保留）；README 已重写（保留）。

---

## 8. `tests/smoke.sh` 说明与运行结果原文（第二/三轮更新）

- 路径：`tests/smoke.sh`；用法：`bash tests/smoke.sh`
- 设计：`set -euo pipefail`；仓库根由脚本位置自动探测；构建到 `mktemp` 目录；全部状态写入临时 `YKT_HOME`；`trap` 清理（只 kill 本脚本记录且 `/health` 自报 `app=ykt` 的进程）；只用 `--no-open`/`serve` 等不弹浏览器调用，不启动 GUI；PASS/FAIL/SKIP 统计，FAIL>0 时非零退出。
- 覆盖 **76 项**检查（第二轮新增 5 项）：`go build -tags cli`、`go vet`、linux/windows 交叉编译；`version`/`status`；后台 `start --no-open`；`server.json`（PID/端口/**owner=daemon**/0600/原子写）；`/health`；`/ykt.user.js`（内容 sha256 与仓库一致、Content-Type、no-store）；安装记录 `state.json`；`/api/config` 无头 403、带头 200、**`configured`/`hasAI`/`hasFeatures` 三态**（无配置 / 只有 settings / 有 ai+features）、同步关闭不泄露 Key、CORS 白名单/非白名单、`/`、`/favicon.ico`、404、405；日志 JSON Lines 且不含 Key；运行中 `status`；`stop` 清理与端口释放、重复 stop 幂等；未知命令/参数退出码、端口越界拒绝、过期 `server.json` 清理、端口顺延、前台 `serve` 的 SIGTERM 优雅退出、原子写无 `.tmp-*` 残留；**M5 `owner=gui` 时 `stop` 拒绝、不杀进程、恢复 owner 后可正常停止**。
- 复跑记录：第二/三轮共 4 次 **76/76 全绿**（含第三轮 22:16）；单次耗时约 **5s**（Go 构建缓存已热）。

运行结果原文（`bash tests/smoke.sh`，2026-11-04 22:16 第三轮；脚本指纹已更新为 4a514769）：

```text
ykt 冒烟测试
仓库：<repo>
Go：go version go1.27.0 darwin/arm64
临时 YKT_HOME：<tmp>/pq/fr_590yx0z790q4l750y1bw00000gn/T/ykt-smoke.7gs9yS/home

PASS  [01] go build -tags cli 构建纯 Go CLI
PASS  [02] go vet -tags cli ./...
PASS  [03] 交叉编译 linux/amd64
PASS  [04] 交叉编译 windows/amd64
PASS  [05] ykt version 退出码 0
PASS  [06] version 输出脚本版本 v3.0.7
PASS  [07] version 输出指纹短码 4a514769
PASS  [08] ykt status（未启动服务）退出码 0
PASS  [09] status 显示本地服务未运行
PASS  [10] status 使用临时 YKT_HOME（不碰真实 ~/.ykt）
PASS  [11] ykt start --no-open -p 8899 退出码 0
PASS  [12] --no-open 只打印地址，不调用浏览器
PASS  [13] server.json 已写入（owner=daemon）
PASS  [14] 服务监听请求端口 8899
PASS  [15] server.json 记录的 PID 58213 存活
PASS  [16] YKT_HOME 目录权限 0700
PASS  [17] server.json 权限 0600
PASS  [18] GET /health 200
PASS  [19] /health app=ykt
PASS  [20] /health 指纹与仓库 yuketang.js 一致
PASS  [21] /health 回显端口 8899
PASS  [22] /health Content-Type 为 JSON
PASS  [23] GET /ykt.user.js 返回非空内容
PASS  [24] 内嵌脚本与仓库 yuketang.js 完全一致
PASS  [25] /ykt.user.js Content-Type 为 application/javascript
PASS  [26] /ykt.user.js Cache-Control: no-store
PASS  [27] 拉取脚本后写入 state.json 安装记录
PASS  [28] state.json 记录当前脚本版本与指纹
PASS  [29] state.json 权限 0600
PASS  [30] /api/config 无 X-YKT-Client 头返回 403
PASS  [31] 403 响应 JSON 含 forbidden
PASS  [32] /api/config 带头返回 200
PASS  [33] 未保存配置时 configured/hasAI/hasFeatures 均为 false 且 sync.enabled=true
PASS  [34] 未保存配置时不返回 AI Key
PASS  [35] /api/config Cache-Control: no-store
PASS  [36] config.json 只有 settings 时 configured/hasAI/hasFeatures 均为 false
PASS  [37] 已保存配置且同步关闭时 configured/hasAI/hasFeatures=true、sync.enabled=false
PASS  [38] 同步关闭时响应不泄露 AI Key
PASS  [39] 同步关闭时功能开关返回零值
PASS  [40] 开启同步后 /api/config 下发 AI Key 与功能开关
PASS  [41] CORS 允许的 Origin 原样回写 ACAO
PASS  [42] CORS 响应带 Vary: Origin
PASS  [43] 非白名单 Origin 不回写 ACAO
PASS  [44] GET /favicon.ico 204
PASS  [45] GET / 返回本地控制台页面
PASS  [46] 未知路径返回 404
PASS  [47] POST /api/config 返回 405
PASS  [48] server.log 存在且权限 0600
PASS  [49] server.log 不含 AI Key
PASS  [50] server.log 为 JSON Lines 格式
PASS  [51] status 显示服务运行中
PASS  [52] status 显示服务 PID 58213
PASS  [53] ykt stop 退出码 0
PASS  [54] stop 输出「已停止」
PASS  [55] stop 后清理 server.json
PASS  [56] stop 后后台进程已退出
PASS  [57] stop 后端口 8899 已释放
PASS  [58] 重复 stop 幂等（未运行，退出码 0）
PASS  [59] 未知命令退出码 2
PASS  [60] 未知参数退出码 2
PASS  [61] 端口超出 8899-8910 被拒绝（退出码 1）
PASS  [62] 端口越界提示明确
PASS  [63] 过期 server.json 时 status 视为未运行
PASS  [64] stop 清理过期 server.json
PASS  [65] 端口 8899 被占用时自动顺延到 8900
PASS  [66] 顺延端口 8900 服务健康
PASS  [67] 前台 ykt serve 启动并可访问 /health
PASS  [68] 前台 serve 收到 SIGTERM 后退出码 0
PASS  [69] 前台 serve 退出后清理 server.json
PASS  [70] 前台 serve 退出后端口已释放
PASS  [71] 原子写未残留临时文件
PASS  [72] 为 M5 用例启动的守护进程就绪（PID 58375）
PASS  [73] owner=gui 时 ykt stop 拒绝并提示（退出码 1）
PASS  [74] owner=gui 时进程未被杀、server.json 保留
PASS  [75] 恢复 owner=daemon 后 stop 正常终止进程
PASS  [76] M5 用例结束后端口释放、server.json 清理

========================================
冒烟结果：PASS=76  FAIL=0  SKIP=0
临时目录（已清理）：<tmp>/pq/fr_590yx0z790q4l750y1bw00000gn/T/ykt-smoke.7gs9yS
========================================
```

---

## 9. `tests/appsync_probe.mjs`：AppSync 隔离探针（第二轮更新）

不修改 `yuketang.js`，按注释标记原样抽取 `const AppSync = {...}`（本轮抽取 3932 字节），用 Node + 最小 localStorage/GM mock 执行真实逻辑。第二轮按 M3 修复后的语义重写为 **15 个正式断言**（无 KNOWN-ISSUE）：

- 降级与总开关：无 GM、12 端口全失败、连接失败、`sync.enabled=false`、`configured=false`；
- **M3 分段覆盖**：仅 AI 段不覆盖网页功能开关、仅 features 段不覆盖网页 AI、两段同时覆盖、AI 空字段逐项回退；
- **旧服务兼容**：无 `hasAI/hasFeatures` 的响应按旧 `configured` 语义覆盖；`configured=false` 时保持面板设置；
- 响应校验：非 ykt/非 200/坏 JSON/500 忽略；多端口竞速取最快并缓存端口；缓存端口优先探测；`apply` 后 `refreshSettings()` 只刷新一次。

运行结果原文：

```text
yuketang.js AppSync 隔离探针
源文件：<repo>/yuketang.js
抽取代码长度：3932 字节

PASS  缺少 GM_xmlhttpRequest 时静默回退（不抛异常、不写 localStorage）
PASS  12 个端口全部失败时返回 false 且 400ms 内收敛
PASS  服务未启动（连接错误）时回退，不改 localStorage
PASS  sync.enabled=false 时保留网页面板设置，不写端口缓存
PASS  configured=false（新版响应）时保留网页面板设置
PASS  仅保存 AI 段（hasAI=true hasFeatures=false）：只覆盖 AI，保留网页功能开关
PASS  仅保存 features 段（hasAI=false hasFeatures=true）：只覆盖开关，保留网页 AI 配置
PASS  两段都有（hasAI/hasFeatures 均 true）：AI 与功能开关同时覆盖
PASS  App 的 AI 字段为空时按字段回退到网页面板值（hasAI=true）
PASS  旧服务响应（无 hasAI/hasFeatures，configured=true）按旧语义同时覆盖两段
PASS  旧服务响应（无 hasAI/hasFeatures，configured=false）保持网页面板设置
PASS  非 ykt 服务 / 非 200 / 坏 JSON / 500 都被忽略
PASS  多端口同时响应时采用最先返回的成功结果（竞速生效）并缓存该端口
PASS  缓存端口生效：下一次同步优先探测上次成功端口
PASS  app 字段固定为 ykt 时才接受（防止误连其他本地服务）

结果：PASS=15  FAIL=0
```

结论：AppSync 的 Promise 竞速、超时、`GM_xmlhttpRequest` 缺失降级、分段覆盖、旧服务向后兼容、端口缓存均按预期工作；首轮 M3 缺陷已消除。

---

## 10. 其他验证记录（第二/三轮）

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/... -count=1` | 6 包通过，**126 个用例**（首轮 97，后端新增 29 个回归用例） |
| `go test -race ./internal/... -count=1` | 全部通过，未报告数据竞态 |
| `CGO_LDFLAGS="-framework UniformTypeIdentifiers" go test -tags desktop,production -count=1 .` | `ok … 0.263s`（5 个 App 集成用例） |
| `go vet ./internal/...` / `go vet -tags cli ./...` | 通过 |
| `CGO_ENABLED=0 go build -tags cli` | 通过（验证「纯 Go、无 cgo」） |
| `GOOS=linux/windows GOARCH=amd64 go build -tags cli` | 通过 |
| `node --check yuketang.js` | 通过 |
| 真实实验矩阵（M1 5 项、M2 5 项、M4 3 项、M5 4 项、L2 2 项、R1 2 项、L1 2 项） | **23/23 PASS**（结果见 §0.1/§0.2） |
| curl 实测 `/api/config` | 无头 403；带头 200；`hasAI/hasFeatures` 三态正确；同步关闭时 `ai.key=""`；OPTIONS 预检白名单 204/非白名单 403；CORS ACAO 正确 |
| 后台守护 / 并发 | `ykt start --no-open` 派生 `serve --daemon`；两个并发 `start` 仅 1 个守护进程；`server.lock` 释放；stop 后端口释放、记录清理 |
| 端口顺延 | 8899 被占用时自动监听 8900（smoke [65]/[66] 实测） |
| 第三轮回归 | smoke 76/76、probe 15/15、`go test ./internal/...` 126 用例、root GUI 5 用例、`go vet` ×2、`node --check` 全部通过 |
| R1/R2 真实实验（第三轮） | **15/15 PASS**：旧版 status/stop/start/install 接管、PID 不一致安全反向、陈旧 `.tmp-*` 清理（详见 §0.6） |

---

## 11. 未验证项（第三轮更新）

1. **GUI 前端与窗口**：明确不在本轮范围；未启动 GUI、未截图、未验证前端交互与 Wails 绑定联调（`app_test.go` 只覆盖绑定方法逻辑）。
2. **M5 的真实 GUI 场景**：用「`server.json owner=gui` + 假 ykt `/health`」模拟验证了 CLI 拒绝逻辑；**未启动真实 App** 验证「App 运行时 `ykt stop` 不会杀掉 App」「多 App 实例下 StopService 的行为」；App 自身注册 SIGTERM 优雅退出也未验证。
3. **R1 真实升级路径**：第三轮用假 `/health`（无 `app`、PID 匹配）验证了旧版识别/停止/接管与安全反向；**未真实运行旧 Node CLI 守护进程**做一次完整升级。
4. **`wails build` 真机构建 `.app`**：未执行；macOS 图标/Info.plist/隐藏标题栏/毛玻璃、剪贴板、Finder 定位、事件通知均未做真机验证。
5. **Windows / Linux 真机运行**：只做 `GOOS` 交叉编译；Windows 的 `detach`/`TerminateProcess`/`forceTerminate`/文件锁行为、Linux 的 `xdg-open` 均未真机验证。
6. **浏览器 + Tampermonkey 端到端 AppSync**：未打开浏览器；`GM_xmlhttpRequest` 真实权限提示、`@connect` 弹窗、真实 400ms 超时行为未验证（probe 是隔离 mock + curl 模拟服务端）。
7. **L11 的启动时序**：`Promise.race([syncDone, sleep(800)])` 只做了代码/语法级复核，未在真实浏览器 + Tampermonkey 下验证「自动恢复刷课一定在配置同步后」。
8. **真实 `~/.ykt` 迁移**：只在临时目录模拟旧 Node 格式，未在真实用户目录做新旧 CLI 交替升级验证。
9. **R2 的 GUI 真实入口**：`config.OpenAt` 清理已通过 CLI 入口实测（GUI 共用同一函数），但未真正启动 GUI 验证启动时清理。
10. **并发/锁边界**：只验证了 2 进程并发；未测锁持有 >5s、陈旧锁 20s 清理、3+ 进程竞争、浏览器 `open` 卡住导致锁超时的场景。
11. **长跑与日志**：L9 日志长期增长未做长跑；L6 去重行为未做长时间观察。
12. **端口全占用路径**：smoke 只覆盖端口顺延，未覆盖 8899–8910 全部占用时的失败提示。
13. **平台**：仅 macOS 15.7.1 / arm64；未验证 Intel Mac、Windows、Linux 真机。
14. **旧 Node CLI 完整行为对照**：只读执行了 `version`/`help`，其余差异基于源码通读（避免写真实 `~/.ykt`）。

---

## 12. 关键复现命令（汇总）

### 12.1 首轮问题复现（保留，修复前证据）

```bash
cd <repo>
BIN=$(mktemp -d)/ykt && go build -tags cli -o "$BIN" .
export YKT_HOME=$(mktemp -d)/home && mkdir -p "$YKT_HOME"

# M1（修复前）并发双启动 → 两个 daemon，stop 只清一个
"$BIN" start --no-open -p 8899 & "$BIN" start --no-open -p 8899 & wait
lsof -nP -iTCP:8899-8910 -sTCP:LISTEN
"$BIN" stop; lsof -nP -iTCP:8899-8910 -sTCP:LISTEN   # 修复前仍有残留

# M4（修复前）旧扁平 config.json → 默认地址/端口被回退
printf '{"url":"https://changjiang.yuketang.cn/","port":8900}\n' > "$YKT_HOME/config.json"
"$BIN" status | grep 默认地址

# L1（修复前）启动即 SIGTERM 的竞态：约 2/12 次 rc=143 + .tmp-* 残留
for i in $(seq 1 12); do
  "$BIN" serve -p 8899 >/dev/null 2>&1 & P=$!
  until curl -s --max-time 0.2 http://127.0.0.1:8899/health | grep -q '"app":"ykt"'; do sleep 0.01; done
  kill -TERM "$P"; wait "$P" 2>/dev/null; echo "rc=$?"
  ls "$YKT_HOME"/.tmp-* 2>/dev/null; rm -f "$YKT_HOME"/server.json
done
```

### 12.2 第二轮修复复核（实测全部通过）

| 场景 | 关键命令 | 期望/实测 |
| --- | --- | --- |
| M1 并发 | 两个 `"$BIN" start --no-open -p 8899 &` 同时执行后 `wait` | 均 exit 0；`lsof 8899-8910` 仅 1 条；`server.lock` 不存在；`stop` 后无监听 |
| M2a 假 health 无 app | 假服务返回 `{"ok":true,"pid":victim}` + stale `server.json`；`"$BIN" status` / `"$BIN" stop` | status=未运行；stop 清理记录但 victim 存活 |
| M2b PID 不一致 | 假 health `app=ykt` 但 pid≠`server.json.pid`；`status` | 未运行且过期记录被清理；victim 存活 |
| M4 迁移 | 旧 `config.json` + 旧 `state.json`；`status` / `start --no-open` | URL=changjiang；安装记录显示旧 hash「与当前脚本一致」；服务监听迁移端口 8900 |
| M5 owner=gui | `start` 后把 `server.json.owner` 改为 `gui`；`"$BIN" stop` | exit 1 +「托管」提示；进程存活、记录保留；恢复 `daemon` 后 stop 成功 |
| L2 SIGKILL | 假 ykt `/health` 由忽略 SIGTERM 的进程提供；`"$BIN" stop` | 3s 后 SIGKILL，exit 0，进程消失，记录清理 |
| R1 旧 Node 兼容 | 假 `/health`（无 `app`）+ 匹配 pid 的 `server.json`；`status`/`stop`/`start` | 旧服务被视为未运行、不被杀（**残留**）；新服务起在下一端口（实测 8900） |
| L1 竞态 | 10 次「`serve` 就绪后立即 SIGTERM」 | 10/10 rc=0；无 `.tmp-*` 残留 |

> 实验脚本为一次性 bash（临时 `YKT_HOME` + python 假服务），结束已 kill 所有进程；复跑入口优先用 `bash tests/smoke.sh` 与 `node tests/appsync_probe.mjs`。

---

### 12.3 第三轮小修复核（实测全部通过）

| 场景 | 关键命令/构造 | 期望/实测 |
| --- | --- | --- |
| R1-A status 识别 | 假旧版 `/health`（`{"ok":true,"pid":<self>}`，无 `app`）+ 匹配 pid 的 `server.json`；`"$BIN" status` | 「旧版 ykt 服务运行中 · 端口 N · 执行 ykt stop 可停止并升级」 |
| R1-B stop 旧版 | 同上；`"$BIN" stop` | exit 0「已停止旧版 ykt 服务（PID N）」；旧进程终止；`server.json` 清理 |
| R1-C start 接管 | 同上；`"$BIN" start --no-open -p <P>` | 先杀旧版并提示「已停止并由新版接管」；新版监听同端口 `<P>`（`/health` app=ykt、owner=daemon） |
| R1-D install 接管 | 同上；`"$BIN" install --no-open -p <P>` | 同 C，自动接管 |
| R1-E 安全反向 | 假旧版 `/health` pid 与 `server.json.pid` 不一致；`status`/`stop` | `status=未运行`；stop 不杀 victim、不误杀假旧版 |
| R2 tmp 清理 | YKT_HOME 预置 2 分钟前 `.tmp-old` 与刚建 `.tmp-fresh`；`"$BIN" status` | `.tmp-old` 被清理；`.tmp-fresh` 保留 |
| 第三轮回归 | smoke / probe / go test / root test / vet / node --check | 76/76、15/15、126 用例、5 用例、通过、通过 |

> 第三轮实验共 15 项断言全部通过（第二、三轮累计 38/38）；实验使用临时 `YKT_HOME`，结束已清理所有假服务与守护进程，8899–8910 无残留。

---

*报告结束。所有问题只记录、未修改产品代码；修复与取舍由 Lead 决策。第三轮仅修改 `tests/**` 与 `docs/qa-report.md`。*
