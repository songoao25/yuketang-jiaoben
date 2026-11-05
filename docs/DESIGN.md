# 雨课助手 · 统一设计语言 v3（与「超星助手」共享 · unslop）

> 两个桌面工具（雨课助手 ykt / 超星助手 chaoxing）共用同一套设计语言，
> 用 unslop 方法去掉 AI 味，做成成熟、精致、符合 macOS 设计的界面；两个 App 并排看应像同一套产品的两个模块。
>
> 本文件是该共享设计语言在 ykt 仓库内的规范副本；chaoxing 仓库的 `desktop/docs/DESIGN.md` 为同源版本。
> 实现以本规范第 1–5 节的设计令牌与组件数值为准，页面内容按第 6 节 ykt 映射。
>
> 方法论：unslop（Damir Sokolovsky，CC BY 4.0）——安静 chrome、单一强调色、真实内容承载视觉、
> 逐层修间距/颜色/图标/文案/字体/布局/细节/视觉/动效、最后按清单验收。
> 禁止项：色相渐变、发光、彩色阴影、emoji 图标、渐变文字、全大写宽字距小标签、脉冲装饰、hover 位移 > 2px。

## 0. 方向

- **产品**：两个「学生自己用的课程自动化」工具，本地运行、数据在 `~/.xxx`，不联网除学习平台本身。
- **性格**：安静、精确、可靠（calm, exact, dependable）。
- **世界**：macOS 系统设置——分组卡片列表、语义色小图标、SF 字体、细滚动条。
- **重点**：不靠装饰。真实数据（课程、进度、日志、复核正文）就是全部视觉内容。
- **签名**（全站一致，两 App 共用）：① 30px 彩色 squircle 行图标（系统设置同款）；
  ② 页头 h1 + 一行说明 + 右侧操作；③ 侧栏底部状态圆点 + 版本号；④ 快捷键提示 ⌘1…。

## 1. 设计令牌（与 ykt 完全一致，亮/暗）

```css
:root {
  --font-ui: -apple-system, BlinkMacSystemFont, "SF Pro Text", "PingFang SC", "Helvetica Neue", Arial, sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;

  /* 亮色 */
  --bg: #f5f5f7;
  --sidebar: #ececf0;
  --sidebar-glass: rgba(246, 246, 248, 0.72);
  --sidebar-border: rgba(0, 0, 0, 0.08);
  --card: #ffffff;
  --card-border: rgba(0, 0, 0, 0.04);
  --card-shadow: 0 0 0 0.5px rgba(0, 0, 0, 0.05), 0 1px 2px rgba(0, 0, 0, 0.04);
  --inset: rgba(120, 120, 128, 0.08);
  --row-hover: rgba(0, 0, 0, 0.03);
  --label: #1d1d1f;
  --label-2: #6e6e73;
  --label-3: #8e8e93;
  --separator: #e5e5ea;
  --fill: rgba(120, 120, 128, 0.08);
  --fill-2: rgba(120, 120, 128, 0.14);
  --accent: #007aff;
  --accent-hover: #0a84ff;
  --accent-soft: rgba(0, 122, 255, 0.12);
  --green: #34c759;  --green-text: #248a3d;  --green-soft: rgba(52, 199, 89, 0.14);
  --orange: #ff9500; --orange-text: #b25000; --orange-soft: rgba(255, 149, 0, 0.14);
  --red: #ff3b30;    --red-text: #d70015;    --red-soft: rgba(255, 59, 48, 0.12);
  --purple: #af52de; --indigo: #5856d6;      --gray: #8e8e93;
  --field-bg: #ffffff; --field-border: #d1d1d6;
  --btn-bg: rgba(0, 0, 0, 0.05); --btn-hover: rgba(0, 0, 0, 0.08); --btn-active: rgba(0, 0, 0, 0.12);
  --switch-off: #e9e9ea;
  --focus-ring: rgba(0, 122, 255, 0.32);
  --scroll-thumb: rgba(0, 0, 0, 0.20); --scroll-thumb-hover: rgba(0, 0, 0, 0.32);
  --toast-bg: rgba(28, 28, 30, 0.94); --toast-border: rgba(255, 255, 255, 0.10); --toast-text: #f5f5f7;
  --modal-shadow: 0 20px 60px rgba(0, 0, 0, 0.24);
  --sk-base: rgba(0, 0, 0, 0.05); --sk-shine: rgba(255, 255, 255, 0.6);
}

/* 暗色（prefers-color-scheme: dark，或 data-theme="dark"） */
--bg: #1c1c1e;        --sidebar: #232326;      --sidebar-glass: rgba(35, 35, 38, 0.72);
--sidebar-border: rgba(255,255,255,0.08);      --card: #2c2c2e;
--card-border: rgba(255,255,255,0.06);         --card-shadow: 0 1px 2px rgba(0,0,0,0.30);
--inset: rgba(255,255,255,0.06);               --row-hover: rgba(255,255,255,0.04);
--label: #ffffff; --label-2: #98989d; --label-3: #6c6c70; --separator: #3a3a3c;
--fill: rgba(255,255,255,0.07); --fill-2: rgba(255,255,255,0.12);
--accent: #0a84ff; --accent-hover: #3d9bff; --accent-soft: rgba(10,132,255,0.22);
--green: #30d158; --orange: #ff9f0a; --red: #ff453a; --purple: #bf5af2; --indigo: #7d7aff;
--field-bg: #1c1c1e; --field-border: #48484a;
--btn-bg: rgba(255,255,255,0.08); --btn-hover: rgba(255,255,255,0.12); --btn-active: rgba(255,255,255,0.16);
--switch-off: #39393d; --focus-ring: rgba(10,132,255,0.45);
--scroll-thumb: rgba(255,255,255,0.20); --scroll-thumb-hover: rgba(255,255,255,0.32);
--modal-shadow: 0 20px 60px rgba(0,0,0,0.5);
```

