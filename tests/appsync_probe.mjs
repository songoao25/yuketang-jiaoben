#!/usr/bin/env node
/**
 * yuketang.js 的 AppSync 逻辑隔离探针（QA 用，不修改源脚本）
 *
 * 做法：从 yuketang.js 中按注释标记原样抽取 `const AppSync = {...}` 代码块，
 * 在 Node 里用最小依赖（Config / Store / localStorage / panel / GM_xmlhttpRequest mock）
 * 执行，验证端口竞速、端口缓存、hasAI/hasFeatures 分段覆盖、旧服务兼容、
 * configured / sync.enabled 分支、配置合并与异常响应处理。
 *
 * 第二轮（2026-11-04）：M3 修复后，服务端返回 hasAI/hasFeatures，脚本按段覆盖；
 * 旧服务响应没有这两个字段时回退 configured 旧语义。以下用例同时锁定新语义与向后兼容。
 *
 * 用法：node tests/appsync_probe.mjs
 * 输出 PASS/FAIL 统计；FAIL > 0 时非零退出。
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SOURCE_PATH = path.join(ROOT, 'yuketang.js');
const source = fs.readFileSync(SOURCE_PATH, 'utf8');

// ---- 抽取 AppSync 代码块（依赖两个分隔注释，标记不存在时明确失败） ----
const START_MARK = '// ---- 本机管理台配置同步 ----';
const END_MARK = '// ---- UI 面板 ----';
const startIndex = source.indexOf(START_MARK);
const endIndex = source.indexOf(END_MARK);
if (startIndex < 0 || endIndex <= startIndex) {
  console.error('FAIL  无法在 yuketang.js 中定位 AppSync 代码块（分隔注释已变化）');
  process.exit(1);
}
const appSyncStart = source.indexOf('const AppSync', startIndex);
if (appSyncStart < 0 || appSyncStart > endIndex) {
  console.error('FAIL  无法在同步区块中找到 const AppSync');
  process.exit(1);
}
const SNIPPET = source.slice(appSyncStart, endIndex);

let PASS = 0;
let FAIL = 0;
const ok = (name) => { PASS += 1; console.log(`PASS  ${name}`); };
const bad = (name, detail) => { FAIL += 1; console.log(`FAIL  ${name}${detail ? `（${detail}）` : ''}`); };

const AI_KEY = 'ykt_ai_conf';
const FEATURE_KEY = 'ykt_feature_conf';
const PORT_KEY = 'ykt_app_port';

// yuketang.js 中 Store 相关的最小镜像（与源脚本第 200~235 行行为一致）
function createStore(Config, localStorage) {
  const safeJSONParse = (value, fallback) => {
    try { return JSON.parse(value); } catch (_) { return fallback; }
  };
  return {
    getAIConf() {
      const saved = safeJSONParse(localStorage.getItem(Config.storageKeys.ai), {}) || {};
      const conf = {
        url: saved.url ?? 'https://api.deepseek.com/chat/completions',
        key: saved.key ?? 'sk-xxxxxxx',
        model: saved.model ?? 'deepseek-chat',
        apiFormat: saved.apiFormat ?? 'openai',
        authMethod: saved.authMethod ?? 'bearer',
      };
      localStorage.setItem(Config.storageKeys.ai, JSON.stringify(conf));
      return conf;
    },
    setAIConf(conf) { localStorage.setItem(Config.storageKeys.ai, JSON.stringify(conf)); },
    getFeatureConf() {
      const saved = safeJSONParse(localStorage.getItem(Config.storageKeys.feature), {}) || {};
      const conf = { autoAI: saved.autoAI ?? false, autoComment: saved.autoComment ?? false };
      localStorage.setItem(Config.storageKeys.feature, JSON.stringify(conf));
      return conf;
    },
    setFeatureConf(conf) { localStorage.setItem(Config.storageKeys.feature, JSON.stringify(conf)); },
  };
}

// 新服务端响应（hasAI/hasFeatures 由调用方覆盖）
function payload(overrides = {}) {
  return {
    ok: true,
    app: 'ykt',
    version: '1.0.0',
    sync: { enabled: true },
    configured: true,
    hasAI: true,
    hasFeatures: true,
    ai: {
      url: 'https://api.deepseek.com/chat/completions',
      key: 'sk-from-app',
      model: 'deepseek-chat',
      apiFormat: 'openai',
      authMethod: 'bearer',
    },
    features: { autoAI: true, autoComment: false },
    ...overrides,
  };
}

// 旧服务端响应：没有 hasAI / hasFeatures 字段（v3.0.7 之前的本地服务）
function legacyPayload(overrides = {}) {
  const data = payload(overrides);
  delete data.hasAI;
  delete data.hasFeatures;
  return data;
}

/**
 * 创建隔离运行环境。
 * respond(port) 返回：{status, body, delay} | 'error' | 'hang'
 */
