/* ==========================================================================
 * 雨课助手 · 管理台前端
 * 统一设计语言 v3（与「超星助手」共用令牌与组件）。
 * 纯原生 JS：无框架、无构建、无外部请求。
 *   ?mock=1 预览：不访问 Wails 绑定，用与 model.State 同构的 mock 数据；
 *   真实模式：window.go.main.App.* + window.runtime 事件 + 3s 轮询兜底。
 * ========================================================================== */
(function () {
  'use strict';

  var MOCK = new URLSearchParams(window.location.search).get('mock') === '1';
  var PAGES = ['overview', 'script', 'review', 'settings'];
  // AI 配置不再占顶级导航，作为设置下的子页面（与超星助手把 AI/引擎配置收在设置里一致）
  var SUB_PAGES = ['ai'];
  function isPage(page) { return PAGES.indexOf(page) >= 0 || SUB_PAGES.indexOf(page) >= 0; }
  var PAGE_META = {
    overview: { title: '概览', sub: '本地服务、用户脚本与 AI 配置' },
    script: { title: '刷课', sub: '按顺序准备，然后在网页里开始' },
    review: { title: '复核', sub: '运行日志与配置同步记录' },
    ai: { title: 'AI 配置', sub: '自动答题与图文讨论回复使用的模型' },
    settings: { title: '设置', sub: '本机运行参数与外观' }
  };
  var PAGE_ACTIONS = {
    overview: [],
    script: ['btnScriptOpenYuketang', 'btnScriptViewLogs'],
    review: [],
    ai: ['aiDirtyChip', 'btnSaveAI'],
    settings: ['settingsDirtyChip', 'btnSaveSettings']
  };

  /* ───────────── 工具 ───────────── */
  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
  function wait(ms) { return new Promise(function (r) { window.setTimeout(r, ms); }); }
  function bindings() { var go = window.go; return go && go.main && go.main.App ? go.main.App : null; }
  function runtime() { var rt = window.runtime; return rt && typeof rt.EventsOn === 'function' ? rt : null; }
  function requireBindings() {
    var api = bindings();
    if (!api || typeof api.GetState !== 'function') throw new Error('未检测到 App 绑定，请通过桌面 App 打开');
    return api;
  }
  function errMsg(err) {
    if (!err) return '未知错误';
    if (typeof err === 'string') return err;
    if (typeof err.message === 'string' && err.message) return err.message;
    try { return JSON.stringify(err); } catch (e) { return String(err); }
  }
  function pad2(n) { return n < 10 ? '0' + n : String(n); }
  function text(sel, value) {
    var el = typeof sel === 'string' ? $(sel) : sel;
    if (el) el.textContent = value == null ? '' : String(value);
  }
  function val(sel) { var el = typeof sel === 'string' ? $(sel) : sel; return el ? el.value : ''; }
  function isOn(sel) { var el = typeof sel === 'string' ? $(sel) : sel; return !!el && el.getAttribute('aria-checked') === 'true'; }
  function setSwitch(el, on) { var n = typeof el === 'string' ? $(el) : el; if (n) n.setAttribute('aria-checked', on ? 'true' : 'false'); }
  function setHidden(el, hidden) { var n = typeof el === 'string' ? $(el) : el; if (n) n.hidden = !!hidden; }
  function platformLabel(p) {
    if (p === 'darwin') return 'macOS (darwin)';
    if (p === 'windows') return 'Windows';
    if (p === 'linux') return 'Linux';
    return p || '未知平台';
  }
  function fileManagerName() { return state && state.platform === 'darwin' ? '访达' : '文件管理器'; }
  function formatLabel(f) { return f === 'anthropic' ? 'Anthropic Messages' : 'OpenAI 兼容'; }
  function fmtUptime(seconds) {
    var sec = Math.max(0, Math.floor(Number(seconds) || 0));
    if (sec < 60) return sec + ' 秒';
    if (sec < 3600) return Math.floor(sec / 60) + ' 分 ' + (sec % 60) + ' 秒';
    var h = Math.floor(sec / 3600);
    var m = Math.floor((sec % 3600) / 60);
    return m ? h + ' 小时 ' + m + ' 分' : h + ' 小时';
  }
  function fmtRelative(ms) {
    var ts = Number(ms) || 0;
    if (ts <= 0) return '无记录';
    var diff = Date.now() - ts;
    if (diff < 0) diff = 0;
    if (diff < 45000) return '刚刚';
    if (diff < 3600000) return Math.floor(diff / 60000) + ' 分钟前';
    var d = new Date(ts);
    var today = new Date();
    if (d.getFullYear() === today.getFullYear() && d.getMonth() === today.getMonth() && d.getDate() === today.getDate()) {
      return '今天 ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
    }
    return d.getMonth() + 1 + ' 月 ' + d.getDate() + ' 日 ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }
  function fmtClock(ms) {
    var d = new Date(Number(ms) || Date.now());
    return pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
  }

  /* ───────────── 服务商预设 ───────────── */
  var PRESETS = {
    deepseek: { url: 'https://api.deepseek.com/chat/completions', model: 'deepseek-chat', apiFormat: 'openai', authMethod: 'bearer', desc: 'DeepSeek 官方接口，OpenAI 兼容格式；网页脚本默认配置即为此项。' },
    kimi: { url: 'https://api.moonshot.cn/v1/chat/completions', model: 'moonshot-v1-8k', apiFormat: 'openai', authMethod: 'bearer', desc: 'Moonshot Kimi 官方接口，OpenAI 兼容格式。' },
    qwen: { url: 'https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions', model: 'qwen-plus', apiFormat: 'openai', authMethod: 'bearer', desc: '阿里云百炼 DashScope 的 OpenAI 兼容模式。' },
    openai: { url: 'https://api.openai.com/v1/chat/completions', model: 'gpt-4o-mini', apiFormat: 'openai', authMethod: 'bearer', desc: 'OpenAI 官方 Chat Completions 接口。' },
    claude: { url: 'https://api.anthropic.com/v1/messages', model: 'claude-3-5-sonnet-latest', apiFormat: 'anthropic', authMethod: 'x-api-key', desc: 'Anthropic Messages API，使用 x-api-key 认证。' },
    custom: { desc: '手动填写以下参数；选择「自定义」不会自动填充。' }
  };
  var PRESET_KEYS = ['deepseek', 'kimi', 'qwen', 'openai', 'claude'];

  /* ───────────── Mock（与 model.State 同构） ───────────── */
  var MOCK_HASH = 'a1fc9521d347b1a04bc80ce16df17dbb701dc1241847c07e80a8064f7153a1a5';
  var MOCK_SHORT_HASH = MOCK_HASH.slice(0, 8);

  function buildMockState() {
    var now = Date.now();
    return {
      appVersion: '1.1.3',
      platform: 'darwin',
      homeDir: '/Users/demo/.ykt',
      configPath: '/Users/demo/.ykt/config.json',
      logPath: '/Users/demo/.ykt/server.log',
      scriptFile: '内置（随 App 打包）',
      service: { running: true, port: 8899, pid: 48213, url: 'http://127.0.0.1:8899/', uptime: 4512, error: '' },
      script: { name: '雨课助手', version: '3.0.9', author: '风之子', lines: 2446, hash: MOCK_HASH, shortHash: MOCK_SHORT_HASH, matches: ['*://*.yuketang.cn/*', '*://*.gdufemooc.cn/*'] },
      install: { installed: true, inSync: true, version: '3.0.9', hash: MOCK_HASH, openedAt: now - 3 * 3600 * 1000, note: '已安装，与当前内置脚本一致' },
      ai: { url: PRESETS.deepseek.url, key: 'sk-demo-1234567890abcdef', model: 'deepseek-chat', apiFormat: 'openai', authMethod: 'bearer' },
      features: { autoAI: true, autoComment: false },
      settings: { port: 8899, defaultUrl: 'https://yuketang.cn/', autoStart: true, syncEnabled: true, appearance: 'system' },
      portRange: { min: 8899, max: 8910 },
      syncUrl: 'http://127.0.0.1:8899/api/config',
      lastSyncAt: now - 125000
    };
  }
  function mockBatchAccounts() {
    return [
      { id: 'acc_demo1', name: '林同学', site: 'https://yuketang.cn/', login_state: 'logged_in', last_run_at: '2026-11-05T09:12:00+08:00', last_result: 'ok', last_error: '' },
      { id: 'acc_demo2', name: '陈同学', site: 'https://yuketang.cn/', login_state: 'needs_login', last_run_at: '', last_result: '', last_error: '' }
    ];
  }

  function buildMockLogs() {
    var now = Date.now();
    var rows = [
      ['info', '本地服务已启动，监听 127.0.0.1:8899', -75.3 * 60000],
      ['info', '已加载内置用户脚本：雨课助手 v3.0.9', -75.1 * 60000],
      ['warn', '检测到 server.json 残留记录，已清理', -74.8 * 60000],
      ['info', 'GET /ykt.user.js 安装请求已响应', -63 * 60000],
      ['info', '配置已同步到网页脚本', -41 * 60000],
      ['info', '健康检查通过：pid=48213, port=8899', -28 * 60000],
      ['warn', '请求 /api/config 缺少 X-YKT-Client 头，已拒绝', -17 * 60000],
      ['error', '打开默认浏览器失败：系统未返回可用的处理程序', -12 * 60000],
      ['info', 'AI 配置已保存（deepseek-chat）', -6.5 * 60000],
      ['info', '用户脚本安装状态已校验', -4 * 60000],
      ['info', '配置已同步到网页脚本', -2.1 * 60000]
    ];
    return rows.map(function (r) { return { time: now + r[2], level: r[0], message: r[1] }; });
  }
  var mockState = MOCK ? buildMockState() : null;
  var mockLogs = MOCK ? buildMockLogs() : [];

  function normalizeLogEntry(raw) {
    var e = raw || {};
    return { time: Number(e.time) || Date.now(), level: String(e.level || 'info').toLowerCase(), message: String(e.message == null ? '' : e.message) };
  }
  function normalizeState(raw) {
    var s = raw || {};
    var script = Object.assign({ name: '', version: '', author: '', lines: 0, hash: '', shortHash: '', matches: [] }, s.script || {});
    if (!Array.isArray(script.matches)) script.matches = [];
    return {
      appVersion: s.appVersion || '—',
      platform: s.platform || 'unknown',
      homeDir: s.homeDir || '',
      configPath: s.configPath || '',
      logPath: s.logPath || '',
      scriptFile: s.scriptFile || '',
      service: Object.assign({ running: false, port: 0, pid: 0, url: '', uptime: 0, error: '' }, s.service || {}),
      script: script,
      install: Object.assign({ installed: false, inSync: false, version: '', hash: '', openedAt: 0, note: '' }, s.install || {}),
      ai: Object.assign({ url: '', key: '', model: '', apiFormat: 'openai', authMethod: 'bearer' }, s.ai || {}),
      features: Object.assign({ autoAI: false, autoComment: false }, s.features || {}),
      settings: Object.assign({ port: 8899, defaultUrl: 'https://yuketang.cn/', autoStart: true, syncEnabled: true, appearance: 'system' }, s.settings || {}),
      portRange: Object.assign({ min: 8899, max: 8910 }, s.portRange || {}),
      syncUrl: s.syncUrl || '',
      lastSyncAt: Number(s.lastSyncAt) || 0
    };
  }

  /* ───────────── 状态 ───────────── */
  var state = null;
  var booted = false;
  var currentPage = 'overview';
  var logs = [];
  var logState = 'idle';
  var logError = '';
  var autoScroll = true;
  var stateFetchedAt = 0;
  var pollTimer = null;
  var logTimer = null;
  var refreshPending = false;
  var aiDirty = false;
  var settingsDirty = false;
  var pendingAppearance = null;

  /* ───────────── Toast ───────────── */
  var TOAST_ICONS = { success: '#i-check-circle', error: '#i-x', warning: '#i-alert', info: '#i-info' };

  function toast(message, type) {
    var host = $('#toastHost');
    if (!host || !message) return;
    var kind = TOAST_ICONS[type] ? type : 'info';
    var el = document.createElement('div');
    el.className = 'toast is-' + kind;
    var icon = document.createElement('span');
    icon.className = 'toast-icon';
    icon.setAttribute('aria-hidden', 'true');
    icon.innerHTML = '<svg class="icon"><use href="' + TOAST_ICONS[kind] + '"></use></svg>';
    var msg = document.createElement('span');
    msg.className = 'toast-text';
    msg.textContent = String(message);
    el.appendChild(icon);
    el.appendChild(msg);
    host.appendChild(el);
    while (host.children.length > 3) host.removeChild(host.firstChild);
    window.setTimeout(function () {
      el.classList.add('is-leaving');
      window.setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 220);
    }, 3000);
  }

  /* ───────────── 确认弹层 ───────────── */
  var modal = { open: false, onConfirm: null, prevFocus: null };
  function openConfirm(opts) {
    opts = opts || {};
    var layer = $('#modalLayer');
    if (!layer) return;
    modal.prevFocus = document.activeElement;
    modal.onConfirm = typeof opts.onConfirm === 'function' ? opts.onConfirm : null;
    modal.open = true;
    text('#modalTitle', opts.title || '确认操作');
    text('#modalDesc', opts.message || '');
    var confirm = $('#modalConfirm');
    if (confirm) {
      confirm.textContent = opts.confirmText || '确认';
      confirm.className = 'btn ' + (opts.danger === false ? 'btn-primary' : 'btn-danger');
    }
    var icon = $('#modalIcon');
    if (icon) {
      icon.className = 'modal-icon' + (opts.danger === false ? ' is-info' : '');
      var use = icon.querySelector('use');
      if (use) use.setAttribute('href', opts.danger === false ? '#i-info' : '#i-alert');
    }
    layer.hidden = false;
    window.setTimeout(function () { var c = $('#modalCancel'); if (c) c.focus(); }, 30);
  }
  function closeModal(restore) {
    var layer = $('#modalLayer');
    if (!layer || layer.hidden) return;
    layer.hidden = true;
    modal.open = false;
    modal.onConfirm = null;
    if (restore !== false && modal.prevFocus && typeof modal.prevFocus.focus === 'function') {
      try { modal.prevFocus.focus(); } catch (e) { /* ignore */ }
    }
    modal.prevFocus = null;
  }
  function confirmModal() {
    var fn = modal.onConfirm;
    var prev = modal.prevFocus;
    closeModal(false);
    if (prev && typeof prev.focus === 'function') { try { prev.focus(); } catch (e) { /* ignore */ } }
    if (fn) Promise.resolve().then(fn).catch(function (err) { toast('操作失败：' + errMsg(err), 'error'); });
  }
  function trapModalFocus(event) {
    if (!modal.open || event.key !== 'Tab') return;
    var focusable = $$('#modalLayer button');
    if (!focusable.length) return;
    var first = focusable[0];
    var last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }

  /* ───────────── 主题 ───────────── */
  function forcedTheme() {
    var f = new URLSearchParams(window.location.search).get('theme');
    return f === 'light' || f === 'dark' ? f : null;
  }
  function applyTheme(appearance) {
    var next = ['light', 'dark', 'system'].indexOf(appearance) >= 0 ? appearance : 'system';
    document.documentElement.setAttribute('data-theme', forcedTheme() || next);
    $$('.seg-btn').forEach(function (btn) {
      var active = btn.getAttribute('data-appearance') === next;
      btn.classList.toggle('is-active', active);
      btn.setAttribute('aria-pressed', active ? 'true' : 'false');
    });
  }

  /* ───────────── 渲染：外壳 ───────────── */
  function hideSkeleton() { setHidden('#overviewSkeleton', true); setHidden('#overviewBody', false); }

  function renderSidebar() {
    if (!state) return;
    text('#sidebarVersion', 'v' + (state.appVersion || '—'));
  }
  function renderToolbar() {
    if (!state) return;
    var svc = state.service;
    text('#toolbarSyncText', svc.running ? '本地服务运行中 · ' + svc.port : svc.error ? '本地服务启动失败' : '本地服务未运行');
    var dot = $('#syncDot');
    if (dot) dot.className = 'sync-dot ' + (svc.running ? 'is-running' : svc.error ? 'is-error' : '');
    var title = $('#toolbarTitle');
    if (title) title.hidden = state.platform === 'darwin';
  }
  function updateKbdHints() {
    var isMac = !state || state.platform === 'darwin';
    $$('.nav-kbd').forEach(function (kbd, i) { kbd.textContent = (isMac ? '⌘' : 'Ctrl+') + (i + 1); });
  }

  /* ───────────── 渲染：概览 ───────────── */
  function renderOverview() {
    if (!state) return;
    var svc = state.service;
    var install = state.install;
    var script = state.script;

    text('#serviceRowTitle', '本地服务');
    text('#serviceRowDesc', svc.running
      ? '端口 ' + svc.port + ' · PID ' + svc.pid + ' · 已运行 ' + fmtUptime(liveUptime())
      : svc.error ? '启动失败：' + svc.error : '端口 ' + state.settings.port + ' · 启动后开始计时');
    setBadge('#serviceBadge', '#serviceBadgeText', svc.running ? 'success' : svc.error ? 'danger' : 'neutral', svc.running ? '运行中' : svc.error ? '异常' : '已停止');
    setHidden('#btnStartService', svc.running);
    setHidden('#btnStopService', !svc.running);
    setHidden('#btnRestartService', !svc.running);
    var hint = $('#serviceHint');
    if (hint) {
      hint.hidden = svc.running;
      hint.textContent = svc.error ? '检查端口占用后点「启动服务」重试' : '启动服务后，网页脚本才能同步配置';
    }

    text('#scriptRowVersion', 'v' + (script.version || '—'));
    setBadge('#installBadge', '#installBadgeText', install.installed && install.inSync ? 'success' : install.installed ? 'warning' : 'neutral', install.installed && install.inSync ? '已安装' : install.installed ? '需更新' : '未安装');
    text('#scriptRowDesc', '指纹 ' + (script.shortHash || '—') + ' · ' + (install.note || '尚未在浏览器中安装脚本'));
    text('#btnInstallScriptText', install.installed ? (install.inSync ? '重新安装' : '更新脚本') : '安装脚本');

    setBadge('#syncBadge', '#syncBadgeText', state.settings.syncEnabled ? 'success' : 'neutral', state.settings.syncEnabled ? '已开启' : '已关闭');
    text('#syncRowDesc', svc.running
      ? '最近同步 ' + fmtRelative(state.lastSyncAt) + ' · ' + (state.syncUrl || 'http://127.0.0.1:' + svc.port + '/api/config')
      : '服务未运行，启动后可同步');

    text('#aiRowDesc', (state.ai.model || '未设置模型') + ' · ' + formatLabel(state.ai.apiFormat) + ' · 自动作答' + (state.features.autoAI ? '已开启' : '已关闭'));
    if (currentPage === 'overview') loadRecentLogs('#overviewLogList', '#overviewLogEmpty', null, 5);
  }

  function liveUptime() {
    if (!state) return 0;
    return (Number(state.service.uptime) || 0) + (stateFetchedAt ? (Date.now() - stateFetchedAt) / 1000 : 0);
  }
  function updateLiveText() { if (state && state.service.running) text('#serviceRowDesc', '端口 ' + state.service.port + ' · PID ' + state.service.pid + ' · 已运行 ' + fmtUptime(liveUptime())); }
  function setBadge(badgeSel, textSel, tone, label) {
    var badge = $(badgeSel);
    if (badge) {
      badge.className = 'badge ' + (tone === 'success' ? 'is-success' : tone === 'warning' ? 'is-warning' : tone === 'danger' ? 'is-danger' : '');
    }
    text(textSel, label);
  }
  function setStepDone(sel, done) {
    var el = $(sel);
    if (el) el.classList.toggle('is-done', !!done);
  }

  /* ───────────── 渲染：脚本页 ───────────── */
  function installURL() {
    if (!state) return '';
    if (state.service.running && state.service.port) return 'http://127.0.0.1:' + state.service.port + '/ykt.user.js';
    if (state.settings.port) return 'http://127.0.0.1:' + state.settings.port + '/ykt.user.js';
    return '';
  }
  function renderScriptPage() {
    if (!state) return;
    var script = state.script;
    var install = state.install;
    var ai = state.ai;
    var aiConfigured = !!(ai && ai.key && ai.key !== 'sk-xxxxxxx');

    setBadge('#installStateBadge', '#installStateBadgeText', install.installed && install.inSync ? 'success' : install.installed ? 'warning' : 'neutral', install.installed && install.inSync ? '已安装' : install.installed ? '需更新' : '未安装');
    text('#installHint', install.note || '尚未在浏览器中安装脚本');
    text('#installUrl', installURL() || '服务未运行，暂无安装地址');
    text('#runScriptVersion', 'v' + (script.version || '—'));
    var installBtn = $('#btnInstallFromScript');
    if (installBtn) installBtn.textContent = install.installed ? (install.inSync ? '重新安装' : '更新脚本') : '安装脚本';

    setBadge('#runAIBadge', '#runAIBadgeText', aiConfigured ? 'success' : 'neutral', aiConfigured ? '已配置' : '未配置');
    text('#runAIDesc', aiConfigured
      ? (ai.model || '—') + ' · ' + formatLabel(ai.apiFormat) + ' · 自动作答' + (state.features.autoAI ? '已开启' : '已关闭')
      : '不配置也能正常刷课');
    text('#runOpenDesc', state.settings.defaultUrl || 'https://yuketang.cn/');
    if (currentPage === 'script' && state) loadLogs();

    /* 设置页里的脚本信息 */
    text('#infoScriptVersion', 'v' + (script.version || '—'));
    text('#infoScriptLines', (script.lines || 0).toLocaleString('zh-CN') + ' 行 · ' + (script.author || '—'));
    text('#infoScriptHash', script.shortHash || '—');
    text('#infoScriptFile', state.scriptFile || '—');
    var matchHost = $('#infoScriptMatches');
    if (matchHost) {
      matchHost.textContent = '';
      var matches = script.matches || [];
      if (!matches.length) {
        var none = document.createElement('span');
        none.className = 'row-value';
        none.textContent = '—';
        matchHost.appendChild(none);
      } else {
        matches.forEach(function (m) {
          var chip = document.createElement('span');
          chip.className = 'chip';
          chip.textContent = m;
          matchHost.appendChild(chip);
        });
      }
    }
    var hashBtn = $('#btnCopyHash');
    if (hashBtn) hashBtn.title = script.hash ? '复制完整指纹：' + script.hash : '暂无指纹';
  }

  /* ───────────── 渲染：最近日志 ───────────── */
  function appendLogPreview(list, entry) {
    var line = document.createElement('div');
    line.className = 'log-row lv-' + levelClass(entry.level);
    var time = document.createElement('span');
    time.className = 'log-time';
    time.textContent = fmtClock(entry.time);
    var level = document.createElement('span');
    level.className = 'log-level';
    level.textContent = levelLabel(entry.level);
    var message = document.createElement('span');
    message.className = 'log-text';
    message.textContent = entry.message;
    line.appendChild(time);
    line.appendChild(level);
    line.appendChild(message);
    list.appendChild(line);
  }

  function renderLogPreview(listSel, emptySel, countSel, entries) {
    var list = $(listSel);
    if (!list) return;
    list.textContent = '';
    var empty = emptySel ? $(emptySel) : null;
    if (!entries || !entries.length) {
      if (empty) empty.hidden = false;
      if (countSel) text(countSel, '0 条');
      return;
    }
    if (empty) empty.hidden = true;
    if (countSel) text(countSel, entries.length + ' 条');
    entries.forEach(function (entry) { appendLogPreview(list, entry); });
  }

  function loadRecentLogs(listSel, emptySel, countSel, limit) {
    if (!state) return;
    if (MOCK) {
      renderLogPreview(listSel, emptySel, countSel, normalizeLogs(mockLogs.slice(-limit)));
      return;
    }
    requireBindings().GetLogs(limit).then(function (raw) {
      var entries = (Array.isArray(raw) ? raw : []).map(normalizeLogEntry).sort(function (a, b) { return a.time - b.time; }).slice(-limit);
      renderLogPreview(listSel, emptySel, countSel, entries);
    }).catch(function () {
      renderLogPreview(listSel, emptySel, countSel, []);
    });
  }

  /* ───────────── 渲染：表单 ───────────── */
  function markKeyVisibility(show) {
    var input = $('#aiKey');
    var btn = $('#btnToggleKey');
    if (input) input.type = show ? 'text' : 'password';
    if (btn) {
      btn.setAttribute('aria-pressed', show ? 'true' : 'false');
      btn.setAttribute('aria-label', show ? '隐藏 API Key' : '显示 API Key');
      var use = btn.querySelector('use');
      if (use) use.setAttribute('href', show ? '#i-eye-off' : '#i-eye');
    }
  }
  function detectPreset(ai) {
    var url = String(ai.url || '').trim().replace(/\/+$/, '');
    if (!url) return 'custom';
    for (var i = 0; i < PRESET_KEYS.length; i += 1) {
      if (PRESETS[PRESET_KEYS[i]].url.replace(/\/+$/, '') === url) return PRESET_KEYS[i];
    }
    return 'custom';
  }
  function updateProviderDesc() {
    var p = PRESETS[val('#aiPreset')];
    text('#providerDesc', p && p.desc ? p.desc : PRESETS.custom.desc);
  }
  function updateDirtyChips() {
    setHidden('#aiDirtyChip', !aiDirty);
    setHidden('#settingsDirtyChip', !settingsDirty);
  }
  function markAIDirty() { aiDirty = true; updateDirtyChips(); }
  function markSettingsDirty() { settingsDirty = true; updateDirtyChips(); }

  function fillAIForm(force) {
    if (!state) return;
    if (aiDirty && !force) return;
    var ai = state.ai;
    if ($('#aiPreset')) $('#aiPreset').value = detectPreset(ai);
    if ($('#aiUrl')) $('#aiUrl').value = ai.url || '';
    if ($('#aiModel')) $('#aiModel').value = ai.model || '';
    if ($('#aiFormat')) $('#aiFormat').value = ai.apiFormat === 'anthropic' ? 'anthropic' : 'openai';
    if ($('#aiAuth')) $('#aiAuth').value = ai.authMethod === 'x-api-key' ? 'x-api-key' : 'bearer';
    if ($('#aiKey')) $('#aiKey').value = ai.key || '';
    setSwitch('#switchAutoAI', !!state.features.autoAI);
    setSwitch('#switchAutoComment', !!state.features.autoComment);
    updateProviderDesc();
    markKeyVisibility(false);
    aiDirty = false;
    updateDirtyChips();
  }
  function buildPortOptions() {
    var sel = $('#setPort');
    if (!sel || !state) return;
    var min = state.portRange.min || 8899;
    var max = state.portRange.max || 8910;
    var sig = min + '-' + max;
    if (sel.getAttribute('data-range') !== sig) {
      sel.textContent = '';
      for (var p = min; p <= max; p += 1) {
        var opt = document.createElement('option');
        opt.value = String(p);
        opt.textContent = String(p);
        sel.appendChild(opt);
      }
      sel.setAttribute('data-range', sig);
    }
  }
  /* ───────────── 更新 ───────────── */
  var mockUpdate = { currentVersion: '1.1.0', latestVersion: '1.1.0', available: false, publishedAt: 0, notes: '', assetName: '', assetSize: 0, assetUrl: '', checksum: '' };
  var updateInfo = null;
  var updateBusy = false;

  function updateStatusText(info) {
    var current = 'v' + ((state && state.appVersion) || '—');
    if (updateBusy) return '正在检查更新…';
    if (info && info.error) return '当前 ' + current + ' · Sparkle 检查失败：' + info.error;
    return '当前 ' + current + ' · 自动更新由 Sparkle 提供';
  }
  function renderUpdate() {
    var info = MOCK ? mockUpdate : updateInfo;
    text('#updateStatus', updateStatusText(info));
    var error = info && info.error ? String(info.error) : '';
    var installBtn = $('#btnInstallUpdate');
    if (installBtn) {
      installBtn.hidden = !(info && info.available) || !!error || updateBusy;
      installBtn.textContent = '下载并重启';
    }
    var checkBtn = $('#btnCheckUpdate');
    if (checkBtn) {
      checkBtn.disabled = updateBusy;
      checkBtn.textContent = updateBusy ? '检查中…' : (error ? '重试' : '检查更新');
    }
    var notesRow = $('#updateNotesRow');
    var notes = info && info.available && !error ? (info.notes || '') : '';
    if (notesRow) notesRow.hidden = !notes;
    if (notes) text('#updateNotes', notes.length > 320 ? notes.slice(0, 320) + '…' : notes);
    var errorRow = $('#updateErrorRow');
    if (errorRow) errorRow.hidden = !error;
    if (error) text('#updateError', '失败原因：' + error + '（不会反复自动重试；可点「重试」）');
  }
  function checkUpdates() {
    if (updateBusy) return;
    updateBusy = true;
    renderUpdate();
    var task;
    if (MOCK) {
      task = wait(450).then(function () { return mockUpdate; });
    } else {
      task = requireBindings().SparkleCheckForUpdates();
    }
    task.then(function () {
      updateBusy = false;
      renderUpdate();
      toast('已打开 Sparkle 更新窗口', 'success');
    }).catch(function (err) {
      updateBusy = false;
      var message = errMsg(err);
      updateInfo = Object.assign({}, updateInfo || {}, { available: false, error: message, checkedAt: Date.now() });
      renderUpdate();
      toast('检查更新失败：' + message, 'error');
    });
  }
  function installUpdate() {
    var info = MOCK ? mockUpdate : updateInfo;
    if (!info || !info.available || updateBusy) return;
    openConfirm({
      title: '下载并安装 v' + info.latestVersion + '？',
      message: 'App 会退出并自动重启；安装失败会回滚到当前版本。',
      confirmText: '下载并安装',
      onConfirm: function () {
        updateBusy = true;
        renderUpdate();
        if (MOCK) {
          return wait(600).then(function () { updateBusy = false; renderUpdate(); toast('预览模式不执行安装', 'info'); });
        }
        return requireBindings().InstallUpdate().then(function (message) {
          updateBusy = false;
          renderUpdate();
          toast(message || '更新已开始', 'success');
        }).catch(function (err) {
          updateBusy = false;
          renderUpdate();
          toast('安装更新失败：' + errMsg(err), 'error');
        });
      }
    });
  }

  /* ───────────── 新手教程（与「超星助手」共用同一套教程组件） ───────────── */
  var ONBOARDING_STEPS = [
    {
      title: '欢迎使用雨课助手',
      text: '这个应用帮你完成雨课堂的课程任务：装好脚本后，打开课程目录页点「开始刷课」即可。你不需要懂代码，所有操作都在这个窗口和浏览器里点按钮完成。',
      points: ['按平台真实时长播放视频与音频', '运行中可随时停止，进度以平台为准', 'AI 自动答题是可选项，不配置也能用']
    },
    {
      title: '第一步：装好浏览器扩展',
      text: '脚本需要 Tampermonkey 才能在网页里运行。在 Chrome、Edge 或 Firefox 的扩展商店搜索 Tampermonkey 安装；Safari 请到 App Store 安装。已经装过就点「下一步」。',
      action: { label: '打开 Tampermonkey 官网', kind: 'external', url: 'https://www.tampermonkey.net/' }
    },
    {
      title: '第二步：安装雨课助手脚本',
      text: '点下面的按钮，浏览器会打开安装页并弹出确认窗口，点「安装」即可。之前装过旧版本的会显示「重新安装」。',
      points: ['如果没看到弹窗，先确认浏览器右上角有 Tampermonkey 图标，再点一次'],
      action: { label: '安装 / 更新脚本', kind: 'install' }
    },
    {
      title: '第三步：登录并进入课程目录',
      text: '点「打开雨课堂」，用学校给的账号登录，进入课程后点开「课程目录」页面。脚本会在这里接管视频播放。',
      points: ['要进课程目录页，不是直接点某一个视频'],
      action: { label: '打开雨课堂', kind: 'open-site' }
    },
    {
      title: '第四步：在网页里点开始刷课',
      text: '课程目录页左下角会出现雨课助手面板，点「开始刷课」后视频自动播放；想停就点「停止刷课」。'
    }
  ];
  var onboardingIndex = 0;
  var onboardingShown = false;

  function onboardingVisible() {
    var layer = $('#onboardingLayer');
    return !!(layer && !layer.hidden);
  }

  function onboardingRender() {
    var step = ONBOARDING_STEPS[onboardingIndex] || ONBOARDING_STEPS[0];
    text('#onboardingProgress', (onboardingIndex + 1) + ' / ' + ONBOARDING_STEPS.length);
    text('#onboardingTitle', step.title);
    text('#onboardingText', step.text || '');
    var points = $('#onboardingPoints');
    if (points) {
      points.textContent = '';
      (step.points || []).forEach(function (line) {
        var item = document.createElement('li');
        item.textContent = line;
        points.appendChild(item);
      });
    }
    var extra = $('#onboardingExtra');
    if (extra) {
      extra.textContent = '';
      if (step.action) {
        var actionButton = document.createElement('button');
        actionButton.type = 'button';
        actionButton.className = 'btn btn-sm';
        actionButton.textContent = step.action.label;
        actionButton.setAttribute('aria-label', step.action.label);
        actionButton.addEventListener('click', function () {
          if (step.action.kind === 'external') openExternal(step.action.url);
          else if (step.action.kind === 'install') doInstallScript(actionButton);
          else if (step.action.kind === 'open-site') doOpenYuketang(actionButton);
        });
        extra.appendChild(actionButton);
      }
    }
    var dots = $('#onboardingDots');
    if (dots) {
      dots.textContent = '';
      ONBOARDING_STEPS.forEach(function (_, i) {
        var dot = document.createElement('span');
        dot.className = 'onboarding-dot' + (i === onboardingIndex ? ' is-active' : '');
        dots.appendChild(dot);
      });
    }
    var back = $('#btnOnboardingBack');
    if (back) back.hidden = onboardingIndex === 0;
    var next = $('#btnOnboardingNext');
    if (next) next.textContent = onboardingIndex === ONBOARDING_STEPS.length - 1 ? '开始使用' : '下一步';
  }

  function onboardingOpen() {
    onboardingIndex = 0;
    var layer = $('#onboardingLayer');
    if (!layer) return;
    layer.hidden = false;
    onboardingRender();
    var next = $('#btnOnboardingNext');
    if (next) window.setTimeout(function () { try { next.focus(); } catch (e) { /* ignore */ } }, 30);
  }

  function onboardingClose(finished) {
    var layer = $('#onboardingLayer');
    if (layer) layer.hidden = true;
    if (!finished) return;
    if (MOCK) { toast('教程已完成', 'success'); return; }
    requireBindings().FinishOnboarding().then(function (next) {
      if (next) applyState(next);
      toast('教程已完成，之后可在设置里重新查看', 'success');
    }).catch(function (err) { toast('保存教程状态失败：' + errMsg(err), 'error'); });
  }

  function onboardingNext() {
    if (onboardingIndex >= ONBOARDING_STEPS.length - 1) { onboardingClose(true); return; }
    onboardingIndex += 1;
    onboardingRender();
  }

  function onboardingBack() {
    if (onboardingIndex <= 0) return;
    onboardingIndex -= 1;
    onboardingRender();
  }

  function maybeShowOnboarding() {
    if (!state || onboardingShown) return;
    var layer = $('#onboardingLayer');
    if (!layer) return;
    if (MOCK) {
      var params = new URLSearchParams(window.location.search);
      if (params.get('onboarding') !== '1') return;
      onboardingShown = true;
      var step = Number(params.get('step')) || 1;
      window.setTimeout(function () {
        onboardingOpen();
        onboardingIndex = Math.min(Math.max(step - 1, 0), ONBOARDING_STEPS.length - 1);
        onboardingRender();
      }, 250);
      return;
    }
    if (state.settings.onboardingDone) return;
    onboardingShown = true;
    window.setTimeout(onboardingOpen, 250);
  }

  function fillSettingsForm(force) {
    if (!state) return;
    if (settingsDirty && !force) return;
    var st = state.settings;
    buildPortOptions();
    if ($('#setPort')) $('#setPort').value = String(st.port || '');
    if ($('#setDefaultUrl')) $('#setDefaultUrl').value = st.defaultUrl || '';
    setSwitch('#switchAutoStart', !!st.autoStart);
    setSwitch('#switchSyncEnabled', !!st.syncEnabled);
    applyTheme(pendingAppearance && settingsDirty ? pendingAppearance : st.appearance);
    text('#pathHome', state.homeDir || '—');
    text('#pathLog', state.logPath || '—');
    text('#aboutVersion', 'App v' + (state.appVersion || '—') + ' · 脚本 v' + (state.script.version || '—'));
    text('#aboutPlatform', platformLabel(state.platform));
    renderUpdate();
    settingsDirty = false;
    pendingAppearance = null;
    updateDirtyChips();
  }

  function applyState(raw, opts) {
    state = normalizeState(raw);
    stateFetchedAt = Date.now();
    booted = true;
    document.documentElement.classList.toggle('platform-darwin', state.platform === 'darwin');
    if (!settingsDirty) applyTheme(state.settings.appearance);
    if (!opts || opts.skeleton !== false) hideSkeleton();
    renderSidebar();
    renderToolbar();
    updateKbdHints();
    renderOverview();
    renderScriptPage();
    if (!aiDirty) fillAIForm();
    if (!settingsDirty) fillSettingsForm();
    if (currentPage === 'script') renderLogs();
    maybeShowOnboarding();
  }

  /* ───────────── 日志 ───────────── */
  function levelClass(level) {
    var v = String(level || '').toLowerCase();
    if (v.indexOf('err') >= 0 || v.indexOf('fatal') >= 0) return 'error';
    if (v.indexOf('warn') >= 0) return 'warn';
    if (v.indexOf('success') >= 0 || v === 'ok') return 'success';
    if (v.indexOf('debug') >= 0 || v.indexOf('trace') >= 0) return 'debug';
    return 'info';
  }
  function levelLabel(level) {
    var c = levelClass(level);
    if (c === 'error') return '错误';
    if (c === 'warn') return '警告';
    if (c === 'success') return '成功';
    if (c === 'debug') return '调试';
    return '信息';
  }
  function renderLogs() {
    var list = $('#logList');
    var empty = $('#logEmpty');
    if (!list || !empty) return;
    text('#logCount', logs.length + ' 条');
    if (state && state.logPath) text('#logPathText', state.logPath);
    var showError = logState === 'error';
    var showEmpty = !showError && logState === 'ready' && logs.length === 0;
    empty.hidden = !(showError || showEmpty);
    text('#logEmptyTitle', showError ? '读取日志失败' : '暂无日志');
    text('#logEmptyDesc', showError ? (logError || '未知错误') + '，可重试' : '启动本地服务或安装脚本后，这里会显示运行记录');
    var retry = $('#btnRetryLogs');
    if (retry) retry.hidden = !showError;
    var icon = empty.querySelector('.empty-icon');
    if (icon) icon.className = 'empty-icon' + (showError ? ' is-error' : '');
    list.hidden = showError || showEmpty;
    list.textContent = '';
    if (list.hidden) return;
    var frag = document.createDocumentFragment();
    logs.forEach(function (entry) {
      var cls = levelClass(entry.level);
      var row = document.createElement('div');
      row.className = 'log-row';
      var time = document.createElement('span');
      time.className = 'log-time';
      time.textContent = fmtClock(entry.time);
      var level = document.createElement('span');
      level.className = 'log-level is-' + cls;
      level.textContent = levelLabel(entry.level);
      var msg = document.createElement('span');
      msg.className = 'log-msg';
      msg.textContent = entry.message;
      row.appendChild(time);
      row.appendChild(level);
      row.appendChild(msg);
      frag.appendChild(row);
    });
    list.appendChild(frag);
    if (autoScroll) list.scrollTop = list.scrollHeight;
  }
  function normalizeLogs(raw) {
    var arr = Array.isArray(raw) ? raw : [];
    return arr.map(normalizeLogEntry).sort(function (a, b) { return a.time - b.time; }).slice(-200);
  }
  function loadLogs() {
    if (!state) return;
    if (logState === 'loading') return;
    logState = 'loading';
    var promise;
    if (MOCK) {
      promise = wait(150).then(function () { return normalizeLogs(mockLogs.slice()); });
    } else {
      promise = Promise.resolve().then(function () { return requireBindings().GetLogs(200); }).then(normalizeLogs);
    }
    promise.then(function (entries) {
      logs = entries; logState = 'ready'; logError = ''; renderLogs();
    }).catch(function (err) {
      logState = 'error'; logError = errMsg(err); renderLogs();
      toast('读取日志失败：' + logError, 'error');
    });
  }
  function startLogPolling() {
    stopLogPolling();
    logTimer = window.setInterval(function () {
      if (document.hidden || currentPage !== 'script') return;
      loadLogs();
    }, 4000);
  }
  function stopLogPolling() { if (logTimer) { window.clearInterval(logTimer); logTimer = null; } }

  /* ───────────── 多账号批量 ───────────── */
  var batchAccounts = [];
  var batchQueueState = null;
  var batchLoginAccount = '';
  var batchLoginTimer = null;
  var batchLoginDone = false;

  function batchEsc(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (ch) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch];
    });
  }

  function batchStatusInfo(account) {
    switch (account.login_state) {
      case 'logged_in': return { text: '已登录', cls: 'is-success', desc: '登录态有效，之后可后台批量' };
      case 'needs_login': return { text: '需登录', cls: 'is-warning', desc: '登录态已失效，点「登录」重新手动登录' };
      case 'running': return { text: '运行中', cls: 'is-running', desc: '正在批量运行' };
      case 'error': return { text: '异常', cls: 'is-danger', desc: '上次运行出错，可重新登录后再试' };
      default: return { text: '未登录', cls: '', desc: '首次使用点「登录」，在浏览器里手动完成一次' };
    }
  }

  function renderBatchAccounts(errorText) {
    var box = $('#batchAccountList');
    if (!box) return;
    if (errorText) {
      box.innerHTML = '<div class="row"><div class="row-copy"><div class="row-title">多账号不可用</div><div class="row-desc">' + batchEsc(errorText) + '</div></div></div>';
      return;
    }
    if (!batchAccounts.length) {
      box.innerHTML = '<div class="row"><span class="row-icon is-gray" aria-hidden="true"><svg class="icon"><use href="#i-user"/></svg></span>' +
        '<div class="row-copy"><div class="row-title">还没有账号</div><div class="row-desc">先添加一个账号；首次需要你手动登录一次，之后可后台批量</div></div>' +
        '<div class="row-control"><button class="btn btn-sm" type="button" data-batch-action="add">添加账号</button></div></div>';
      return;
    }
    var html = '';
    batchAccounts.forEach(function (account) {
      var status = batchStatusInfo(account);
      var badge = '<span class="badge ' + status.cls + '"><span class="badge-dot" aria-hidden="true"></span>' + status.text + '</span>';
      html += '<div class="row" data-batch-id="' + batchEsc(account.id) + '">' +
        '<span class="row-icon is-gray" aria-hidden="true"><svg class="icon"><use href="#i-user"/></svg></span>' +
        '<div class="row-copy"><div class="row-title">' + batchEsc(account.name) + ' ' + badge + '</div>' +
        '<div class="row-desc">' + batchEsc(status.desc) + '</div></div>' +
        '<div class="row-control"><div class="batch-actions">' +
        '<button class="btn btn-sm" type="button" data-batch-action="login" data-batch-id="' + batchEsc(account.id) + '">' +
        (account.login_state === 'logged_in' ? '重新登录' : '登录') + '</button>' +
        '<button class="icon-btn" type="button" data-batch-action="remove" data-batch-id="' + batchEsc(account.id) + '" aria-label="删除账号" title="删除账号"><svg class="icon" aria-hidden="true"><use href="#i-trash"/></svg></button>' +
        '</div></div></div>';
    });
    box.innerHTML = html;
  }

  function loadBatchAccounts() {
    if (MOCK) { batchAccounts = mockBatchAccounts(); renderBatchAccounts(); return Promise.resolve(true); }
    var api = bindings();
    if (!api || typeof api.ListBatchAccounts !== 'function') {
      renderBatchAccounts('当前 App 版本不支持多账号，请重新构建安装');
      return Promise.resolve(false);
    }
    return Promise.resolve().then(function () { return api.ListBatchAccounts(); })
      .then(function (list) { batchAccounts = Array.isArray(list) ? list : []; renderBatchAccounts(); return true; })
      .catch(function (err) { renderBatchAccounts(errMsg(err)); return false; });
  }

  function showBatchAddForm(show) {
    setHidden('#batchAddForm', !show);
    if (show) {
      var input = $('#batchAccountName');
      if (input) { input.value = ''; input.focus(); }
    }
  }

  function submitBatchAccount() {
    var name = (val('#batchAccountName') || '').trim();
    if (!name) { toast('先填一个账号备注', 'warning'); return; }
    showBatchAddForm(false);
    if (MOCK) {
      batchAccounts.push({ id: 'acc_mock_' + Date.now(), name: name, site: 'https://yuketang.cn/', login_state: 'unknown' });
      renderBatchAccounts();
      toast('已添加账号（演示）', 'success');
      return;
    }
    var api = bindings();
    if (!api || typeof api.AddBatchAccount !== 'function') { toast('未检测到 App 绑定', 'error'); return; }
    api.AddBatchAccount(name).then(function () {
      toast('已添加账号，点「登录」完成首次登录', 'success');
      return loadBatchAccounts();
    }).catch(function (err) { toast('添加失败：' + errMsg(err), 'error'); });
  }

  function stopBatchLoginPolling() {
    if (batchLoginTimer) { window.clearInterval(batchLoginTimer); batchLoginTimer = null; }
  }

  function setBatchLoginText(message) { text('#batchLoginText', message); }

  function startBatchLogin(id) {
    var account = null;
    batchAccounts.forEach(function (item) { if (item.id === id) account = item; });
    if (!account) return;
    batchLoginAccount = id;
    batchLoginDone = false;
    setHidden('#batchLoginPanel', false);
    setBatchLoginText('正在打开浏览器…');
    if (MOCK) {
      setBatchLoginText('演示模式：已模拟打开登录页，稍后自动完成。');
      stopBatchLoginPolling();
      batchLoginTimer = window.setInterval(function () {
        if (!batchLoginAccount) return;
        if (!batchLoginDone) { batchLoginDone = true; setBatchLoginText('演示模式：已检测到登录成功…'); window.setTimeout(function () { completeBatchLogin(); }, 700); }
      }, 1200);
      return;
    }
    var api = bindings();
    if (!api || typeof api.OpenBatchLogin !== 'function') { toast('未检测到 App 绑定', 'error'); return; }
    api.OpenBatchLogin(id).then(function () {
      setBatchLoginText('已打开浏览器。请完成登录（扫码/密码均可），确认页面已进入课程首页后，点「我已完成登录」。');
      stopBatchLoginPolling();
      batchLoginTimer = window.setInterval(pollBatchLoginOnce, 1500);
    }).catch(function (err) {
      setHidden('#batchLoginPanel', true);
      batchLoginAccount = '';
      toast('打开登录浏览器失败：' + errMsg(err), 'error');
    });
  }

  function pollBatchLoginOnce() {
    var id = batchLoginAccount;
    if (!id) return;
    if (MOCK) return;
    var api = bindings();
    if (!api || typeof api.GetBatchLoginState !== 'function') return;
    api.GetBatchLoginState(id).then(function (status) {
      if (!status || !status.open) return;
      if (status.logged_in) {
        // 只提示，不自动保存：登录态必须由用户亲眼确认后点按钮，避免主页/扫码页被误判成已登录。
        setBatchLoginText('检测到已进入登录后的页面。请在浏览器确认登录成功，然后点「我已完成登录」。');
      } else {
        setBatchLoginText('还没有检测到登录完成。请在浏览器里完成登录（扫码/密码均可），然后点「我已完成登录」。');
      }
    }).catch(function () { /* 轮询失败不打扰，继续等 */ });
  }

  function completeBatchLogin() {
    var id = batchLoginAccount;
    if (!id) return;
    stopBatchLoginPolling();
    var finish = function (ok, message) {
      setHidden('#batchLoginPanel', true);
      batchLoginAccount = '';
      batchLoginDone = false;
      toast(message, ok ? 'success' : 'error');
      if (ok) loadBatchAccounts();
    };
    if (MOCK) { finish(true, '登录已保存（演示）'); return; }
    var api = bindings();
    if (!api || typeof api.CompleteBatchLogin !== 'function') { finish(false, '未检测到 App 绑定'); return; }
    api.CompleteBatchLogin(id).then(function () {
      finish(true, '登录已保存，之后可后台批量');
    }).catch(function (err) {
      finish(false, '保存登录失败：' + errMsg(err));
    });
  }

  function cancelBatchLogin() {
    var id = batchLoginAccount;
    stopBatchLoginPolling();
    setHidden('#batchLoginPanel', true);
    batchLoginAccount = '';
    batchLoginDone = false;
    if (MOCK || !id) return;
    var api = bindings();
    if (api && typeof api.CancelBatchLogin === 'function') api.CancelBatchLogin(id).catch(function () {});
  }

  function renderBatchQueue(state) {
    batchQueueState = state || null;
    var textEl = $('#batchQueueText');
    var startBtn = $('#btnBatchStart');
    var stopBtn = $('#btnBatchStop');
    var running = !!(state && (state.state === 'running' || state.state === 'stopping'));
    if (startBtn) {
      startBtn.hidden = running;
      startBtn.disabled = true;
      startBtn.textContent = '批量配置开发中';
      startBtn.title = '课程选择、范围和数量配置还没做完';
    }
    if (stopBtn) stopBtn.hidden = !running;
    if (textEl) {
      if (!state || !state.state || state.state === 'idle') {
        textEl.textContent = batchAccounts.length
          ? '批量功能正在重构：课程选择、范围和数量配置还没做完，暂不可用'
          : '先添加账号并完成登录；批量配置还在开发中';
      } else if (state.state === 'running') {
        var items = Array.isArray(state.items) ? state.items : [];
        var total = items.length;
        var done = items.filter(function (item) { return ['ok', 'failed', 'needs_login', 'cancelled', 'skipped'].indexOf(item.status) >= 0; }).length;
        var current = null;
        items.forEach(function (item) { if (item.account_id === state.current) current = item; });
        textEl.textContent = '批量运行中 ' + done + '/' + total + (current ? ' · 当前：' + current.name : '');
      } else if (state.state === 'stopping') {
        textEl.textContent = '正在停止…';
      } else if (state.state === 'done') {
        var okCount = (state.items || []).filter(function (item) { return item.status === 'ok'; }).length;
        textEl.textContent = '批量结束：成功 ' + okCount + '/' + (state.items || []).length;
      } else {
        textEl.textContent = state.message || '—';
      }
    }
  }

  function loadBatchQueue() {
    if (MOCK) { renderBatchQueue(batchQueueState || { state: 'idle' }); return Promise.resolve(true); }
    var api = bindings();
    if (!api || typeof api.GetBatchState !== 'function') { renderBatchQueue({ state: 'idle' }); return Promise.resolve(false); }
    return Promise.resolve().then(function () { return api.GetBatchState(); })
      .then(function (state) { renderBatchQueue(state); return true; })
      .catch(function () { renderBatchQueue({ state: 'idle' }); return false; });
  }

  function startBatch() {
    toast('批量功能正在重构：还不能选择课程和范围，暂不可用', 'warning');
    return;
    var ready = batchAccounts.filter(function (account) { return account.login_state === 'logged_in'; });
    if (!ready.length) { toast('没有已登录的账号，先完成登录', 'warning'); return; }
    openConfirm({
      title: '开始批量运行？',
      message: '将按顺序运行 ' + ready.length + ' 个已登录账号，账号之间会自动停顿。运行期间请保持 App 与电脑唤醒；可随时停止。',
      confirmText: '开始批量',
      danger: false,
      onConfirm: function () {
        if (MOCK) {
          renderBatchQueue({ state: 'running', current: ready[0].id, items: ready.map(function (account) { return { account_id: account.id, name: account.name, status: 'queued' }; }) });
          var index = 0;
          var tick = window.setInterval(function () {
            if (index >= ready.length) { window.clearInterval(tick); renderBatchQueue({ state: 'done', items: ready.map(function (account) { return { account_id: account.id, name: account.name, status: 'ok' }; }) }); return; }
            renderBatchQueue({ state: 'running', current: ready[index].id, items: ready.map(function (account, i) { return { account_id: account.id, name: account.name, status: i < index ? 'ok' : (i === index ? 'running' : 'queued') }; }) });
            index += 1;
          }, 1200);
          return;
        }
        var api = bindings();
        if (!api || typeof api.StartBatch !== 'function') throw new Error('未检测到 App 绑定');
        return api.StartBatch(ready.map(function (account) { return account.id; })).then(function (state) {
          renderBatchQueue(state);
          toast('已开始批量运行', 'success');
          loadBatchAccounts();
        });
      }
    });
  }

  function stopBatch() {
    if (MOCK) { renderBatchQueue({ state: 'done', items: (batchQueueState && batchQueueState.items) || [] }); toast('已停止（演示）', 'success'); return; }
    var api = bindings();
    if (!api || typeof api.StopBatch !== 'function') { toast('未检测到 App 绑定', 'error'); return; }
    Promise.resolve().then(function () { return api.StopBatch(); }).then(function (state) {
      renderBatchQueue(state);
      toast('已请求停止，当前账号结束后不再继续', 'success');
    }).catch(function (err) { toast('停止失败：' + errMsg(err), 'error'); });
  }

  function removeBatchAccount(id) {
    var account = null;
    batchAccounts.forEach(function (item) { if (item.id === id) account = item; });
    openConfirm({
      title: '删除账号？',
      message: '将删除「' + (account ? account.name : '该账号') + '」及其本机浏览器登录态，不可恢复。',
      confirmText: '删除',
      danger: true,
      onConfirm: function () {
        if (MOCK) {
          batchAccounts = batchAccounts.filter(function (item) { return item.id !== id; });
          renderBatchAccounts();
          toast('账号已删除（演示）', 'success');
          return;
        }
        var api = bindings();
        if (!api || typeof api.RemoveBatchAccount !== 'function') throw new Error('未检测到 App 绑定');
        return api.RemoveBatchAccount(id).then(function () {
          toast('账号已删除', 'success');
          return loadBatchAccounts();
        });
      }
    });
  }

  /* ───────────── 导航 ───────────── */
  function routeFromHash() {
    var raw = String(window.location.hash || '').replace(/^#\/?/, '');
    return isPage(raw) ? raw : 'overview';
  }
  function navigate(page) {
    if (!isPage(page)) page = 'overview';
    if (routeFromHash() === page) setPage(page);
    else window.location.hash = '#' + page;
  }
  function setPage(page, opts) {
    if (!isPage(page)) page = 'overview';
    currentPage = page;
    var meta = PAGE_META[page];
    document.title = meta.title + ' · 雨课助手';

    var actions = PAGE_ACTIONS[page] || [];
    ['btnScriptOpenYuketang', 'btnScriptViewLogs', 'aiDirtyChip', 'btnSaveAI', 'settingsDirtyChip', 'btnSaveSettings'].forEach(function (id) {
      setHidden('#' + id, actions.indexOf(id) < 0);
    });
    updateDirtyChips();

    var navPage = page === 'ai' ? 'settings' : page;
    $$('.nav-item').forEach(function (btn) {
      var active = btn.getAttribute('data-page') === navPage;
      btn.classList.toggle('is-active', active);
      btn.setAttribute('aria-selected', active ? 'true' : 'false');
      btn.tabIndex = active ? 0 : -1;
    });
    $$('.page').forEach(function (section) {
      var active = section.getAttribute('data-page') === page;
      section.hidden = !active;
      section.classList.toggle('page-enter', false);
      if (active && (!opts || opts.animate !== false)) {
        void section.offsetWidth;
        section.classList.add('page-enter');
      }
    });
    var scroll = $('#mainScroll');
    if (scroll) {
      scroll.classList.toggle('is-fixed', false);
      if (!opts || opts.keepScroll !== true) scroll.scrollTop = 0;
    }
    if (page === 'review') { if (state) loadLogs(); startLogPolling(); }
    if (page === 'script') { loadBatchAccounts(); loadBatchQueue(); }
    else stopLogPolling();
    if (page === 'ai' && !aiDirty) fillAIForm();
    if (page === 'settings' && !settingsDirty) fillSettingsForm();
    if (page === 'overview') loadRecentLogs('#overviewLogList', '#overviewLogEmpty', null, 5);
    if (page === 'settings' && !MOCK) {
      requireBindings().GetUpdateInfo().then(function (info) {
        if (info && (info.latestVersion || info.currentVersion)) {
          updateInfo = info;
          renderUpdate();
        }
      }).catch(function () { /* 忽略：点检查更新时会给出错误 */ });
    }
  }

  /* ───────────── 操作辅助 ───────────── */
  function runBtn(btn, task, label) {
    if (!btn || btn.classList.contains('is-busy')) return Promise.resolve();
    btn.classList.add('is-busy');
    return Promise.resolve().then(task).catch(function (err) {
      toast((label || '操作') + '失败：' + errMsg(err), 'error');
    }).then(function () {
      btn.classList.remove('is-busy');
      if (state) { renderSidebar(); renderToolbar(); renderOverview(); renderScriptPage(); }
    });
  }
  function mockStartService(port) {
    var pid = 40000 + Math.floor(Math.random() * 20000);
    mockState.service = { running: true, port: port, pid: pid, url: 'http://127.0.0.1:' + port + '/', uptime: 0, error: '' };
    mockState.syncUrl = 'http://127.0.0.1:' + port + '/api/config';
    mockLogs.push({ time: Date.now(), level: 'info', message: '本地服务已启动，监听 127.0.0.1:' + port });
  }
  function mockStopService() {
    mockState.service = { running: false, port: 0, pid: 0, url: '', uptime: 0, error: '' };
    mockState.syncUrl = '';
    mockLogs.push({ time: Date.now(), level: 'info', message: '本地服务已停止' });
  }
  function refreshMock() { applyState(normalizeState(mockState)); }

  function doStartService(btn) {
    return runBtn(btn, function () {
      if (MOCK) {
        if (mockState.service.running) return wait(180).then(function () { toast('本地服务已在运行', 'info'); });
        return wait(430).then(function () { mockStartService(mockState.settings.port); refreshMock(); toast('本地服务已启动（127.0.0.1:' + mockState.settings.port + '）', 'success'); });
      }
      return requireBindings().StartService().then(function (next) {
        applyState(next);
        var svc = normalizeState(next).service;
        toast(svc.running ? '本地服务已启动（127.0.0.1:' + svc.port + '）' : '本地服务启动失败', svc.running ? 'success' : 'error');
      });
    }, '启动服务');
  }
  function doStopService(btn) {
    return runBtn(btn, function () {
      if (MOCK) {
        if (!mockState.service.running) return wait(150).then(function () { toast('本地服务当前未运行', 'info'); });
        return wait(330).then(function () { mockStopService(); refreshMock(); toast('本地服务已停止', 'success'); });
      }
      return requireBindings().StopService().then(function (next) { applyState(next); toast('本地服务已停止', 'success'); });
    }, '停止服务');
  }
  function doRestartService(btn) {
    return runBtn(btn, function () {
      if (MOCK) {
        return wait(560).then(function () {
          mockStopService(); mockStartService(mockState.settings.port); refreshMock();
          toast('本地服务已重启（127.0.0.1:' + mockState.settings.port + '）', 'success');
        });
      }
      return requireBindings().RestartService().then(function (next) {
        applyState(next);
        var svc = normalizeState(next).service;
        toast(svc.running ? '本地服务已重启（127.0.0.1:' + svc.port + '）' : '重启失败，本地服务未运行', svc.running ? 'success' : 'error');
      });
    }, '重启服务');
  }
  function doInstallScript(btn) {
    return runBtn(btn, function () {
      if (MOCK) {
        return wait(480).then(function () {
          if (!mockState.service.running) mockStartService(mockState.settings.port);
          mockState.install = { installed: true, inSync: true, version: mockState.script.version, hash: mockState.script.hash, openedAt: Date.now(), note: '已安装，与当前内置脚本一致' };
          mockLogs.push({ time: Date.now(), level: 'info', message: '用户脚本安装请求：GET /ykt.user.js' });
          refreshMock();
          toast('已打开安装页面（脚本 v' + mockState.script.version + '）', 'success');
        });
      }
      return requireBindings().InstallScript().then(function (result) {
        var version = result && result.version ? result.version : state && state.script.version;
        toast('已打开安装页面' + (version ? '（脚本 v' + version + '）' : ''), 'success');
        return refreshState();
      });
    }, '安装脚本');
  }
  function doOpenYuketang(btn) {
    return runBtn(btn, function () {
      if (MOCK) {
        return wait(280).then(function () {
          if (!mockState.service.running) mockStartService(mockState.settings.port);
          refreshMock();
          toast('已在浏览器打开 ' + (mockState.settings.defaultUrl || 'https://yuketang.cn/'), 'success');
        });
      }
      return requireBindings().OpenYuketang().then(function () { toast('已在浏览器打开雨课堂', 'success'); return refreshState(); });
    }, '打开雨课堂');
  }

  /* ───────────── 剪贴板 / 路径 / 外链 ───────────── */
  function legacyCopy(value) {
    var area = document.createElement('textarea');
    area.value = value;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.top = '-1000px';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    var ok = false;
    try { ok = document.execCommand && document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(area);
    if (!ok) throw new Error('浏览器拒绝了剪贴板操作');
  }
  function mockCopy(value) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      return navigator.clipboard.writeText(value).catch(function () { legacyCopy(value); });
    }
    return Promise.resolve().then(function () { legacyCopy(value); });
  }
  function copyText(content, label) {
    if (!content) { toast('没有可复制的' + (label || '内容'), 'warning'); return Promise.resolve(); }
    var promise;
    if (MOCK) promise = mockCopy(content);
    else {
      promise = Promise.resolve().then(function () { return requireBindings().SetClipboard(content); }).catch(function (err) {
        if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
          return navigator.clipboard.writeText(content).catch(function () { throw err; });
        }
        throw err;
      });
    }
    return promise.then(function () { toast((label || '内容') + '已复制', 'success'); })
      .catch(function (err) { toast('复制' + (label || '内容') + '失败：' + errMsg(err), 'error'); });
  }
  function revealPath(which, label, btn) {
    return runBtn(btn, function () {
      if (MOCK) return wait(160).then(function () { toast('已在' + fileManagerName() + '中定位' + label + '（演示模式）', 'info'); });
      return requireBindings().RevealPath(which).then(function () { toast('已在' + fileManagerName() + '中定位' + label, 'success'); });
    }, '打开' + label);
  }
  function openExternal(url) {
    if (!url) return;
    if (MOCK) { toast('演示模式：将用默认浏览器打开 ' + url, 'info'); return; }
    Promise.resolve().then(function () { return requireBindings().OpenExternal(url); })
      .catch(function (err) { toast('打开链接失败：' + errMsg(err), 'error'); });
  }

  /* ───────────── 保存 AI / 设置 ───────────── */
  function collectAIConfig() {
    return {
      url: String(val('#aiUrl') || '').trim(),
      key: String(val('#aiKey') || ''),
      model: String(val('#aiModel') || '').trim(),
      apiFormat: val('#aiFormat') === 'anthropic' ? 'anthropic' : 'openai',
      authMethod: val('#aiAuth') === 'x-api-key' ? 'x-api-key' : 'bearer'
    };
  }
  function saveAIConfig(btn) {
    if (!state) return Promise.resolve();
    var config = collectAIConfig();
    var features = { autoAI: isOn('#switchAutoAI'), autoComment: isOn('#switchAutoComment') };
    if (!config.url) { toast('请填写 API 地址', 'error'); if ($('#aiUrl')) $('#aiUrl').focus(); return Promise.resolve(); }
    if (!/^https?:\/\//i.test(config.url)) { toast('API 地址需以 http:// 或 https:// 开头', 'error'); if ($('#aiUrl')) $('#aiUrl').focus(); return Promise.resolve(); }
    return runBtn(btn, function () {
      aiDirty = false; updateDirtyChips();
      if (MOCK) {
        return wait(380).then(function () {
          mockState.ai = config; mockState.features = features;
          mockLogs.push({ time: Date.now(), level: 'info', message: 'AI 配置已保存（' + (config.model || '未填写模型') + '）' });
          refreshMock(); fillAIForm(true); toast('AI 配置已保存', 'success');
        });
      }
      var api = requireBindings();
      return api.SaveAIConfig(config)
        .then(function (next) { return api.SaveFeatures(features).then(function (after) { return after || next; }); })
        .then(function (next) { applyState(next); fillAIForm(true); toast('AI 配置已保存', 'success'); });
    }, '保存 AI 配置');
  }
  function collectSettings() {
    var appearance = pendingAppearance || 'system';
    if (!pendingAppearance) {
      var active = $('.seg-btn.is-active');
      if (active) appearance = active.getAttribute('data-appearance') || 'system';
    }
    return {
      port: Number(val('#setPort')) || (state && state.settings.port) || 8899,
      defaultUrl: String(val('#setDefaultUrl') || '').trim(),
      autoStart: isOn('#switchAutoStart'),
      syncEnabled: isOn('#switchSyncEnabled'),
      appearance: appearance
    };
  }
  function saveSettings(btn) {
    if (!state) return Promise.resolve();
    var next = collectSettings();
    if (!next.defaultUrl) { toast('默认网址不能为空', 'error'); if ($('#setDefaultUrl')) $('#setDefaultUrl').focus(); return Promise.resolve(); }
    if (!/^https?:\/\//i.test(next.defaultUrl)) { toast('默认网址需以 http:// 或 https:// 开头', 'error'); if ($('#setDefaultUrl')) $('#setDefaultUrl').focus(); return Promise.resolve(); }
    var min = state.portRange.min || 8899;
    var max = state.portRange.max || 8910;
    if (!(next.port >= min && next.port <= max)) { toast('端口需在 ' + min + '–' + max + ' 之间', 'error'); return Promise.resolve(); }
    var portChanged = state.settings.port !== next.port;
    var wasRunning = state.service.running;
    return runBtn(btn, function () {
      settingsDirty = false; pendingAppearance = null; updateDirtyChips();
      if (MOCK) {
        return wait(420).then(function () {
          mockState.settings = next;
          if (portChanged && wasRunning) { mockState.service.running = false; mockStartService(next.port); }
          refreshMock(); fillSettingsForm(true);
          toast(portChanged && wasRunning ? '设置已保存，服务已在新端口重启' : '设置已保存', 'success');
        });
      }
      return requireBindings().SaveSettings(next).then(function (after) {
        applyState(after); fillSettingsForm(true);
        var svc = normalizeState(after).service;
        toast(portChanged && wasRunning && svc.running ? '设置已保存，服务已在新端口重启' : '设置已保存', 'success');
      });
    }, '保存设置');
  }

  /* ───────────── 刷新 / 轮询 ───────────── */
  function refreshState(opts) {
    if (MOCK) { applyState(normalizeState(mockState), opts); return Promise.resolve(true); }
    return Promise.resolve().then(function () { return requireBindings().GetState(); })
      .then(function (next) { applyState(next, opts); return true; })
      .catch(function (err) {
        if (!booted) showEnvironmentError(errMsg(err));
        else if (!opts || opts.silent !== true) toast('读取 App 状态失败：' + errMsg(err), 'error');
        return false;
      });
  }
  function scheduleRefresh() {
    if (refreshPending) return;
    refreshPending = true;
    window.setTimeout(function () {
      refreshPending = false;
      refreshState({ silent: true }).then(function (ok) { if (ok && currentPage === 'script') loadLogs(); });
    }, 180);
  }
  function startPolling() {
    if (MOCK || pollTimer) return;
    pollTimer = window.setInterval(function () {
      if (document.hidden) return;
      refreshState({ silent: true });
    }, 3000);
  }
  function showEnvironmentError(message) {
    hideSkeleton();
    text('#serviceRowDesc', message + '，浏览器预览请使用 ?mock=1');
    setBadge('#serviceBadge', '#serviceBadgeText', 'danger', '未连接');

    toast('未检测到 App 绑定，请通过桌面 App 打开', 'error');
  }

  /* ───────────── 事件 ───────────── */
  function bindSwitch(selector, onChange) {
    var el = $(selector);
    if (!el) return;
    el.addEventListener('click', function () {
      var next = el.getAttribute('aria-checked') !== 'true';
      el.setAttribute('aria-checked', next ? 'true' : 'false');
      if (typeof onChange === 'function') onChange(next);
    });
  }
  function bindEvents() {
    $$('.nav-item').forEach(function (btn) {
      btn.addEventListener('click', function () { navigate(btn.getAttribute('data-page')); });
    });
    var nav = $('.nav');
    if (nav) {
      nav.addEventListener('keydown', function (event) {
        if (['ArrowDown', 'ArrowUp', 'Home', 'End'].indexOf(event.key) < 0) return;
        var items = $$('.nav-item');
        var index = items.indexOf(document.activeElement);
        if (index < 0) return;
        event.preventDefault();
        var target = index;
        if (event.key === 'ArrowDown') target = (index + 1) % items.length;
        if (event.key === 'ArrowUp') target = (index - 1 + items.length) % items.length;
        if (event.key === 'Home') target = 0;
        if (event.key === 'End') target = items.length - 1;
        items[target].focus();
        navigate(items[target].getAttribute('data-page'));
      });
    }
    window.addEventListener('hashchange', function () {
      var page = routeFromHash();
      if (page !== currentPage) setPage(page);
    });
    document.addEventListener('keydown', function (event) {
      if (onboardingVisible()) {
        if (event.key === 'Escape') { event.preventDefault(); onboardingClose(true); return; }
        if (event.key === 'ArrowRight' || event.key === 'Enter') { event.preventDefault(); onboardingNext(); return; }
        if (event.key === 'ArrowLeft') { event.preventDefault(); onboardingBack(); return; }
      }
      if (event.key === 'Escape') {
        if (modal.open) { event.preventDefault(); closeModal(); }
        return;
      }
      trapModalFocus(event);
      var mod = event.metaKey || event.ctrlKey;
      if (!mod || event.altKey || event.shiftKey) return;
      var index = ['1', '2', '3', '4'].indexOf(event.key);
      if (index >= 0) { event.preventDefault(); navigate(PAGES[index]); }
    });

    var cancel = $('#modalCancel');
    if (cancel) cancel.addEventListener('click', function () { closeModal(); });
    var confirm = $('#modalConfirm');
    if (confirm) confirm.addEventListener('click', confirmModal);
    var backdrop = $('#modalBackdrop');
    if (backdrop) backdrop.addEventListener('click', function () { closeModal(); });

    /* 概览 */
    var btnStart = $('#btnStartService');
    if (btnStart) btnStart.addEventListener('click', function () { doStartService(btnStart); });
    var btnStop = $('#btnStopService');
    if (btnStop) btnStop.addEventListener('click', function () { doStopService(btnStop); });
    var btnRestart = $('#btnRestartService');
    if (btnRestart) btnRestart.addEventListener('click', function () { doRestartService(btnRestart); });
    var btnInstall = $('#btnInstallScript');
    if (btnInstall) btnInstall.addEventListener('click', function () { doInstallScript(btnInstall); });
    var btnOpen = $('#btnOpenYuketang');
    if (btnOpen) btnOpen.addEventListener('click', function () { doOpenYuketang(btnOpen); });
    var btnRefreshState = $('#btnRefreshState');
    if (btnRefreshState) {
      btnRefreshState.addEventListener('click', function () {
        runBtn(btnRefreshState, function () {
          if (MOCK) return wait(180).then(function () { refreshMock(); if (currentPage === 'review') loadLogs(); toast('状态已刷新', 'success'); });
          return refreshState({ silent: true }).then(function (ok) {
            if (!ok) throw new Error('读取状态失败');
            if (currentPage === 'review') loadLogs();
            toast('状态已刷新', 'success');
          });
        }, '刷新状态');
      });
    }

    /* 脚本页 */
    var goRun = $('#btnGoRun');
    if (goRun) goRun.addEventListener('click', function () { navigate('script'); });
    var viewLogs = $('#btnScriptViewLogs');
    if (viewLogs) viewLogs.addEventListener('click', function () { navigate('review'); });
    var batchList = $('#batchAccountList');
    if (batchList) {
      batchList.addEventListener('click', function (event) {
        var trigger = event.target && event.target.closest ? event.target.closest('[data-batch-action]') : null;
        if (!trigger) return;
        var action = trigger.getAttribute('data-batch-action');
        var id = trigger.getAttribute('data-batch-id');
        if (action === 'add') showBatchAddForm(true);
        else if (action === 'login') startBatchLogin(id);
        else if (action === 'remove') removeBatchAccount(id);
      });
    }
    var addSave = $('#btnBatchAddSave');
    if (addSave) addSave.addEventListener('click', submitBatchAccount);
    var addCancel = $('#btnBatchAddCancel');
    if (addCancel) addCancel.addEventListener('click', function () { showBatchAddForm(false); });
    var addInput = $('#batchAccountName');
    if (addInput) addInput.addEventListener('keydown', function (event) { if (event.key === 'Enter') { event.preventDefault(); submitBatchAccount(); } });
    var loginDone = $('#btnBatchLoginDone');
    if (loginDone) loginDone.addEventListener('click', completeBatchLogin);
    var loginCancel = $('#btnBatchLoginCancel');
    if (loginCancel) loginCancel.addEventListener('click', cancelBatchLogin);
    var startBatchBtn = $('#btnBatchStart');
    if (startBatchBtn) startBatchBtn.addEventListener('click', startBatch);
    var stopBatchBtn = $('#btnBatchStop');
    if (stopBatchBtn) stopBatchBtn.addEventListener('click', stopBatch);
    var goAI = $('#btnGoAI');
    if (goAI) goAI.addEventListener('click', function () { navigate('ai'); });
    var goOpen = $('#btnGoOpenYuketang');
    if (goOpen) goOpen.addEventListener('click', function () { doOpenYuketang(goOpen); });
    var runOpen = $('#btnRunOpenYuketang');
    if (runOpen) runOpen.addEventListener('click', function () { doOpenYuketang(runOpen); });
    var goAIConfig = $('#btnGoAIConfig');
    if (goAIConfig) goAIConfig.addEventListener('click', function () { navigate('ai'); });
    var openAIConfig = $('#btnOpenAIConfig');
    if (openAIConfig) openAIConfig.addEventListener('click', function () { navigate('ai'); });
    var aiBack = $('#btnAIBack');
    if (aiBack) aiBack.addEventListener('click', function () { navigate('settings'); });
    var showOnboardingFromRun = $('#btnShowOnboardingFromRun');
    if (showOnboardingFromRun) showOnboardingFromRun.addEventListener('click', onboardingOpen);
    var btnScriptOpen = $('#btnScriptOpenYuketang');
    if (btnScriptOpen) btnScriptOpen.addEventListener('click', function () { doOpenYuketang(btnScriptOpen); });
    var btnInstallFromScript = $('#btnInstallFromScript');
    if (btnInstallFromScript) btnInstallFromScript.addEventListener('click', function () { doInstallScript(btnInstallFromScript); });
    var btnCopyHash = $('#btnCopyHash');
    if (btnCopyHash) btnCopyHash.addEventListener('click', function () { copyText(state && state.script.hash ? state.script.hash : '', '脚本指纹'); });
    var btnCopyUrl = $('#btnCopyInstallUrl');
    if (btnCopyUrl) btnCopyUrl.addEventListener('click', function () { copyText(installURL(), '安装地址'); });

    /* AI */
    var presetEl = $('#aiPreset');
    if (presetEl) {
      presetEl.addEventListener('change', function () {
        var preset = PRESETS[val('#aiPreset')] || PRESETS.custom;
        if (preset.url) {
          if ($('#aiUrl')) $('#aiUrl').value = preset.url;
          if ($('#aiModel')) $('#aiModel').value = preset.model;
          if ($('#aiFormat')) $('#aiFormat').value = preset.apiFormat;
          if ($('#aiAuth')) $('#aiAuth').value = preset.authMethod;
        }
        updateProviderDesc();
        markAIDirty();
      });
    }
    ['#aiUrl', '#aiModel', '#aiKey'].forEach(function (sel) {
      var el = $(sel);
      if (el) el.addEventListener('input', markAIDirty);
    });
    ['#aiFormat', '#aiAuth'].forEach(function (sel) {
      var el = $(sel);
      if (el) el.addEventListener('change', markAIDirty);
    });
    var toggleKey = $('#btnToggleKey');
    if (toggleKey) toggleKey.addEventListener('click', function () { markKeyVisibility(toggleKey.getAttribute('aria-pressed') !== 'true'); });
    bindSwitch('#switchAutoAI', markAIDirty);
    bindSwitch('#switchAutoComment', markAIDirty);
    var saveAI = $('#btnSaveAI');
    if (saveAI) saveAI.addEventListener('click', function () { saveAIConfig(saveAI); });
    var aiForm = $('#aiForm');
    if (aiForm) aiForm.addEventListener('submit', function (event) { event.preventDefault(); saveAIConfig($('#btnSaveAI')); });

    /* 日志 */
    var autoScrollBtn = $('#switchAutoScroll');
    if (autoScrollBtn) {
      autoScrollBtn.addEventListener('click', function () {
        autoScroll = autoScrollBtn.getAttribute('aria-checked') !== 'true';
        autoScrollBtn.setAttribute('aria-checked', autoScroll ? 'true' : 'false');
        if (autoScroll) { var list = $('#logList'); if (list) list.scrollTop = list.scrollHeight; }
      });
    }
    var retryLogs = $('#btnRetryLogs');
    if (retryLogs) retryLogs.addEventListener('click', function () { loadLogs(); });
    var revealLog = $('#btnRevealLog');
    if (revealLog) revealLog.addEventListener('click', function () { revealPath('log', '日志文件', revealLog); });
    var clearLogs = $('#btnClearLogs');
    if (clearLogs) {
      clearLogs.addEventListener('click', function () {
        openConfirm({
          title: '清空运行日志？',
          message: '将删除本机日志文件中的全部记录，此操作不可撤销。',
          confirmText: '清空',
          danger: true,
          onConfirm: function () {
            if (MOCK) return wait(150).then(function () { mockLogs = []; logs = []; logState = 'ready'; renderLogs(); toast('日志已清空', 'success'); });
            return requireBindings().ClearLogs().then(function () {
              logs = []; logState = 'ready'; renderLogs(); toast('日志已清空', 'success');
            }).catch(function (err) { toast('清空日志失败：' + errMsg(err), 'error'); loadLogs(); });
          }
        });
      });
    }

    /* 设置 */
    if ($('#setPort')) $('#setPort').addEventListener('change', markSettingsDirty);
    if ($('#setDefaultUrl')) $('#setDefaultUrl').addEventListener('input', markSettingsDirty);
    bindSwitch('#switchAutoStart', markSettingsDirty);
    bindSwitch('#switchSyncEnabled', markSettingsDirty);
    $$('.seg-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        pendingAppearance = btn.getAttribute('data-appearance') || 'system';
        applyTheme(pendingAppearance);
        markSettingsDirty();
      });
    });
    var saveSettingsBtn = $('#btnSaveSettings');
    if (saveSettingsBtn) saveSettingsBtn.addEventListener('click', function () { saveSettings(saveSettingsBtn); });
    var revealConfig = $('#btnRevealConfig');
    if (revealConfig) revealConfig.addEventListener('click', function () { revealPath('home', '配置目录', revealConfig); });
    var revealLogPath = $('#btnRevealLogPath');
    if (revealLogPath) revealLogPath.addEventListener('click', function () { revealPath('log', '日志文件', revealLogPath); });
    var checkUpdateBtn = $('#btnCheckUpdate');
    if (checkUpdateBtn) checkUpdateBtn.addEventListener('click', checkUpdates);
    var installUpdateBtn = $('#btnInstallUpdate');
    if (installUpdateBtn) installUpdateBtn.addEventListener('click', installUpdate);
    var showOnboardingBtn = $('#btnShowOnboarding');
    if (showOnboardingBtn) showOnboardingBtn.addEventListener('click', onboardingOpen);
    var onboardingNextBtn = $('#btnOnboardingNext');
    if (onboardingNextBtn) onboardingNextBtn.addEventListener('click', onboardingNext);
    var onboardingBackBtn = $('#btnOnboardingBack');
    if (onboardingBackBtn) onboardingBackBtn.addEventListener('click', onboardingBack);
    var onboardingSkipBtn = $('#btnOnboardingSkip');
    if (onboardingSkipBtn) onboardingSkipBtn.addEventListener('click', function () { onboardingClose(true); });
    var onboardingBackdrop = $('#onboardingBackdrop');
    if (onboardingBackdrop) onboardingBackdrop.addEventListener('click', function () { onboardingClose(true); });
    $$('[data-external]').forEach(function (btn) {
      btn.addEventListener('click', function () { openExternal(btn.getAttribute('data-external')); });
    });

    document.addEventListener('visibilitychange', function () {
      if (document.hidden || MOCK) return;
      refreshState({ silent: true });
      if (currentPage === 'review') loadLogs();
    });
  }

  /* ───────────── 启动 ───────────── */
  /* 仅 mock 预览：?demo=switch|save|copy|clear|stopped（用于交互截图） */
  function runDemo(kind) {
    if (!kind || !MOCK) return;
    window.setTimeout(function () {
      switch (kind) {
        case 'copy': toast('安装地址已复制', 'success'); break;
        case 'switch':
          navigate('ai');
          var sw = $('#switchAutoComment');
          if (sw) sw.setAttribute('aria-checked', 'true');
          markAIDirty();
          toast('有未保存的更改', 'info');
          break;
        case 'save': navigate('ai'); toast('AI 配置已保存', 'success'); break;
        case 'clear':
          navigate('script');
          openConfirm({ title: '清空运行日志？', message: '将删除本机日志文件中的全部记录，此操作不可撤销。', confirmText: '清空', danger: true, onConfirm: function () { toast('日志已清空', 'success'); } });
          break;
        case 'stopped': mockStopService(); refreshMock(); break;
        default: break;
      }
    }, 800);
  }

  function init() {
    if (MOCK) document.documentElement.classList.add('mock');
    if (forcedTheme()) document.documentElement.setAttribute('data-theme', forcedTheme());
    if (new URLSearchParams(window.location.search).get('nomotion') === '1') document.documentElement.classList.add('no-motion');
    bindEvents();
    updateKbdHints();
    setPage(routeFromHash(), { silent: true, animate: false });

    if (MOCK) {
      window.setTimeout(function () {
        applyState(normalizeState(mockState));
        if (currentPage === 'review') loadLogs();
        runDemo(new URLSearchParams(window.location.search).get('demo'));
      }, 420);
    } else {
      refreshState({ skeleton: true }).then(function (ok) {
        if (!ok) return;
        var rt = runtime();
        if (rt) {
          try { rt.EventsOn('state:changed', scheduleRefresh); } catch (e) { /* 轮询兜底 */ }
          try { rt.EventsOn('batch:changed', function () { if (currentPage === 'script') loadBatchAccounts(); }); } catch (e) { /* 忽略 */ }
          try { rt.EventsOn('batch:state', function (state) { if (currentPage === 'script') { renderBatchQueue(state); loadBatchAccounts(); } }); } catch (e) { /* 忽略 */ }
          try {
            rt.EventsOn('app:notice', function (message) {
              var content = typeof message === 'string' ? message : '';
              if (!content) return;
              toast(content, /失败|错误|异常|✖/.test(content) ? 'error' : 'warning');
            });
          } catch (e) { /* 轮询兜底 */ }
        }
        startPolling();
        if (currentPage === 'review') loadLogs();
      });
    }

    window.setInterval(function () {
      if (!state || document.hidden) return;
      updateLiveText();
      renderToolbar();
    }, 1000);
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