字体：正文 13px/1.45；页头 h1 22px/600 letter-spacing -0.015em；page-sub 12px `--label-2`；
row-title 13px；row-desc 11px `--label-2`；mono 数字 tabular-nums；日志 11.5px mono。
圆角：控件 6/7px，分组卡 10px，弹层 12px，行图标 8px，小图标按钮 5px。
阴影：只用 `--card-shadow` 与 `--modal-shadow`，禁止彩色阴影/发光。

## 2. 窗口外壳（两个 App 相同）

```
┌───────────────────────────────────────────────────────────┐
│ ●●●   [toolbar 52px：右侧状态点 + 刷新等操作，可拖动窗口]     │
├──────────┬────────────────────────────────────────────────┤
│ sidebar  │ main（padding-top 52px，滚动区 12px 32px 20px） │
│ 220px    │   page max-width 660px 居中                     │
│ 58px 顶部 │   page-wide 用于日志/复核双栏                   │
│ 留交通灯  │                                                │
└──────────┴────────────────────────────────────────────────┘
```

- `html/body` 在真实窗口里**透明**（`html.platform-darwin:not(.mock) body { background: transparent }`），
  主内容区 `.main` 自己铺 `--bg`；侧栏 `--sidebar-glass` + `backdrop-filter: blur(28px) saturate(1.6)`，
  透出桌面毛玻璃（与 ykt 一致）。
- toolbar：`position: fixed; top:0; left:0; right:0; height:52px; padding:0 20px 0 72px;`，
  `--wails-draggable: drag`；左侧 72px 为交通灯空间；右侧放：状态点 + 文案、刷新按钮。
- sidebar：220px（≤900px 时 204px，≤760px 收成 64px 图标栏）；`padding: 58px 10px 12px`；
  nav-label 11px/600 `--label-3`（如「工作台」「系统」）；nav-item 30px 高、圆角 7、图标 17px、
  右侧 kbd 提示 9.5px mono；active = `--accent` 底白字。
- 侧栏底部：mini-status（7px 状态点 + 11px 文案）、版本号 10px `--label-3`。
- 页头：`page-head` flex，左 h1 + page-sub，右 `page-head-side`（按钮/分段/未保存提示）；
  页头下 18px 接分组列表。**不要**旧的「01 / 04 索引 + 墨线」签名（那是 v2 的，已废弃）。

## 3. 分组列表（核心组件，直接复用 ykt 数值）

- `.group` 下边距 22；`.group-title` margin `0 12px 6px`，13px/500 `--label-2`；
  `.group-box` 背景 `--card`、圆角 10、`--card-shadow`、overflow hidden。