function createHarness({ respond, ports = [8899, 8900], seed = {} }) {
  const storage = new Map(Object.entries(seed));
  const Config = {
    appSync: { ports: ports.slice(), timeout: 50 },
    storageKeys: { ai: AI_KEY, feature: FEATURE_KEY, appPort: PORT_KEY },
  };
  const localStorage = {
    getItem: (key) => (storage.has(key) ? storage.get(key) : null),
    setItem: (key, value) => storage.set(key, String(value)),
    removeItem: (key) => storage.delete(key),
  };
  const Store = createStore(Config, localStorage);
  const requests = [];
  const panel = {
    lines: [],
    refreshCount: 0,
    log(message) { this.lines.push(message); },
    refreshSettings() { this.refreshCount += 1; },
  };

  let gm;
  if (respond) {
    gm = (options) => {
      const port = Number(new URL(options.url).port);
      requests.push(port);
      const result = respond(port);
      if (result === 'error') {
        setTimeout(() => options.onerror && options.onerror(), 1);
        return;
      }
      if (result === 'hang') return;
      setTimeout(() => {
        if (result.status !== 200) {
          options.onload && options.onload({ status: result.status, responseText: '' });
          return;
        }
        options.onload && options.onload({
          status: 200,
          responseText: typeof result.body === 'string' ? result.body : JSON.stringify(result.body),
        });
      }, result.delay || 0);
    };
  }

  // eslint-disable-next-line no-new-func
  const factory = new Function(
    'Config', 'Store', 'panel', 'localStorage', 'GM_xmlhttpRequest',
    `${SNIPPET}\nreturn AppSync;`,
  );
  const AppSync = factory(Config, Store, panel, localStorage, gm);
  return { AppSync, storage, panel, localStorage, requests };
}

const readStored = (storage, key) => {
  const raw = storage.get(key);
  return raw ? JSON.parse(raw) : null;
};
const hasKey = (storage, key) => storage.has(key);

const PANEL_SEED = {
  [AI_KEY]: JSON.stringify({ url: 'https://panel.example/v1', key: 'sk-panel', model: 'panel-model', apiFormat: 'openai', authMethod: 'bearer' }),
  [FEATURE_KEY]: JSON.stringify({ autoAI: true, autoComment: true }),
};

const tests = [];
const test = (name, fn) => tests.push({ name, fn });

// ---------------------------------------------------------------- 基础降级
test('缺少 GM_xmlhttpRequest 时静默回退（不抛异常、不写 localStorage）', async () => {
  const h = createHarness({ respond: undefined });
  const applied = await h.AppSync.sync();
  if (applied !== false) throw new Error(`sync() 应为 false，实际 ${applied}`);
  if (h.panel.lines.length !== 0) throw new Error('无 GM 时不应输出面板日志');
  if (h.storage.size !== 0) throw new Error('无 GM 时不应改 localStorage');
});

test('12 个端口全部失败时返回 false 且 400ms 内收敛', async () => {
  const h = createHarness({
    respond: () => 'error',
    ports: [8899, 8900, 8901, 8902, 8903, 8904, 8905, 8906, 8907, 8908, 8909, 8910],
  });
  const started = Date.now();
  const applied = await h.AppSync.sync();
  const elapsed = Date.now() - started;
  if (applied !== false) throw new Error(`sync() 应为 false，实际 ${applied}`);
  if (elapsed > 500) throw new Error(`收敛耗时 ${elapsed}ms 超过 500ms`);
});

test('服务未启动（连接错误）时回退，不改 localStorage', async () => {
  const h = createHarness({ respond: () => 'error', seed: PANEL_SEED });
  await h.AppSync.sync();
  const ai = readStored(h.storage, AI_KEY);
  const feature = readStored(h.storage, FEATURE_KEY);
  if (ai.key !== 'sk-panel' || feature.autoAI !== true) {
    throw new Error('连接失败时不应改动网页面板配置');
  }
  if (hasKey(h.storage, PORT_KEY)) throw new Error('连接失败时不应缓存端口');
});

// ---------------------------------------------------------------- 同步总开关 / configured
test('sync.enabled=false 时保留网页面板设置，不写端口缓存', async () => {
  const h = createHarness({
    respond: () => ({ status: 200, body: payload({ sync: { enabled: false }, ai: {}, features: { autoAI: false, autoComment: false } }) }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== false) throw new Error('同步关闭时 sync() 应为 false');
  const ai = readStored(h.storage, AI_KEY);
  const feature = readStored(h.storage, FEATURE_KEY);
  if (ai.key !== 'sk-panel' || feature.autoAI !== true) throw new Error('同步关闭时不应覆盖网页面板配置');
  if (!h.panel.lines.some((line) => line.includes('同步已关闭'))) throw new Error('缺少「同步已关闭」面板日志');
});

test('configured=false（新版响应）时保留网页面板设置', async () => {
  const h = createHarness({
    respond: () => ({ status: 200, body: payload({ configured: false, hasAI: false, hasFeatures: false, ai: {}, features: { autoAI: false, autoComment: false } }) }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== false) throw new Error('configured=false 时 sync() 应为 false');
  const ai = readStored(h.storage, AI_KEY);
  if (ai.key !== 'sk-panel') throw new Error('configured=false 时不应覆盖网页面板 AI 配置');
  if (!h.panel.lines.some((line) => line.includes('尚未保存配置'))) throw new Error('缺少「尚未保存配置」面板日志');
});

// ---------------------------------------------------------------- M3：按段覆盖
test('仅保存 AI 段（hasAI=true hasFeatures=false）：只覆盖 AI，保留网页功能开关', async () => {
  const h = createHarness({
    respond: () => ({
      status: 200,
      body: payload({ configured: true, hasAI: true, hasFeatures: false, features: { autoAI: false, autoComment: false } }),
    }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('sync() 应为 true');
  const ai = readStored(h.storage, AI_KEY);
  const feature = readStored(h.storage, FEATURE_KEY);
  if (ai.key !== 'sk-from-app') throw new Error(`AI 段未同步：${ai.key}`);
  if (feature.autoAI !== true || feature.autoComment !== true) {
    throw new Error(`features 段缺失时不应覆盖网页开关，实际 ${JSON.stringify(feature)}`);
  }
  const log = h.panel.lines.find((line) => line.includes('已从本机 App 同步配置'));
  if (!log || !/：AI（端口 \d+）/.test(log)) throw new Error(`同步日志不符合预期：${JSON.stringify(h.panel.lines)}`);
  if (h.panel.refreshCount !== 1) throw new Error(`apply 后应刷新设置面板一次，实际 ${h.panel.refreshCount}`);
});

test('仅保存 features 段（hasAI=false hasFeatures=true）：只覆盖开关，保留网页 AI 配置', async () => {
  const h = createHarness({
    respond: () => ({
      status: 200,
      body: payload({ configured: true, hasAI: false, hasFeatures: true, ai: {}, features: { autoAI: false, autoComment: true } }),
    }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('sync() 应为 true');
  const ai = readStored(h.storage, AI_KEY);
  const feature = readStored(h.storage, FEATURE_KEY);
  if (ai.key !== 'sk-panel' || ai.url !== 'https://panel.example/v1') {
    throw new Error(`AI 段缺失时不应覆盖网页 AI 配置，实际 ${JSON.stringify(ai)}`);
  }
  if (feature.autoAI !== false || feature.autoComment !== true) {
    throw new Error(`features 段未同步：${JSON.stringify(feature)}`);
  }
  const log = h.panel.lines.find((line) => line.includes('已从本机 App 同步配置'));
  if (!log || !/：功能开关（端口 \d+）/.test(log)) throw new Error(`同步日志不符合预期：${JSON.stringify(h.panel.lines)}`);
});

test('两段都有（hasAI/hasFeatures 均 true）：AI 与功能开关同时覆盖', async () => {
  const h = createHarness({ respond: () => ({ status: 200, body: payload() }), seed: PANEL_SEED });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('sync() 应为 true');
  if (readStored(h.storage, AI_KEY).key !== 'sk-from-app') throw new Error('AI 段未同步');
  if (readStored(h.storage, FEATURE_KEY).autoAI !== true) throw new Error('features 段未同步');
  const log = h.panel.lines.find((line) => line.includes('已从本机 App 同步配置'));
  if (!log || !/：AI与功能开关（端口 \d+）/.test(log)) throw new Error(`同步日志不符合预期：${JSON.stringify(h.panel.lines)}`);
  if (h.panel.refreshCount !== 1) throw new Error('apply 后应刷新设置面板一次');
});

test('App 的 AI 字段为空时按字段回退到网页面板值（hasAI=true）', async () => {
  const h = createHarness({
    respond: () => ({
      status: 200,
      body: payload({
        hasAI: true,
        hasFeatures: false,
        ai: { url: '', key: '', model: '', apiFormat: '', authMethod: '' },
      }),
    }),
    seed: PANEL_SEED,
  });
  await h.AppSync.sync();
  const ai = readStored(h.storage, AI_KEY);
  if (ai.key !== 'sk-panel' || ai.url !== 'https://panel.example/v1' || ai.model !== 'panel-model') {
    throw new Error(`空字段应回退到面板值：${JSON.stringify(ai)}`);
  }
});

// ---------------------------------------------------------------- 向后兼容：旧服务响应没有 hasAI/hasFeatures
test('旧服务响应（无 hasAI/hasFeatures，configured=true）按旧语义同时覆盖两段', async () => {
  const h = createHarness({
    respond: () => ({
      status: 200,
      body: legacyPayload({ configured: true, features: { autoAI: false, autoComment: false } }),
    }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('旧服务 configured=true 时 sync() 应为 true');
  if (readStored(h.storage, AI_KEY).key !== 'sk-from-app') throw new Error('旧语义应覆盖 AI 段');
  const feature = readStored(h.storage, FEATURE_KEY);
  if (feature.autoAI !== false || feature.autoComment !== false) {
    throw new Error(`旧语义应同时覆盖 features 段，实际 ${JSON.stringify(feature)}`);
  }
  const log = h.panel.lines.find((line) => line.includes('已从本机 App 同步配置'));
  if (!log || !/：AI与功能开关（端口 \d+）/.test(log)) throw new Error(`旧语义同步日志不符：${JSON.stringify(h.panel.lines)}`);
});

test('旧服务响应（无 hasAI/hasFeatures，configured=false）保持网页面板设置', async () => {
  const h = createHarness({
    respond: () => ({
      status: 200,
      body: legacyPayload({ configured: false, ai: {}, features: { autoAI: false, autoComment: false } }),
    }),
    seed: PANEL_SEED,
  });
  const applied = await h.AppSync.sync();
  if (applied !== false) throw new Error('旧服务 configured=false 时 sync() 应为 false');
  if (readStored(h.storage, AI_KEY).key !== 'sk-panel') throw new Error('configured=false 不应覆盖 AI');
  if (readStored(h.storage, FEATURE_KEY).autoAI !== true) throw new Error('configured=false 不应覆盖 features');
});

// ---------------------------------------------------------------- 响应校验 / 竞速 / 端口缓存
test('非 ykt 服务 / 非 200 / 坏 JSON / 500 都被忽略', async () => {
  const cases = [
    { status: 200, body: payload({ app: 'other' }) },
    { status: 403, body: '' },
    { status: 200, body: '{bad json' },
    { status: 500, body: payload() },
  ];
  for (const response of cases) {
    const h = createHarness({ respond: () => response, seed: PANEL_SEED });
    const applied = await h.AppSync.sync();
    if (applied !== false) throw new Error(`异常响应应被忽略：${JSON.stringify(response).slice(0, 80)}`);
    if (readStored(h.storage, AI_KEY).key !== 'sk-panel') throw new Error('异常响应不应改动面板配置');
  }
});

test('多端口同时响应时采用最先返回的成功结果（竞速生效）并缓存该端口', async () => {
  const h = createHarness({
    ports: [8899, 8900],
    respond: (port) => ({ status: 200, body: payload(), delay: port === 8899 ? 40 : 2 }),
  });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('sync() 应为 true');
  const log = h.panel.lines.find((line) => line.includes('已从本机 App 同步配置'));
  if (!log || !log.includes('端口 8900')) throw new Error(`应选中最先返回的 8900 端口：${JSON.stringify(h.panel.lines)}`);
  if (h.storage.get(PORT_KEY) !== '8900') throw new Error(`应缓存端口 8900，实际 ${h.storage.get(PORT_KEY)}`);
});

test('缓存端口生效：下一次同步优先探测上次成功端口', async () => {
  const h = createHarness({
    ports: [8899, 8900, 8901],
    seed: { [PORT_KEY]: '8901' },
    respond: () => ({ status: 200, body: payload() }),
  });
  h.requests.length = 0;
  await h.AppSync.sync();
  if (h.requests[0] !== 8901) throw new Error(`首个探测端口应为缓存端口 8901，实际顺序 ${h.requests.join(',')}`);
});

test('app 字段固定为 ykt 时才接受（防止误连其他本地服务）', async () => {
  const h = createHarness({ respond: () => ({ status: 200, body: payload({ app: 'ykt' }) }) });
  const applied = await h.AppSync.sync();
  if (applied !== true) throw new Error('app=ykt 应被接受');
});

// ---------------------------------------------------------------- 运行
console.log('yuketang.js AppSync 隔离探针');
console.log(`源文件：${SOURCE_PATH}`);
console.log(`抽取代码长度：${SNIPPET.length} 字节\n`);

for (const { name, fn } of tests) {
  try {
    await fn();
    ok(name);
  } catch (err) {
    bad(name, err && err.message ? err.message : String(err));
  }
}

console.log(`\n结果：PASS=${PASS}  FAIL=${FAIL}`);
process.exit(FAIL > 0 ? 1 : 0);