- `.row`：min-height 40、padding 5px 14px、gap 12，hover `--row-hover`；
  行间分隔用 `::before` 1px `--separator`（left 14px；带图标时 56px）。
- `.row-icon`：30px squircle 圆角 8、白图标 16px、内高光；
  语义色块：蓝 `#3b82f6`、绿 `#2fb45c`、橙 `#e8940f`、紫 `#9a6bd8`、靛 `#6c6bd9`、灰 `#8e8e93`。
- `.row-copy`：row-title 13px，可带 `row-title-extra` 11.5px `--label-2`；row-desc 11px `--label-2`。
- `.row-control`：右侧控件，gap 8；`.row-value` 12px `--label-2`。
- `.chips/.chip`：10px mono 标签；`.badge`：18px 高胶囊，语义色底 + 同色文字 + 5px 圆点
  （success/warning/danger/running）。
- `.step-row/.step-num`：22px 圆圈编号；完成态绿底白勾（用于刷课阶段、安装步骤）。
- `.settings-layout`：两栏 `1.05fr / 1fr`、gap 22，≤900px 单栏（设置页用）。
- 网页内注入面板（yuketang.js）沿用同一套令牌与 4px 栅格：中性底 + 单一系统蓝主操作，无 emoji、无彩色按钮。

## 4. 控件（数值照抄 ykt）

- `.btn`：26px 高、圆角 6、字号 12.5、padding 0 11、hover/active/focus/disabled/busy 齐全；
  `.btn-sm` 24px；`.btn-primary` = `--accent` 底白字 500；`.btn-danger` = 红底白字；
  `.btn-danger-quiet` 红字；`.icon-btn` 24×24 圆角 5；`.link-btn` accent 文字 12px。
- `.switch`：38×22 胶囊，开启 `--green`，knob 18px，200ms 位移；`.switch-sm` 32×19。
- `.appearance-seg` + `.seg-btn`：inset 底分段控件，24px 高（复核过滤、可选参数用它）。
- `.input`：28px 高、圆角 6、12.5px；`.input.mono` 11.5px；focus 3px `--focus-ring`；
  `.select-wrap select` 28px + 自绘箭头；`.input-md` 220px、`.input-wide` 340px。
- `.icon-sprite` 内联 SVG + `<use>`；图标 18px、stroke 1.7、圆头圆角；`.icon-fill` 用于实心播放/停止。
- `--wails-draggable: no-drag` 必须加在所有交互控件上（button/input/select/switch/nav 等）。

## 5. 复合组件

- **日志卡**（与 ykt 运行日志页同款，我们的刷课运行日志直接复用）：
  `.log-card` 白卡 10px 圆角；`.log-card-head` 36px（live-dot + 计数 + 路径 mono 10px）；
  `.log-row` 三列 grid `62px 50px 1fr`、mono 11.5、行间 hairline、hover `--fill`；
  level：info accent、warn orange-text、error red-text、success green-text、debug label-3；
  `.empty-state`（42px 圆底图标 + 标题 13/500 + 说明 11 + 按钮）覆盖空态/错误态。
- **Toast**：底部居中胶囊，深色半透明 + blur，240ms 入场，3s 消失。
- **Modal**：320px 宽、12px 圆角、图标圈 + 标题 13.5/600 + 说明 11.5 + 右侧按钮；遮罩 18% 黑 + 1.5px blur。
- **Skeleton**：`.page-skeleton` + `.sk-line/.sk-box` shimmer 1.5s，首屏加载用。
- **About 头**：40px 圆角 9 图标 + 名称 14/600 + 版本 11 mono。

## 6. 页面映射（ykt 版）

侧栏分组：`工作台` → 概览(⌘1)/脚本(⌘2)/AI 配置(⌘3)；`系统` → 运行日志(⌘4)/设置(⌘5)。
底部状态点 + 脚本版本；toolbar 右侧：最近同步时间 + 刷新。

- **概览**：h1 概览 / 副标题「本地服务、用户脚本与 AI 配置」；
  分组「运行状态」带图标行：
  - 本地服务：row-icon 蓝，状态 badge（运行中/已停止）+ 端口 / PID / 运行时长（mono），
    行内按钮：启动（btn-primary）或停止/重启（次级）；
  - 用户脚本：row-icon 靛，版本 v3.0.8 + 指纹短码（mono）+ 安装状态 badge（已安装/待重装），
    行内「重新安装」；
  - 配置同步：row-icon 绿，状态 badge（已开启/已关闭）+ 最近同步时间 + `127.0.0.1:8899`（mono）。
  分组「快捷操作」：打开雨课堂（右侧 btn-primary）、AI 配置、运行日志；
  分组「快速开始」：step-row 三步（启动服务/安装脚本/打开雨课堂），完成态绿底白勾 + 安静说明；
  未运行 / 脚本待更新时用 group-note 横幅写清后果与下一步，不用大色块。
- **脚本**：h1 脚本 / 副标题「Tampermonkey 用户脚本」；页头右侧「打开雨课堂」；
  分组「安装状态」「脚本信息」「Tampermonkey 安装步骤」：行内 row-icon + 版本/行数/指纹/匹配域名（mono）、
  安装地址（mono + 复制按钮）、重新安装；不出现彩色大徽章。
- **AI 配置**：h1 AI 配置 / 副标题「自动答题与图文讨论回复使用的模型」；页头右侧「保存配置」btn-primary；
  分组「服务商」（预设 select + 说明 text）、「接口」（API 地址 input-wide / 模型 / 格式 / 认证方式 / Key + 显示切换）、
  「脚本功能」（两个 switch 行 + 说明）；底部 group-note 说明配置保存位置与同步行为。
- **运行日志**：page-wide；h1 运行日志 / 副标题「最近 200 条本地服务日志」；
  v3 log-card：head 36px（live 点 + 条数 + 日志路径 mono 10 + 自动滚动 switch / 刷新 / 清空 / 日志文件），
  行 grid `62px 50px 1fr`、mono 11.5、level 用 accent/orange-text/red-text/green-text/label-3、hover `--fill`；
  空态 empty-state；清空二次确认 modal。
- **设置**：settings-layout 两栏（1.05fr / 1fr，≤900px 单栏）；
  分组「服务」（端口 8899–8910 select、启动时自动启动服务 switch、启用脚本配置同步 switch）、
  「站点」（默认网址 input-wide）、「外观」（跟随系统/浅色/深色 分段控件）、
  「数据与关于」（打开配置目录/日志文件 link-btn，关于 about-head：App v1.0.1 · 脚本 v3.0.8 · GPL-3.0 · 上游仓库）。

## 7. 文案（与 ykt 语气一致）

- 平静、具体、可执行；无感叹号；按钮/标签不加句号；不用「一键/智能/赋能/超级」。
- 复核页必须准确：AI 文字是**先提交后留痕**，文案只说明「已提交给平台、可回看/复制」，
  绝不暗示可提交前编辑。
- 数据必须真实；无数据显示「暂无记录」并说明来源。

## 8. 动效

- 页面入场 180ms fade+4px；hover/active 120–160ms；modal 180ms scale .98→1；toast 240ms。
- 唯一允许的循环：运行中状态点 breathing（语义 = 正在运行），源自 ykt 的 dot-pulse；
  非运行状态禁止脉冲。
- reduced-motion 全部关闭。禁止弹跳、位移 >4px、布局属性动画。

## 9. 验收清单

- [ ] 与 ykt 并排：字体/字号/行高/圆角/分隔线/控件高度一致，像同一产品的两个模块
- [ ] 无 v2 遗留：无「01 / 04」索引墨线、无大写数字卡、无旧渐变/发光/emoji
- [ ] 亮/暗两套 + 880×620/1280×800 不破版；侧栏毛玻璃在真机 .app 内正常
- [ ] 所有交互五态齐全；键盘 ⌘1–⌘4 可切换；focus-visible 可见
- [ ] 日志/空态/错误态/骨架 与 ykt 组件同款
- [ ] 无外链资源、无 console 报错、无默认控件外观
- [ ] 模板测试：把「超星助手」换成「雨课助手」也不违和 → 设计语言统一成功

---

设计语言参考：yuketang-jiaoben/frontend/app.css（同作者项目，2026-11-04）；
方法论：unslop by Damir Sokolovsky（CC BY 4.0, https://github.com/heydamir/unslop）。
