/* Porch 门廊 · 前端逻辑：登录、监控看板、模块 CRUD、日志流。
   原生 JS + Canvas，无任何外部依赖（离线可用，随二进制内嵌）。
   交互原则：任何一次点击都要有即时反馈（进度条 / 数字滚动 / 轻提示），
   数据刷新用差异更新，避免整页重绘带来的闪烁。 */

const TOKEN_KEY = 'porch_token';
// 兼容旧版本（Lucky 时期）留下的登录态，避免升级后要求重新登录
let TOKEN = localStorage.getItem(TOKEN_KEY) || localStorage.getItem('lucky_token') || '';
// 演示模式：在地址后加 ?demo=1 可用示例数据预览界面（不需要后端）
let DEMO = location.search.indexOf('demo') >= 0 || location.pathname.indexOf('preview') >= 0;
let STATE = { base: {}, ddns: [], cert: [], proxy: [], system: {}, meta: null, version: '', metrics: null };
let PAGE = 'dashboard';
let logSince = 0;
let logStream = null;
let metricsTimer = null;

// 局部列表的渲染缓存（用于差异更新，避免每 2 秒重建 DOM）
let lastKVHtml = '';
let lastCertHtml = '';

// 列表搜索关键字（保持输入焦点，只重绘表格容器）
let FILTER = { ddns: '', cert: '', proxy: '' };

const RING_C = 2 * Math.PI * 40;

const PAGES = [
  { id: 'dashboard', name: '概览', icon: 'i-dashboard', title: '概览', sub: '一眼看清家里的服务是否正常' },
  { id: 'ddns', name: '动态域名', icon: 'i-globe', title: '动态域名', sub: '让域名始终指向你家宽带的最新 IP' },
  { id: 'cert', name: 'SSL 证书', icon: 'i-shield', title: 'SSL 证书', sub: '一键申请免费 HTTPS 证书，到期自动续' },
  { id: 'proxy', name: '反向代理', icon: 'i-shuffle', title: '反向代理', sub: '把内网地址变成一个能在外网打开的网址' },
  { id: 'logs', name: '运行日志', icon: 'i-log', title: '运行日志', sub: '哪里不对，先来这里看' },
  { id: 'settings', name: '系统设置', icon: 'i-settings', title: '系统设置', sub: '账号、端口、访问策略与备份' }
];

/* ---------------- 基础工具 ---------------- */

function $(id) { return document.getElementById(id); }

function toast(msg, type) {
  type = type || 'ok';
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  const icon = type === 'err' ? 'i-x' : (type === 'info' ? 'i-sparkle' : 'i-check');
  el.innerHTML = '<svg class="icon icon-sm"><use href="#' + icon + '"/></svg><span>' + esc(msg) + '</span>';
  $('toasts').appendChild(el);
  setTimeout(() => el.classList.add('out'), 2600);
  setTimeout(() => el.remove(), 2920);
}

function esc(s) {
  if (s === null || s === undefined) return '';
  return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function num(v, d) {
  const n = Number(v);
  return isNaN(n) ? (d === undefined ? 0 : d) : n;
}

// 数字滚动：数值变化时从旧值缓动到新值（420ms），比瞬间跳变更容易看清
// 只写真正变化的文本：显示结果没变就不碰 DOM，2 秒一次的采样不会白白触发重排
const numAnim = new WeakMap();
const valState = new WeakMap();   // 旧值放内存，不写 dataset（属性变更也会触发样式重算）
const bumpAnim = new WeakMap();

function setText(el, s) {
  if (el && el.textContent !== s) el.textContent = s;
}

// "数值变了"的轻微放大：用 Web Animations 播，不上 class、不读 offsetWidth
// （旧实现靠 void offsetWidth 重启 CSS 动画，等于每次采样强制一次同步布局）
function bumpValue(el) {
  if (!el || typeof el.animate !== 'function') return;
  const prev = bumpAnim.get(el);
  if (prev) { try { prev.cancel(); } catch (e) { /* 已被取消 */ } }
  bumpAnim.set(el, el.animate(
    [{ transform: 'scale(1)' }, { transform: 'scale(1.06)' }, { transform: 'scale(1)' }],
    { duration: 340, easing: 'cubic-bezier(.34, 1.56, .64, 1)' }
  ));
}

function animateNum(el, to, fmt, empty) {
  if (!el) return;
  fmt = fmt || (v => String(Math.round(v)));
  if (to === null || to === undefined || !isFinite(to)) {
    setText(el, empty || '--');
    valState.delete(el);
    return;
  }
  const st = valState.get(el) || {};
  const from = st.v;
  st.v = to;
  valState.set(el, st);
  // 格式化后完全一样 → 直接返回，连一次 textContent 都不写
  if (!isFinite(from) || fmt(from) === fmt(to)) { setText(el, fmt(to)); return; }
  const prev = numAnim.get(el);
  if (prev) cancelAnimationFrame(prev);
  const t0 = performance.now(), dur = 420;
  let lastPaint = 0;
  const ease = t => 1 - Math.pow(1 - t, 3);
  // 限制到约 25fps 写入：一段文字每帧重排没必要，肉眼也看不出差别
  const step = now => {
    const p = Math.min(1, (now - t0) / dur);
    if (now - lastPaint >= 40 || p >= 1) {
      lastPaint = now;
      setText(el, fmt(from + (to - from) * ease(p)));
    }
    if (p < 1) numAnim.set(el, requestAnimationFrame(step));
    else { numAnim.delete(el); setText(el, fmt(to)); }
  };
  numAnim.set(el, requestAnimationFrame(step));
  bumpValue(el);
}

let pendingReq = 0;

// 顶部进度条：请求进行中滑动，完成即隐藏（引用计数，支持并发请求）
function netBusy(delta) {
  pendingReq = Math.max(0, pendingReq + delta);
  const bar = $('topProgress');
  if (bar) bar.classList.toggle('on', pendingReq > 0);
}

async function api(path, opts) {
  opts = opts || {};
  const headers = { 'Content-Type': 'application/json' };
  if (TOKEN) headers['X-Token'] = TOKEN;
  let res;
  netBusy(1);
  try {
    res = await fetch(path, {
      method: opts.method || 'GET',
      headers: headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined
    });
  } catch (e) {
    netBusy(-1);
    throw new Error('网络请求失败: ' + e.message);
  }
  netBusy(-1);
  if (res.status === 401) {
    TOKEN = '';
    localStorage.removeItem(TOKEN_KEY);
    stopMetrics();
    showLogin();
    throw new Error('登录已过期');
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok || data.ok === false) throw new Error(data.msg || ('请求失败(' + res.status + ')'));
  return data.data;
}

/* ---------------- 登录 ---------------- */

async function doLogin() {
  if (DEMO) { TOKEN = 'demo'; showApp(); loadState(); return; }
  const user = $('loginUser').value.trim();
  const pass = $('loginPass').value;
  $('loginErr').classList.remove('show');
  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ account: user, password: pass })
    });
    const data = await res.json();
    if (!res.ok || !data.ok) {
      $('loginErr').textContent = data.msg || '登录失败';
      $('loginErr').classList.add('show');
      return;
    }
    TOKEN = data.data.token;
    localStorage.setItem(TOKEN_KEY, TOKEN);
    showApp();
    loadState();
    if (PAGE === 'dashboard') startMetrics();
  } catch (e) {
    $('loginErr').textContent = e.message;
    $('loginErr').classList.add('show');
  }
}

async function doLogout() {
  try { await api('/api/logout', { method: 'POST' }); } catch (e) { /* 忽略 */ }
  TOKEN = '';
  localStorage.removeItem(TOKEN_KEY);
  stopLogStream();
  stopMetrics();
  showLogin();
}

function showLogin() {
  $('login').classList.remove('hidden');
  $('app').classList.add('hidden');
}

function showApp() {
  $('login').classList.add('hidden');
  $('app').classList.remove('hidden');
  renderNav();
  syncHeader();
}

/* ---------------- 导航与页面 ---------------- */

function renderNav() {
  $('nav').innerHTML = PAGES.map(p => {
    const count = p.id === 'ddns' ? (STATE.ddns || []).length
      : p.id === 'cert' ? (STATE.cert || []).length
        : p.id === 'proxy' ? (STATE.proxy || []).length : 0;
    return '<button class="nav-item ' + (PAGE === p.id ? 'active' : '') + '" onclick="go(\'' + p.id + '\')" title="' + esc(p.name) + '">' +
      '<svg class="icon"><use href="#' + p.icon + '"/></svg><span>' + p.name + '</span>' +
      (count ? '<i class="badge">' + count + '</i>' : '') + '</button>';
  }).join('');
}

function syncHeader() {
  const p = PAGES.find(x => x.id === PAGE) || PAGES[0];
  if ($('pageTitle')) $('pageTitle').textContent = p.title;
  if ($('pageSub')) $('pageSub').textContent = p.sub;
}

function go(page) {
  const changed = page !== PAGE;
  PAGE = page;
  syncHeader();
  renderNav();
  if (page !== 'logs') stopLogStream();
  if (page === 'dashboard') startMetrics(); else stopMetrics();
  if (changed) playEnter();
  render();
  if (changed) window.scrollTo(0, 0);
}

// 入场动画只在这里触发：加类 -> 播一次 -> 移除，避免数据刷新时反复重播
let enterTimer = null;
function playEnter() {
  const c = $('content');
  if (!c) return;
  c.classList.add('entering');
  clearTimeout(enterTimer);
  enterTimer = setTimeout(() => c.classList.remove('entering'), 780);
}

function demoState() {
  return {
    base: { adminWebListenPort: 5200, adminAccount: '666', adminPassword: '666', allowInternetAccess: false, sessionTimeout: 7200, logLevel: 'info', autoStart: true },
    ddns: [
      { key: 'ddns-1', enable: true, name: '家里宽带', provider: 'cloudflare', domain: 'example.com', subDomain: 'home', recordType: 'A', ipType: 'ipv4', ipSource: 'api', checkInterval: 300, ttl: 600, currentIP: '203.0.113.7', status: '运行中', lastUpdate: '2026-10-23 06:55:32', updateCount: 12 },
      { key: 'ddns-2', enable: true, name: 'NAS IPv6', provider: 'alidns', domain: 'example.com', subDomain: 'nas', recordType: 'AAAA', ipType: 'ipv6', ipSource: 'interface', interfaceName: 'pppoe-wan', checkInterval: 600, ttl: 600, currentIP: '2408:8207:78cc:5cf1::1', status: '运行中', lastUpdate: '2026-10-23 06:40:11', updateCount: 3 },
      { key: 'ddns-3', enable: false, name: '备用线路', provider: 'dnspod', domain: 'example.com', subDomain: 'bak', recordType: 'A', ipType: 'ipv4', ipSource: 'api', checkInterval: 300, ttl: 600, status: '已禁用', updateCount: 0 }
    ],
    cert: [
      { key: 'cert-1', enable: true, name: '家庭证书', domains: ['home.example.com', 'www.example.com'], email: 'me@example.com', caProvider: 'letsencrypt', keyType: 'ec256', status: '已签发', expireTime: '2027-01-22 22:55:00', lastIssue: '2026-10-23 06:55:17', issueCount: 2, certPath: '/etc/porch/porchconf/certs/家庭证书/fullchain.pem' },
      { key: 'cert-2', enable: true, name: '相册证书', domains: ['photo.example.com'], caProvider: 'letsencrypt', status: '申请中', keyType: 'ec256' }
    ],
    proxy: [
      { key: 'rp-1', enable: true, name: '内网 NAS', listenPort: 8080, domain: 'nas.example.com', pathPrefix: '', backends: ['http://192.168.1.10:5000'], enableTLS: true, enableGzip: true, status: '运行中', requestCount: 1284 },
      { key: 'rp-2', enable: true, name: 'HomeAssistant', listenPort: 8081, domain: 'ha.example.com', backends: ['http://192.168.1.20:8123'], status: '运行中', requestCount: 356 }
    ],
    system: { hostname: 'OpenWrt', os: 'linux', arch: 'arm64', goVersion: 'go1.22.5', uptime: '3 天 6 小时 12 分', cpuCount: 4, loadAvg: '0.12 0.08 0.05', memoryUsed: 412, memoryAll: 1985,
      interfaces: [{ name: 'br-lan', ipv4: ['192.168.1.1'] }, { name: 'pppoe-wan', ipv4: ['10.12.33.7'], ipv6: ['2408:8207:78cc::1'] }, { name: 'eth0', ipv4: [] }] },
    version: '1.1.2', confDir: '/etc/porch/porchconf'
  };
}

function demoMetrics() {
  const now = Date.now();
  const hist = [];
  for (let i = 59; i >= 0; i--) {
    hist.push({
      t: now - i * 2000,
      cpu: Math.max(2, 12 + Math.sin(i / 6) * 8 + Math.random() * 5),
      mem: Math.max(5, 21 + Math.sin(i / 9) * 4 + Math.random() * 2),
      heap: 6 + Math.random() * 2
    });
  }
  const last = hist[hist.length - 1];
  return {
    system: {
      cpuPercent: last.cpu, cpuAvailable: true, memPercent: 38.4,
      memUsedMB: 412, memAllMB: 1985, goroutines: 16, heapMB: 7.2,
      uptimeSec: 3 * 86400 + 6 * 3600 + 12 * 60, history: hist
    },
    certs: [
      { key: 'cert-1', name: '家庭证书', domains: ['home.example.com', 'www.example.com'], status: '已签发', expireTime: '2027-01-22 22:55:00', daysLeft: 91, enable: true },
      { key: 'cert-2', name: '相册证书', domains: ['photo.example.com'], status: '申请中', expireTime: '', daysLeft: -1, enable: true }
    ],
    counters: { ddnsTasks: 3, ddnsUpdated: 15, certTasks: 2, proxyRules: 2, proxyRequests: 1640 }
  };
}

async function loadState() {
  if (DEMO) {
    STATE = Object.assign(demoState(), { meta: STATE.meta });
    $('versionText').textContent = 'v' + (STATE.version || '');
    $('footInfo').textContent = (STATE.system.hostname || '') + ' · ' + (STATE.system.uptime || '');
    renderNav(); render(); updateHealth();
    return;
  }
  try {
    STATE = await api('/api/state');
    STATE.metrics = STATE.metrics || null;
    $('versionText').textContent = 'v' + (STATE.version || '');
    const sys = STATE.system || {};
    $('footInfo').textContent = (sys.hostname || '') + ' · ' + (sys.uptime || '');
    renderNav();
    // 仪表盘只看结构变化：运行时长/负载/内存每 15 秒必变，但那不是"结构变化"。
    // 以前只要这几个字变了就整页重建 DOM，曲线图跟着被清空重画——
    // 看起来就像"曲线图一刷新，页面也跟着刷新"。现在结构没变就只改那几个字。
    const sig = structSig();
    if (PAGE === 'dashboard' && lastRender.page === 'dashboard' && sig === lastStructSig) {
      updateDashboardLive();
    } else {
      lastStructSig = sig;
      render();
    }
    updateHealth();
  } catch (e) {
    if (e.message !== '登录已过期') toast(e.message, 'err');
  }
}

// 页面结构签名：任务列表/版本变了才值得整页重绘
function structSig() {
  return JSON.stringify([STATE.version, STATE.ddns, STATE.cert, STATE.proxy]);
}
let lastStructSig = '';

// 仪表盘上每 15 秒会变的几个字段，原地改文本，不动 DOM 结构
function updateDashboardLive() {
  const sys = STATE.system || {};
  setText($('upVal'), sys.uptime || '-');
  setText($('loadVal'), sys.loadAvg || '-');
  setText($('memRowVal'), sys.memoryAll
    ? (sys.memoryUsed || 0) + ' / ' + sys.memoryAll + ' MB'
    : (sys.memoryUsed || 0) + ' MB');
}

async function reloadService() {
  try {
    await api('/api/service/reload', { method: 'POST' });
    toast('已按最新配置重载服务', 'ok');
    setTimeout(loadState, 800);
  } catch (e) { toast(e.message, 'err'); }
}

// 上一次渲染结果，用于差异更新：15 秒自动刷新时内容没变就不重绘，
// 既省掉一次全量 DOM 重建，也避免入场动画被重复播放（观感上像"卡顿"）
let lastRender = { page: '', html: '' };

function render(force) {
  const c = $('content');
  let html = '';
  switch (PAGE) {
    case 'dashboard': html = renderDashboard(); break;
    case 'ddns': html = renderDDNS(); break;
    case 'cert': html = renderCert(); break;
    case 'proxy': html = renderProxy(); break;
    case 'logs': html = renderLogs(); break;
    case 'settings': html = renderSettings(); break;
  }
  const firstPaint = !lastRender.page;
  if (force || html !== lastRender.html || PAGE !== lastRender.page) {
    c.innerHTML = html;
    if (firstPaint) playEnter();
    lastRender = { page: PAGE, html: html };
    lastStructSig = structSig();
    // DOM 已重建，之前缓存的片段 html 失效，必须允许重新写入
    lastKVHtml = '';
    lastCertHtml = '';
    if (PAGE === 'dashboard') { paintMetrics(); updateHealth(); }
    if (PAGE === 'logs') startLogStream();
  }
  // 保证任何进入路径（登录 / 刷新 / 切页）都开启指标轮询
  if (PAGE === 'dashboard' && !metricsTimer) startMetrics();
}

/* ================= 监控看板 ================= */

function statusTag(status) {
  if (!status) return '<span class="tag">未运行</span>';
  if (/失败|错误/.test(status)) return '<span class="tag tag-err">' + esc(status) + '</span>';
  if (/未|停止/.test(status)) return '<span class="tag">' + esc(status) + '</span>';
  return '<span class="tag tag-ok">' + esc(status) + '</span>';
}

function ringMarkup(id, color, label) {
  const c = RING_C.toFixed(2);
  return '<div class="ring-item">' +
    '<div class="ring-wrap">' +
    '<svg class="ring-svg" viewBox="0 0 96 96" aria-hidden="true">' +
    '<circle class="ring-bg" cx="48" cy="48" r="40"></circle>' +
    '<circle class="ring-fg" id="' + id + '" cx="48" cy="48" r="40" stroke="' + color +
    '" stroke-dasharray="' + c + '" stroke-dashoffset="' + c + '"></circle>' +
    '</svg>' +
    '<div class="ring-center" id="' + id + 'Val">--</div>' +
    '</div>' +
    '<div class="ring-label">' + label + '</div>' +
    '</div>';
}

/* ---------------- 上手向导：把"要做什么"直接摆在第一屏 ---------------- */

const GUIDE_STEPS = [
  {
    icon: 'i-globe', page: 'ddns',
    title: '让域名指向你家',
    desc: '填好域名和密钥，Porch 每隔几分钟自动把最新公网 IP 写进解析记录。'
  },
  {
    icon: 'i-shield', page: 'cert',
    title: '开启 HTTPS',
    desc: '填一个域名点"申请"，自动拿到免费证书，到期前 20 天会自己续期。'
  },
  {
    icon: 'i-shuffle', page: 'proxy',
    title: '把内网服务发出去',
    desc: '例如把 nas.你的域名.com 转发到 192.168.1.10:5000，外网就能访问。'
  }
];

function guideDoneState() {
  const ddns = (STATE.ddns || []).length > 0;
  const cert = (STATE.cert || []).length > 0;
  const proxy = (STATE.proxy || []).length > 0;
  return [ddns, cert, proxy];
}

function renderGuide() {
  const done = guideDoneState();
  const n = done.filter(Boolean).length;

  // 三步全部就绪后收成一行，把屏幕让给真正的数据
  if (n === 3) {
    return '<section class="card guide">' +
      '<div class="guide-head" style="margin-bottom:0">' +
      '<svg class="icon" style="color:var(--success)"><use href="#i-check"/></svg>' +
      '<h2>三步都已就绪</h2>' +
      '<div class="desc">域名、证书、转发都在正常工作，随时可以在左侧调整</div>' +
      '<div class="guide-progress"><span>' + n + '/3</span><span class="bar"><i style="width:100%"></i></span></div>' +
      '</div></section>';
  }

  const steps = GUIDE_STEPS.map((st, i) => {
    const ok = done[i];
    return '<div class="guide-step' + (ok ? ' done' : '') + '" onclick="go(\'' + st.page + '\')" title="' + (ok ? '去看一眼' : '开始这一步') + '">' +
      '<div class="guide-num">' + (ok ? '✓' : (i + 1)) + '</div>' +
      '<div class="guide-body"><b>' + esc(st.title) + '</b><span>' + esc(st.desc) + '</span></div>' +
      '<svg class="icon icon-sm go"><use href="#i-arrow"/></svg>' +
      '</div>';
  }).join('');

  return '<section class="card guide">' +
    '<div class="guide-head">' +
    '<svg class="icon" style="color:var(--primary)"><use href="#i-porch"/></svg>' +
    '<h2>三步让家里的服务安全出门</h2>' +
    '<div class="desc">照着点就行，不用懂网络术语</div>' +
    '<div class="guide-progress"><span>' + n + '/3 已完成</span>' +
    '<span class="bar"><i style="width:' + Math.round(n / 3 * 100) + '%"></i></span></div>' +
    '</div>' +
    '<div class="guide-steps">' + steps + '</div>' +
    '</section>';
}

function renderDashboard() {
  const sys = STATE.system || {};
  const ddnsOK = (STATE.ddns || []).filter(t => t.enable && !/失败|错误/.test(t.status || '')).length;
  const certOK = (STATE.cert || []).filter(t => (t.status || '') === '已签发').length;
  const proxyOn = (STATE.proxy || []).filter(t => t.enable).length;

  const cards = [
    { icon: 'i-globe', label: '动态域名任务', value: (STATE.ddns || []).length, hint: ddnsOK + ' 个运行中' },
    { icon: 'i-shield', label: 'SSL 证书', value: (STATE.cert || []).length, hint: certOK + ' 张已签发' },
    { icon: 'i-shuffle', label: '反向代理规则', value: (STATE.proxy || []).length, hint: proxyOn + ' 条已启用' },
    { icon: 'i-clock', label: '运行时长', value: sys.uptime || '-', hint: (sys.os || '') + ' / ' + (sys.arch || ''), id: 'upVal' }
  ].map(s => '<div class="stat"><div class="label"><svg class="icon icon-sm"><use href="#' + s.icon + '"/></svg>' +
    s.label + '</div><div class="value"' + (s.id ? ' id="' + s.id + '"' : '') + '>' + esc(s.value) + '</div><div class="hint">' + esc(s.hint) + '</div></div>').join('');

  const charts =
    '<section class="card chart-card">' +
    '<div class="chart-head"><svg class="icon icon-sm" style="color:var(--chart-cpu)"><use href="#i-cpu"/></svg>' +
    '<h2>CPU 使用率</h2><div class="big"><b id="cpuNow">--</b><span>%</span></div></div>' +
    '<canvas id="cpuChart" class="chart-canvas" aria-label="CPU 使用率曲线"></canvas>' +
    '<div class="chart-foot"><span>最近 3 分钟</span><span id="cpuHint">采样中…</span></div></section>' +

    '<section class="card chart-card">' +
    '<div class="chart-head"><svg class="icon icon-sm" style="color:var(--chart-mem)"><use href="#i-memory"/></svg>' +
    '<h2>内存使用率</h2><div class="big"><b id="memNow">--</b><span>%</span><span id="memSub">--</span></div></div>' +
    '<canvas id="memChart" class="chart-canvas" aria-label="内存使用率曲线"></canvas>' +
    '<div class="chart-foot"><span>最近 3 分钟</span><span id="memHint">采样中…</span></div></section>';

  const rings = '<section class="card">' +
    '<div class="card-head"><h2>运行指标</h2><div class="desc">进程与系统实时占用</div></div>' +
    '<div class="grid grid-2" style="gap:18px;align-items:start">' +
    '<div class="ring-grid">' +
    ringMarkup('ringCpu', '#2477ad', 'CPU') +
    ringMarkup('ringMem', '#7b5cf0', '内存') +
    ringMarkup('ringGo', '#2ba36b', 'Goroutine') +
    ringMarkup('ringHeap', '#d98b14', '堆内存') +
    '</div>' +
    '<div class="kv-list" id="kvList"></div>' +
    '</div></section>';

  const certCard = '<section class="card">' +
    '<div class="card-head"><h2>证书到期</h2><div class="desc">7 天内为红色预警</div></div>' +
    '<div id="certList"><div class="chart-empty" style="height:auto;padding:16px 0">加载中…</div></div></section>';

  const rows = [];
  (STATE.ddns || []).forEach(t => rows.push(['动态域名', t.name, (t.subDomain ? t.subDomain + '.' : '') + t.domain, statusTag(t.status), t.currentIP || '-']));
  (STATE.cert || []).forEach(t => rows.push(['SSL 证书', t.name, (t.domains || []).join(', '), statusTag(t.status), t.expireTime || '-']));
  (STATE.proxy || []).forEach(t => rows.push(['反向代理', t.name, ':' + t.listenPort + ' → ' + (t.backends || []).join(', '), statusTag(t.status), (t.requestCount || 0) + ' 次请求']));

  const table = rows.length ? '<div class="table-wrap"><table><thead><tr><th>类型</th><th>名称</th><th>目标</th><th>状态</th><th>详情</th></tr></thead><tbody>' +
    rows.map(r => '<tr><td>' + r[0] + '</td><td class="name">' + esc(r[1]) + '</td><td>' + esc(r[2]) + '</td><td>' + r[3] + '</td><td class="sub">' + esc(r[4]) + '</td></tr>').join('') +
    '</tbody></table></div>' : emptyBox('还没有任何任务', '从上面的三步开始，或者直接新建一条规则。', '新建动态域名', "go('ddns')");

  const sysInfo = '<div class="grid grid-2">' +
    infoCard('主机信息', [
      ['主机名', sys.hostname], ['系统架构', (sys.os || '') + ' / ' + (sys.arch || '')],
      ['CPU 核心', sys.cpuCount], ['负载', sys.loadAvg || '-', 'loadVal'],
      ['内存', sys.memoryAll ? (sys.memoryUsed || 0) + ' / ' + sys.memoryAll + ' MB' : (sys.memoryUsed || 0) + ' MB', 'memRowVal'],
      ['Go 版本', sys.goVersion]
    ]) +
    infoCard('网络接口（前 6 个）', (sys.interfaces || []).slice(0, 6).map(i =>
      [i.name, (i.ipv4 || []).join(', ') || (i.ipv6 || []).join(', ') || '-'])) +
    '</div>';

  return renderGuide() +
    '<div class="stat-grid">' + cards + '</div>' +
    '<div class="monitor-grid">' + charts + '</div>' +
    '<div class="grid grid-2">' + rings + certCard + '</div>' +
    '<div class="card"><div class="card-head"><h2>任务总览</h2><div class="desc">全部模块的运行状态</div></div>' + table + '</div>' +
    sysInfo;
}

function infoCard(title, rows) {
  // r[2] 可选：给值单元格一个 id，让 15 秒轮询能原地改文本（避免整卡重建）
  return '<div class="card"><div class="card-head"><h2>' + esc(title) + '</h2></div><table><tbody>' +
    (rows || []).map(r => '<tr><td class="sub" style="width:38%">' + esc(r[0]) + '</td><td' +
      (r[2] ? ' id="' + r[2] + '"' : '') + '>' + esc(r[1] === undefined || r[1] === null ? '-' : r[1]) + '</td></tr>').join('') +
    '</tbody></table></div>';
}

function emptyBox(text, sub, btnText, onclick) {
  return '<div class="empty">' +
    '<svg class="icon icon-lg"><use href="#i-porch"/></svg>' +
    '<div>' + esc(text) + '</div>' +
    (sub ? '<div class="empty-sub">' + esc(sub) + '</div>' : '') +
    (btnText ? '<button class="btn btn-primary btn-sm" onclick="' + (onclick || '') + '">' +
      '<svg class="icon icon-sm"><use href="#i-plus"/></svg> ' + esc(btnText) + '</button>' : '') +
    '</div>';
}

/* ---------------- 指标轮询与绘制 ---------------- */

function startMetrics() {
  stopMetrics();
  loadMetrics();
  metricsTimer = setInterval(loadMetrics, 2000);
}

function stopMetrics() {
  if (metricsTimer) { clearInterval(metricsTimer); metricsTimer = null; }
}

async function loadMetrics() {
  if (document.hidden) return;   // 后台标签页不刷新，回到前台立刻补一次
  if (DEMO) { STATE.metrics = demoMetrics(); paintMetrics(); updateHealth(); return; }
  try {
    STATE.metrics = await api('/api/metrics');
    paintMetrics();
    updateHealth();
  } catch (e) { /* 指标失败不打扰用户，下个周期重试 */ }
}

function paintMetrics() {
  const m = STATE.metrics;
  if (!m) return;
  const sys = m.system || {};
  const hist = sys.history || [];
  const avail = sys.cpuAvailable !== false;

  animateNum($('cpuNow'), avail ? num(sys.cpuPercent) : NaN, v => v.toFixed(1), '--');
  animateNum($('memNow'), num(sys.memPercent), v => v.toFixed(1), '--');
  setText($('memSub'), num(sys.memUsedMB) + ' / ' + num(sys.memAllMB) + ' MB');
  setText($('cpuHint'), avail ? (hist.length + ' 个采样点') : '当前平台不支持 CPU 采样');
  setText($('memHint'), hist.length + ' 个采样点');

  drawLineChart($('cpuChart'), hist.map(s => s.cpu), cssVar('--chart-cpu'));
  drawLineChart($('memChart'), hist.map(s => s.mem), cssVar('--chart-mem'));

  const cpuPct = avail ? num(sys.cpuPercent) : 0;
  const memPct = num(sys.memPercent);
  const goN = num(sys.goroutines);
  const heap = num(sys.heapMB);
  const heapPct = num(sys.memAllMB) > 0 ? heap / num(sys.memAllMB) * 100 : 0;
  setRing('ringCpu', cpuPct, Math.round(cpuPct) + '%');
  setRing('ringMem', memPct, Math.round(memPct) + '%');
  setRing('ringGo', Math.min(100, goN * 2), String(goN));
  setRing('ringHeap', Math.min(100, heapPct * 2), heap.toFixed(0) + 'M');

  const c = m.counters || {};
  if ($('kvList')) {
    const kv = [
      ['i-activity', 'Goroutine 数', String(goN)],
      ['i-memory', 'Go 堆内存', heap.toFixed(1) + ' MB'],
      ['i-shuffle', '代理请求总数', num(c.proxyRequests).toLocaleString()],
      ['i-refresh', 'DDNS 更新次数', String(num(c.ddnsUpdated))],
      ['i-clock', '进程运行', fmtUptime(sys.uptimeSec)]
    ];
    const kvHtml = kv.map(r => '<div class="kv-row"><span class="kv-key">' +
      '<svg class="icon icon-sm"><use href="#' + r[0] + '"/></svg>' + r[1] +
      '</span><span class="kv-val">' + esc(r[2]) + '</span></div>').join('');
    // 内容没变就不动 DOM，避免每 2 秒重建一次造成闪烁
    if (kvHtml !== lastKVHtml) { $('kvList').innerHTML = kvHtml; lastKVHtml = kvHtml; }
  }

  paintCerts(m.certs || []);
}

function setRing(id, pct, text) {
  const el = $(id);
  if (!el) return;
  const p = Math.max(0, Math.min(100, num(pct)));
  const off = (RING_C * (1 - p / 100)).toFixed(2);
  // 只在真的变了才写属性：SVG 属性变更同样会触发样式重算
  if (el.getAttribute('stroke-dashoffset') !== off) el.setAttribute('stroke-dashoffset', off);
  setText($(id + 'Val'), text);
}

function paintCerts(list) {
  const box = $('certList');
  if (!box) return;
  if (!list.length) {
    const h = emptyBox('还没有证书任务', '添加一张证书，外网访问就会用上 HTTPS。', '去添加证书', "go('cert')");
    if (h !== lastCertHtml) { box.innerHTML = h; lastCertHtml = h; }
    return;
  }
  const html = list.map(c => {
    const d = typeof c.daysLeft === 'number' ? c.daysLeft : -1;
    let cls = '', pct = 0, txt = '', raw = '';
    if (d >= 0) {
      pct = Math.max(4, Math.min(100, d / 90 * 100));
      txt = d + ' 天';
      cls = d <= 7 ? 'err' : (d <= 20 ? 'warn' : '');
    } else {
      raw = c.status || '未签发';
      // 失败详情可能很长，折叠成"失败"，完整信息放在 title 里
      txt = /失败|错误/.test(raw) ? '失败' : raw;
      cls = /失败|错误/.test(raw) ? 'err' : '';
    }
    const color = cls === 'err' ? 'var(--danger)' : (cls === 'warn' ? 'var(--warn)' : 'var(--success)');
    // 续期失败不影响已签发，但把原因挂在 title 上便于排查
    const tip = (raw || txt) + (c.lastError ? '\n最近一次续期失败：' + c.lastError : '');
    return '<div class="cert-row">' +
      '<div class="cert-name"><b title="' + esc(c.name || '') + '">' + esc(c.name || (c.domains || [])[0] || '未命名') + '</b>' +
      '<span title="' + esc((c.domains || []).join(', ')) + '">' + esc((c.domains || []).join(', ')) + '</span></div>' +
      '<div class="cert-bar"><i class="' + cls + '" style="width:' + pct.toFixed(0) + '%"></i></div>' +
      '<div class="cert-days" style="color:' + color + '" title="' + esc(tip) + '">' + esc(txt) + '</div>' +
      '</div>';
  }).join('');
  if (html !== lastCertHtml) { box.innerHTML = html; lastCertHtml = html; }
}

function updateHealth() {
  const badge = $('healthBadge'), text = $('healthText');
  if (!badge || !text) return;
  let err = 0, warn = 0;
  (STATE.ddns || []).forEach(t => { if (/失败|错误/.test(t.status || '')) err++; });
  (STATE.proxy || []).forEach(t => { if (/失败|错误/.test(t.status || '')) err++; });
  (STATE.cert || []).forEach(t => {
    if (/失败|错误/.test(t.status || '')) err++;
    else if (t.lastError) warn++; // 已签发但最近一次续期失败，算关注项
  });
  const certs = (STATE.metrics && STATE.metrics.certs) || [];
  certs.forEach(c => {
    if (typeof c.daysLeft === 'number' && c.daysLeft >= 0 && c.daysLeft <= 20) warn++;
  });
  if (err) { badge.className = 'health-badge err'; text.textContent = err + ' 项异常'; }
  else if (warn) { badge.className = 'health-badge warn'; text.textContent = warn + ' 项关注'; }
  else { badge.className = 'health-badge ok'; text.textContent = '健康'; }
}

function fmtUptime(sec) {
  sec = num(sec);
  if (sec <= 0) return '-';
  const d = Math.floor(sec / 86400);
  const h = Math.floor(sec % 86400 / 3600);
  const mi = Math.floor(sec % 3600 / 60);
  if (d > 0) return d + ' 天 ' + h + ' 小时';
  if (h > 0) return h + ' 小时 ' + mi + ' 分';
  return mi + ' 分';
}

/* ---------------- Canvas 折线图 ---------------- */

function cssVar(name) {
  return (getComputedStyle(document.documentElement).getPropertyValue(name) || '').trim() || '#2477ad';
}

function hexToRgba(color, a) {
  color = (color || '').trim();
  if (color.indexOf('rgb') === 0) return color;
  let hex = color.replace('#', '');
  if (hex.length === 3) hex = hex[0] + hex[0] + hex[1] + hex[1] + hex[2] + hex[2];
  const n = parseInt(hex, 16);
  if (isNaN(n)) return 'rgba(36,119,173,' + a + ')';
  return 'rgba(' + ((n >> 16) & 255) + ',' + ((n >> 8) & 255) + ',' + (n & 255) + ',' + a + ')';
}

function drawLineChart(cvs, values, color, maxV) {
  if (!cvs) return;
  maxV = maxV || 100;
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const rect = cvs.getBoundingClientRect();
  const w = Math.max(rect.width, 10), h = Math.max(rect.height, 10);
  if (cvs.width !== Math.round(w * dpr) || cvs.height !== Math.round(h * dpr)) {
    cvs.width = Math.round(w * dpr);
    cvs.height = Math.round(h * dpr);
  }
  const ctx = cvs.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);

  const padL = 4, padR = 6, padT = 10, padB = 8;
  const iw = w - padL - padR, ih = h - padT - padB;

  // 横向网格
  ctx.strokeStyle = 'rgba(30,45,70,0.07)';
  ctx.lineWidth = 1;
  for (let i = 0; i <= 4; i++) {
    const y = Math.round(padT + ih * i / 4) + 0.5;
    ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke();
  }

  const vals = (values || []).filter(v => typeof v === 'number' && !isNaN(v));
  if (!vals.length) return;
  const n = vals.length;
  const xAt = i => padL + (n === 1 ? iw / 2 : iw * i / (n - 1));
  const yAt = v => padT + ih * (1 - Math.max(0, Math.min(maxV, v)) / maxV);

  // 面积渐变
  const grad = ctx.createLinearGradient(0, padT, 0, padT + ih);
  grad.addColorStop(0, hexToRgba(color, 0.30));
  grad.addColorStop(1, hexToRgba(color, 0));
  ctx.beginPath();
  ctx.moveTo(xAt(0), padT + ih);
  for (let i = 0; i < n; i++) ctx.lineTo(xAt(i), yAt(vals[i]));
  ctx.lineTo(xAt(n - 1), padT + ih);
  ctx.closePath();
  ctx.fillStyle = grad;
  ctx.fill();

  // 折线
  ctx.beginPath();
  for (let i = 0; i < n; i++) {
    const x = xAt(i), y = yAt(vals[i]);
    if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
  }
  ctx.strokeStyle = color;
  ctx.lineWidth = 2;
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';
  ctx.stroke();

  // 末端高亮
  const lx = xAt(n - 1), ly = yAt(vals[n - 1]);
  ctx.beginPath(); ctx.arc(lx, ly, 3, 0, Math.PI * 2);
  ctx.fillStyle = '#fff'; ctx.fill();
  ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.stroke();

  // 首次拿到数据时，让曲线从左到右展开一次
  if (!cvs.classList.contains('ready')) cvs.classList.add('ready');
}

function repaintCharts() {
  if (PAGE !== 'dashboard') return;
  const m = STATE.metrics;
  if (!m) return;
  const hist = (m.system && m.system.history) || [];
  drawLineChart($('cpuChart'), hist.map(s => s.cpu), cssVar('--chart-cpu'));
  drawLineChart($('memChart'), hist.map(s => s.mem), cssVar('--chart-mem'));
}

let resizeTimer = null;
window.addEventListener('resize', () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(repaintCharts, 120);
});
document.addEventListener('fullscreenchange', () => setTimeout(repaintCharts, 160));

/* ================= 动态域名 ================= */

function renderDDNS() {
  return '<div class="card"><div class="card-head"><h2>动态域名任务</h2>' +
    '<div class="desc">支持 Cloudflare / 阿里云 / DNSPod / GoDaddy / DuckDNS / No-IP / 自定义回调</div>' +
    '<div class="right">' +
    '<label class="search-box"><svg class="icon icon-sm"><use href="#i-search"/></svg>' +
    '<input id="q_ddns" placeholder="搜索名称 / 域名 / 服务商" value="' + esc(FILTER.ddns) + '" oninput="setFilter(\'ddns\', this.value)"></label>' +
    '<button class="btn btn-primary btn-sm" data-action="ddns-add" onclick="editDDNS()"><svg class="icon icon-sm"><use href="#i-plus"/></svg> 新增任务</button>' +
    '</div></div><div id="list-ddns">' + ddnsTableHtml() + '</div></div>';
}

function ddnsTableHtml() {
  const q = FILTER.ddns.trim().toLowerCase();
  const list = (STATE.ddns || []).filter(t => !q ||
    [t.name, (t.subDomain ? t.subDomain + '.' : '') + t.domain, t.provider, t.currentIP]
      .join(' ').toLowerCase().indexOf(q) >= 0);
  if (!list.length) return q
    ? emptyBox('没有匹配的任务', '换个关键词试试，或者清空搜索框。')
    : emptyBox('还没有动态域名任务', '添加后 Porch 会把最新的公网 IP 自动同步到你的域名。', '新增第一条任务', 'editDDNS()');
  return '<div class="table-wrap"><table><thead><tr><th>启用</th><th>名称</th><th>域名</th><th>服务商</th><th>记录</th><th>当前 IP</th><th>状态</th><th style="text-align:right">操作</th></tr></thead><tbody>' +
    list.map(t => '<tr>' +
      '<td><button class="toggle ' + (t.enable ? 'on' : '') + '" data-action="ddns-toggle" data-key="' + esc(t.key) + '" onclick="toggleTask(\'ddns\',\'' + t.key + '\')" title="' + (t.enable ? '点击停用' : '点击启用') + '"></button></td>' +
      '<td><div class="name">' + esc(t.name || '未命名') + '</div><div class="sub">每 ' + num(t.checkInterval, 300) + ' 秒检查</div></td>' +
      '<td>' + esc((t.subDomain ? t.subDomain + '.' : '') + (t.domain || '')) + '</td>' +
      '<td>' + esc(t.provider) + '</td>' +
      '<td>' + esc(t.recordType) + ' / ' + esc(t.ipType) + '</td>' +
      '<td class="sub">' + esc(t.currentIP || '-') + '</td>' +
      '<td>' + statusTag(t.enable ? (t.status || '运行中') : '已禁用') + '</td>' +
      '<td><div class="actions">' +
      '<button class="btn btn-sm btn-ghost" data-action="ddns-run" onclick="runDDNS(\'' + t.key + '\')" title="立即执行"><svg class="icon icon-sm"><use href="#i-play"/></svg></button>' +
      '<button class="btn btn-sm btn-ghost" data-action="ddns-edit" onclick="editDDNS(\'' + t.key + '\')" title="编辑"><svg class="icon icon-sm"><use href="#i-edit"/></svg></button>' +
      '<button class="btn btn-sm btn-danger" data-action="ddns-del" onclick="delDDNS(\'' + t.key + '\')" title="删除"><svg class="icon icon-sm"><use href="#i-trash"/></svg></button>' +
      '</div></td></tr>').join('') + '</tbody></table></div>';
}

async function editDDNS(key) {
  const meta = await getMeta();
  const t = key ? (STATE.ddns || []).find(x => x.key === key) : null;
  const providers = (meta && meta.ddnsProviders) || [];
  const ifaces = (meta && meta.interfaces) || [];
  const cur = t || {
    name: '', provider: 'cloudflare', domain: '', subDomain: '', accessID: '', accessKey: '',
    recordType: 'A', ipType: 'ipv4', ipSource: 'api', interfaceName: '', manualIP: '',
    checkInterval: 300, forceInterval: 0, ttl: 600, webhookURL: '', enable: true
  };
  const html = '' +
    '<div class="row"><div class="field"><label>任务名称</label><input id="f_name" value="' + esc(cur.name) + '" placeholder="例如：家里宽带"></div>' +
    '<div class="field"><label>启用</label><label class="switch"><input type="checkbox" id="f_enable" ' + (cur.enable ? 'checked' : '') + '><span class="track"></span><span>开机自动运行</span></label></div></div>' +
    '<div class="field"><label>服务商</label><select id="f_provider">' +
    providers.map(p => '<option value="' + p.value + '" ' + (cur.provider === p.value ? 'selected' : '') + '>' + esc(p.label) + '</option>').join('') +
    '</select></div>' +
    '<div class="row"><div class="field"><label id="f_idLabel">' + esc(idLabel(providers, cur.provider)) + '</label><input id="f_accessID" value="' + esc(cur.accessID) + '"></div>' +
    '<div class="field"><label id="f_keyLabel">' + esc(keyLabel(providers, cur.provider)) + '</label><input id="f_accessKey" type="password" value="' + esc(cur.accessKey) + '"></div></div>' +
    '<div class="row"><div class="field"><label>主域名</label><input id="f_domain" value="' + esc(cur.domain) + '" placeholder="example.com"></div>' +
    '<div class="field"><label>主机记录</label><input id="f_subDomain" value="' + esc(cur.subDomain) + '" placeholder="www 或留空"></div></div>' +
    '<div class="row">' +
    '<div class="field"><label>记录类型</label><select id="f_recordType"><option value="A" ' + (cur.recordType === 'A' ? 'selected' : '') + '>A (IPv4)</option><option value="AAAA" ' + (cur.recordType === 'AAAA' ? 'selected' : '') + '>AAAA (IPv6)</option></select></div>' +
    '<div class="field"><label>IP 类型</label><select id="f_ipType"><option value="ipv4" ' + (cur.ipType === 'ipv4' ? 'selected' : '') + '>IPv4</option><option value="ipv6" ' + (cur.ipType === 'ipv6' ? 'selected' : '') + '>IPv6</option></select></div></div>' +
    '<div class="row"><div class="field"><label>IP 来源</label><select id="f_ipSource">' +
    '<option value="api" ' + (cur.ipSource === 'api' ? 'selected' : '') + '>公网接口探测</option>' +
    '<option value="interface" ' + (cur.ipSource === 'interface' ? 'selected' : '') + '>指定网卡</option>' +
    '<option value="manual" ' + (cur.ipSource === 'manual' ? 'selected' : '') + '>手动指定</option></select></div>' +
    '<div class="field"><label>网卡 / 手动 IP</label><input id="f_iface" value="' + esc(cur.ipSource === 'manual' ? cur.manualIP : cur.interfaceName) + '" placeholder="eth0 或 1.2.3.4" list="ifaceList">' +
    '<datalist id="ifaceList">' + ifaces.map(i => '<option value="' + esc(i.name) + '">').join('') + '</datalist></div></div>' +
    '<div class="row"><div class="field"><label>检查间隔(秒)</label><input id="f_interval" type="number" value="' + num(cur.checkInterval, 300) + '"></div>' +
    '<div class="field"><label>强制更新间隔(小时)</label><input id="f_force" type="number" value="' + num(cur.forceInterval, 0) + '"><div class="tip">0 表示仅 IP 变化时才更新</div></div></div>' +
    '<div class="row"><div class="field"><label>TTL(秒)</label><input id="f_ttl" type="number" value="' + num(cur.ttl, 600) + '"></div>' +
    '<div class="field"><label>Webhook 通知</label><input id="f_webhook" value="' + esc(cur.webhookURL) + '" placeholder="可选，支持 #{ip} 占位符"></div></div>';

  openModal(key ? '编辑动态域名任务' : '新增动态域名任务',
    guideTip('填写域名服务商的密钥后，Porch 会定期把最新的公网 IP 写进这条域名。密钥只保存在本机。') + html,
    async () => {
    const src = $('f_ipSource').value;
    const body = {
      name: $('f_name').value.trim(),
      enable: $('f_enable').checked,
      provider: $('f_provider').value,
      accessID: $('f_accessID').value.trim(),
      accessKey: $('f_accessKey').value,
      domain: $('f_domain').value.trim(),
      subDomain: $('f_subDomain').value.trim(),
      recordType: $('f_recordType').value,
      ipType: $('f_ipType').value,
      ipSource: src,
      interfaceName: src === 'interface' ? $('f_iface').value.trim() : '',
      manualIP: src === 'manual' ? $('f_iface').value.trim() : '',
      checkInterval: parseInt($('f_interval').value, 10) || 300,
      forceInterval: parseInt($('f_force').value, 10) || 0,
      ttl: parseInt($('f_ttl').value, 10) || 600,
      webhookURL: $('f_webhook').value.trim()
    };
    if (!body.domain) { toast('请填写主域名', 'err'); return; }
    try {
      if (key) await api('/api/ddns/' + key, { method: 'PUT', body: body });
      else await api('/api/ddns', { method: 'POST', body: body });
      closeModal();
      toast('已保存', 'ok');
      loadState();
    } catch (e) { toast(e.message, 'err'); }
  });

  $('f_provider').addEventListener('change', () => {
    $('f_idLabel').textContent = idLabel(providers, $('f_provider').value);
    $('f_keyLabel').textContent = keyLabel(providers, $('f_provider').value);
  });
}

function idLabel(list, v) { const p = list.find(x => x.value === v); return p && p.idLabel ? p.idLabel : 'Access ID'; }
function keyLabel(list, v) { const p = list.find(x => x.value === v); return p && p.keyLabel ? p.keyLabel : 'Access Key'; }

async function delDDNS(key) {
  confirmDialog('删除动态域名任务', '删除后不再更新这条域名的解析记录，已有的解析不会被改动。', async () => {
    try { await api('/api/ddns/' + key, { method: 'DELETE' }); toast('已删除'); loadState(); }
    catch (e) { toast(e.message, 'err'); }
  });
}

async function runDDNS(key) {
  try { await api('/api/ddns/' + key + '/run', { method: 'POST' }); toast('已触发执行，请查看状态', 'info'); setTimeout(loadState, 1200); }
  catch (e) { toast(e.message, 'err'); }
}

/* ================= SSL 证书 ================= */

function renderCert() {
  return '<div class="card"><div class="card-head"><h2>SSL 证书任务</h2>' +
    '<div class="desc">ACME v2 HTTP-01 自动签发，到期前自动续期</div>' +
    '<div class="right">' +
    '<label class="search-box"><svg class="icon icon-sm"><use href="#i-search"/></svg>' +
    '<input id="q_cert" placeholder="搜索名称 / 域名" value="' + esc(FILTER.cert) + '" oninput="setFilter(\'cert\', this.value)"></label>' +
    '<button class="btn btn-primary btn-sm" data-action="cert-add" onclick="editCert()"><svg class="icon icon-sm"><use href="#i-plus"/></svg> 新增证书</button>' +
    '</div></div><div id="list-cert">' + certTableHtml() + '</div></div>';
}

function certDaysCell(expire) {
  const d = certDaysLeft(expire);
  if (d === null) return '<span class="sub">-</span>';
  const cls = d <= 7 ? 'tag-err' : (d <= 20 ? 'tag-warn' : 'tag-ok');
  return '<span class="tag ' + cls + '">' + d + ' 天</span>';
}

function certDaysLeft(expire) {
  if (!expire) return null;
  const t = new Date(String(expire).replace(/-/g, '/')).getTime();
  if (isNaN(t)) return null;
  return Math.floor((t - Date.now()) / 86400000);
}

function certTableHtml() {
  const q = FILTER.cert.trim().toLowerCase();
  const list = (STATE.cert || []).filter(t => !q ||
    [t.name, (t.domains || []).join(' '), t.caProvider].join(' ').toLowerCase().indexOf(q) >= 0);
  if (!list.length) return q
    ? emptyBox('没有匹配的证书', '换个关键词试试，或者清空搜索框。')
    : emptyBox('还没有证书任务', '填一个域名，点「申请」就能拿到免费证书并自动续期。', '申请第一张证书', 'editCert()');
  return '<div class="table-wrap"><table><thead><tr><th>启用</th><th>名称</th><th>域名</th><th>颁发机构</th><th>到期时间</th><th>剩余</th><th>状态</th><th style="text-align:right">操作</th></tr></thead><tbody>' +
    list.map(t => '<tr>' +
      '<td><button class="toggle ' + (t.enable ? 'on' : '') + '" data-action="cert-toggle" data-key="' + esc(t.key) + '" onclick="toggleTask(\'cert\',\'' + t.key + '\')" title="' + (t.enable ? '点击停用' : '点击启用') + '"></button></td>' +
      '<td><div class="name">' + esc(t.name || (t.domains || [])[0] || '未命名') + '</div><div class="sub">签发 ' + num(t.issueCount, 0) + ' 次</div></td>' +
      '<td>' + esc((t.domains || []).join(', ')) + '</td>' +
      '<td>' + esc(t.caProvider) + '</td>' +
      '<td class="sub">' + esc(t.expireTime || '-') + '</td>' +
      '<td>' + certDaysCell(t.expireTime) + '</td>' +
      '<td' + (t.lastError ? ' title="最近一次续期失败：' + esc(t.lastError) + '"' : '') + '>' +
      statusTag(t.status || (t.enable ? '待申请' : '已禁用')) + '</td>' +
      '<td><div class="actions">' +
      '<button class="btn btn-sm btn-ghost" data-action="cert-apply" onclick="applyCert(\'' + t.key + '\')" title="立即申请/续期"><svg class="icon icon-sm"><use href="#i-bolt"/></svg></button>' +
      '<button class="btn btn-sm btn-ghost" data-action="cert-edit" onclick="editCert(\'' + t.key + '\')" title="编辑"><svg class="icon icon-sm"><use href="#i-edit"/></svg></button>' +
      '<button class="btn btn-sm btn-danger" data-action="cert-del" onclick="delCert(\'' + t.key + '\')" title="删除"><svg class="icon icon-sm"><use href="#i-trash"/></svg></button>' +
      '</div></td></tr>').join('') + '</tbody></table></div>';
}

async function editCert(key) {
  const meta = await getMeta();
  const t = key ? (STATE.cert || []).find(x => x.key === key) : null;
  const cas = (meta && meta.caProviders) || [];
  const cur = t || {
    name: '', domains: [], email: '', caProvider: 'letsencrypt', keyType: 'ec256',
    challenge: 'http01', httpPort: 80, renewBefore: 20, enable: true
  };
  const html = '' +
    '<div class="row"><div class="field"><label>任务名称</label><input id="c_name" value="' + esc(cur.name) + '" placeholder="例如：家庭域名证书"></div>' +
    '<div class="field"><label>启用自动续期</label><label class="switch"><input type="checkbox" id="c_enable" ' + (cur.enable ? 'checked' : '') + '><span class="track"></span><span>到期前自动续期</span></label></div></div>' +
    '<div class="field"><label>域名（多个用逗号分隔）</label><input id="c_domains" value="' + esc((cur.domains || []).join(',')) + '" placeholder="example.com,www.example.com"><div class="tip">所有域名都必须能解析到本机，且 80 端口可从公网访问</div></div>' +
    '<div class="row"><div class="field"><label>邮箱</label><input id="c_email" value="' + esc(cur.email) + '" placeholder="用于证书到期提醒"></div>' +
    '<div class="field"><label>颁发机构</label><select id="c_ca">' +
    cas.map(p => '<option value="' + p.value + '" ' + (cur.caProvider === p.value ? 'selected' : '') + '>' + esc(p.label) + '</option>').join('') +
    '</select></div></div>' +
    '<div class="row"><div class="field"><label>密钥类型</label><select id="c_keytype">' +
    '<option value="ec256" ' + (cur.keyType === 'ec256' ? 'selected' : '') + '>EC P-256</option>' +
    '<option value="rsa2048" ' + (cur.keyType === 'rsa2048' ? 'selected' : '') + '>RSA 2048</option></select></div>' +
    '<div class="field"><label>HTTP-01 端口</label><input id="c_port" type="number" value="' + num(cur.httpPort, 80) + '"><div class="tip">ACME 校验固定使用 80 端口，修改需具备端口转发</div></div></div>' +
    '<div class="field"><label>提前续期天数</label><input id="c_renew" type="number" value="' + num(cur.renewBefore, 20) + '"></div>';

  openModal(key ? '编辑证书任务' : '新增证书任务',
    guideTip('填好域名和邮箱保存，再点列表里的闪电按钮即可申请。申请时域名需已解析到本机，且 80 端口可从公网访问。') + html,
    async () => {
    const domains = $('c_domains').value.split(/[,\s]+/).map(s => s.trim()).filter(Boolean);
    if (!domains.length) { toast('请至少填写一个域名', 'err'); return; }
    const body = {
      name: $('c_name').value.trim(),
      enable: $('c_enable').checked,
      domains: domains,
      email: $('c_email').value.trim(),
      caProvider: $('c_ca').value,
      keyType: $('c_keytype').value,
      challenge: 'http01',
      httpPort: parseInt($('c_port').value, 10) || 80,
      renewBefore: parseInt($('c_renew').value, 10) || 20
    };
    try {
      if (key) await api('/api/cert/' + key, { method: 'PUT', body: body });
      else await api('/api/cert', { method: 'POST', body: body });
      closeModal();
      toast('已保存', 'ok');
      loadState();
    } catch (e) { toast(e.message, 'err'); }
  });
}

async function delCert(key) {
  confirmDialog('删除证书任务', '已签发的证书文件会一并清理，正在使用它的反向代理将无法再用 HTTPS。', async () => {
    try { await api('/api/cert/' + key, { method: 'DELETE' }); toast('已删除'); loadState(); }
    catch (e) { toast(e.message, 'err'); }
  });
}

async function applyCert(key) {
  try { await api('/api/cert/' + key + '/apply', { method: 'POST' }); toast('已在后台开始申请，请查看日志', 'info'); setTimeout(loadState, 2000); }
  catch (e) { toast(e.message, 'err'); }
}

/* ================= 反向代理 ================= */

function renderProxy() {
  return '<div class="card"><div class="card-head"><h2>反向代理规则</h2>' +
    '<div class="desc">按域名或路径前缀转发到内网服务，支持多后端轮询</div>' +
    '<div class="right">' +
    '<label class="search-box"><svg class="icon icon-sm"><use href="#i-search"/></svg>' +
    '<input id="q_proxy" placeholder="搜索名称 / 域名 / 端口" value="' + esc(FILTER.proxy) + '" oninput="setFilter(\'proxy\', this.value)"></label>' +
    '<button class="btn btn-primary btn-sm" data-action="proxy-add" onclick="editProxy()"><svg class="icon icon-sm"><use href="#i-plus"/></svg> 新增规则</button>' +
    '</div></div><div id="list-proxy">' + proxyTableHtml() + '</div></div>';
}

function proxyTableHtml() {
  const q = FILTER.proxy.trim().toLowerCase();
  const list = (STATE.proxy || []).filter(t => !q ||
    [t.name, t.domain, String(t.listenPort), (t.backends || []).join(' ')].join(' ').toLowerCase().indexOf(q) >= 0);
  if (!list.length) return q
    ? emptyBox('没有匹配的规则', '换个关键词试试，或者清空搜索框。')
    : emptyBox('还没有反向代理规则', '一条规则就把一个内网地址映射成一个网址，例如 https://nas.你的域名.com。', '新增第一条规则', 'editProxy()');
  return '<div class="table-wrap"><table><thead><tr><th>启用</th><th>名称</th><th>监听端口</th><th>匹配域名</th><th>后端地址</th><th>请求数</th><th>状态</th><th style="text-align:right">操作</th></tr></thead><tbody>' +
    list.map(t => '<tr>' +
      '<td><button class="toggle ' + (t.enable ? 'on' : '') + '" data-action="proxy-toggle" data-key="' + esc(t.key) + '" onclick="toggleTask(\'proxy\',\'' + t.key + '\')" title="' + (t.enable ? '点击停用' : '点击启用') + '"></button></td>' +
      '<td><div class="name">' + esc(t.name || '未命名') + '</div><div class="sub">' + (t.enableTLS ? 'HTTPS' : 'HTTP') + (t.enableGzip ? ' · gzip' : '') + '</div></td>' +
      '<td>' + num(t.listenPort) + '</td>' +
      '<td>' + esc(t.domain || '任意域名') + ' ' + esc(t.pathPrefix || '') + '</td>' +
      '<td class="sub">' + esc((t.backends || []).join(', ')) + '</td>' +
      '<td class="sub">' + num(t.requestCount).toLocaleString() + '</td>' +
      '<td>' + statusTag(t.enable ? (t.status || '运行中') : '已禁用') + '</td>' +
      '<td><div class="actions">' +
      '<button class="btn btn-sm btn-ghost" data-action="proxy-edit" onclick="editProxy(\'' + t.key + '\')" title="编辑"><svg class="icon icon-sm"><use href="#i-edit"/></svg></button>' +
      '<button class="btn btn-sm btn-danger" data-action="proxy-del" onclick="delProxy(\'' + t.key + '\')" title="删除"><svg class="icon icon-sm"><use href="#i-trash"/></svg></button>' +
      '</div></td></tr>').join('') + '</tbody></table></div>';
}

async function editProxy(key) {
  const certs = (STATE.cert || []).filter(c => c.certPath);
  const t = key ? (STATE.proxy || []).find(x => x.key === key) : null;
  const cur = t || {
    name: '', listenPort: 8080, domain: '', pathPrefix: '', backends: [''],
    enableTLS: false, certFile: '', keyFile: '', enableGzip: true, enableXReal: true, enable: true
  };
  const html = '' +
    '<div class="row"><div class="field"><label>规则名称</label><input id="p_name" value="' + esc(cur.name) + '" placeholder="例如：内网 NAS"></div>' +
    '<div class="field"><label>启用</label><label class="switch"><input type="checkbox" id="p_enable" ' + (cur.enable ? 'checked' : '') + '><span class="track"></span><span>立即生效</span></label></div></div>' +
    '<div class="row"><div class="field"><label>监听端口</label><input id="p_port" type="number" value="' + num(cur.listenPort, 8080) + '"></div>' +
    '<div class="field"><label>匹配域名</label><input id="p_domain" value="' + esc(cur.domain) + '" placeholder="留空匹配所有"></div></div>' +
    '<div class="field"><label>路径前缀</label><input id="p_prefix" value="' + esc(cur.pathPrefix) + '" placeholder="留空匹配所有路径，例如 /api"></div>' +
    '<div class="field"><label>后端地址（每行一个）</label><textarea id="p_backends" placeholder="http://192.168.1.10:5000">' + esc((cur.backends || []).join('\n')) + '</textarea><div class="tip">多个后端按顺序轮询；可写 http://1.2.3.4:8080 或 192.168.1.10:8080</div></div>' +
    '<div class="row">' +
    '<div class="field"><label>启用 HTTPS</label><label class="switch"><input type="checkbox" id="p_tls" ' + (cur.enableTLS ? 'checked' : '') + '><span class="track"></span><span>使用证书对外提供 HTTPS</span></label></div>' +
    '<div class="field"><label>选择已签发证书</label><select id="p_certpick"><option value="">手动填写路径</option>' +
    certs.map(c => '<option value="' + esc(c.certPath) + '|' + esc(c.keyPath) + '">' + esc((c.domains || []).join(', ')) + '</option>').join('') +
    '</select></div></div>' +
    '<div class="row"><div class="field"><label>证书文件路径</label><input id="p_cert" value="' + esc(cur.certFile) + '"></div>' +
    '<div class="field"><label>私钥文件路径</label><input id="p_key" value="' + esc(cur.keyFile) + '"></div></div>' +
    '<div class="row"><div class="field"><label>启用 gzip</label><label class="switch"><input type="checkbox" id="p_gzip" ' + (cur.enableGzip ? 'checked' : '') + '><span class="track"></span><span>压缩响应</span></label></div>' +
    '<div class="field"><label>透传真实 IP</label><label class="switch"><input type="checkbox" id="p_xreal" ' + (cur.enableXReal ? 'checked' : '') + '><span class="track"></span><span>X-Real-IP</span></label></div></div>';

  openModal(key ? '编辑反代规则' : '新增反代规则',
    guideTip('左边填你想对外使用的网址，右边填内网设备地址（例如 http://192.168.1.10:5000），保存即生效。') + html,
    async () => {
    const backends = $('p_backends').value.split('\n').map(s => s.trim()).filter(Boolean);
    if (!backends.length) { toast('至少需要一个后端地址', 'err'); return; }
    const body = {
      name: $('p_name').value.trim(),
      enable: $('p_enable').checked,
      listenPort: parseInt($('p_port').value, 10) || 0,
      domain: $('p_domain').value.trim(),
      pathPrefix: $('p_prefix').value.trim(),
      backends: backends,
      enableTLS: $('p_tls').checked,
      certFile: $('p_cert').value.trim(),
      keyFile: $('p_key').value.trim(),
      enableGzip: $('p_gzip').checked,
      enableXReal: $('p_xreal').checked
    };
    try {
      if (key) await api('/api/proxy/' + key, { method: 'PUT', body: body });
      else await api('/api/proxy', { method: 'POST', body: body });
      closeModal();
      toast('已保存', 'ok');
      loadState();
    } catch (e) { toast(e.message, 'err'); }
  });

  $('p_certpick').addEventListener('change', () => {
    const v = $('p_certpick').value;
    if (!v) return;
    const parts = v.split('|');
    $('p_cert').value = parts[0] || '';
    $('p_key').value = parts[1] || '';
  });
}

async function delProxy(key) {
  confirmDialog('删除反向代理规则', '删除后这个网址会立刻停止转发，再次访问会提示连接失败。', async () => {
    try { await api('/api/proxy/' + key, { method: 'DELETE' }); toast('已删除'); loadState(); }
    catch (e) { toast(e.message, 'err'); }
  });
}

/* ---------------- 搜索与启停 ---------------- */

function setFilter(module, value) {
  FILTER[module] = value;
  const box = $('list-' + module);
  if (!box) return;
  box.innerHTML = module === 'ddns' ? ddnsTableHtml() : (module === 'cert' ? certTableHtml() : proxyTableHtml());
}

async function toggleTask(module, key) {
  if (DEMO) {
    const list = STATE[module] || [];
    const t = list.find(x => x.key === key);
    if (t) t.enable = !t.enable;
    toast(t && t.enable ? '已启用' : '已停用', 'ok');
    render();
    return;
  }
  try {
    const r = await api('/api/' + module + '/' + key + '/toggle', { method: 'POST' });
    toast(r && r.enable ? '已启用' : '已停用', 'ok');
    await loadState();
  } catch (e) { toast(e.message, 'err'); }
}

/* ---------------- 日志 ---------------- */

function renderLogs() {
  return '<div class="card"><div class="card-head"><h2>运行日志</h2><div class="desc">实时输出（最多保留最近 2000 条）</div>' +
    '<div class="right"><button class="btn btn-sm btn-ghost" onclick="clearLogView()">清屏</button></div></div>' +
    '<div class="log-box" id="logBox"></div></div>';
}

function clearLogView() { const b = $('logBox'); if (b) b.innerHTML = ''; }

function appendLogs(list) {
  const box = $('logBox');
  if (!box || !list || !list.length) return;
  list.forEach(e => {
    logSince = Math.max(logSince, e.seq);
    const line = document.createElement('div');
    line.className = 'log-line';
    line.innerHTML = '<span class="log-time">' + esc(e.time) + '</span>' +
      '<span class="log-lv ' + esc(e.level) + '">' + esc(e.level) + '</span>' +
      '<span class="log-mod">[' + esc(e.module) + ']</span>' +
      '<span class="log-msg">' + esc(e.message) + '</span>';
    box.appendChild(line);
  });
  box.scrollTop = box.scrollHeight;
  while (box.children.length > 800) box.removeChild(box.firstChild);
}

function startLogStream() {
  if (logStream) return;
  api('/api/logs').then(list => appendLogs(list)).catch(() => {});
  logStream = new EventSource('/api/logs/stream?token=' + encodeURIComponent(TOKEN));
  logStream.onmessage = ev => {
    try { appendLogs([JSON.parse(ev.data)]); } catch (e) { /* 忽略解析错误 */ }
  };
  logStream.onerror = () => { /* 由浏览器自动重连 */ };
}

function stopLogStream() {
  if (logStream) { logStream.close(); logStream = null; }
}

/* ---------------- 设置 ---------------- */

function renderSettings() {
  const b = STATE.base || {};
  return '<div class="card"><div class="card-head"><h2>基础设置</h2><div class="desc">修改端口后需重启服务生效</div></div>' +
    '<div class="row"><div class="field"><label>管理端口</label><input id="s_port" type="number" value="' + num(b.adminWebListenPort, 5200) + '"></div>' +
    '<div class="field"><label>登录账号</label><input id="s_user" value="' + esc(b.adminAccount || '') + '"></div></div>' +
    '<div class="row"><div class="field"><label>登录密码</label><input id="s_pass" type="text" value="' + esc(b.adminPassword || '') + '"></div>' +
    '<div class="field"><label>会话超时(秒)</label><input id="s_timeout" type="number" value="' + num(b.sessionTimeout, 7200) + '"></div></div>' +
    '<div class="field"><label>允许外网访问管理页面</label><label class="switch"><input type="checkbox" id="s_internet" ' + (b.allowInternetAccess ? 'checked' : '') + '><span class="track"></span><span>关闭时仅允许局域网访问</span></label></div>' +
    '<div class="field"><label>启用 HTTPS 管理页面</label><label class="switch"><input type="checkbox" id="s_tls" ' + (b.enableTLS ? 'checked' : '') + '><span class="track"></span><span>需填写证书与私钥路径</span></label></div>' +
    '<div class="row"><div class="field"><label>证书文件路径</label><input id="s_cert" value="' + esc(b.tlsCertFile || '') + '"></div>' +
    '<div class="field"><label>私钥文件路径</label><input id="s_key" value="' + esc(b.tlsKeyFile || '') + '"></div></div>' +
    '<div class="field"><label>日志级别</label><select id="s_level">' +
    ['debug', 'info', 'warn', 'error'].map(l => '<option value="' + l + '" ' + ((b.logLevel || 'info') === l ? 'selected' : '') + '>' + l + '</option>').join('') +
    '</select></div>' +
    '<div style="display:flex;gap:8px;flex-wrap:wrap;padding-top:6px">' +
    '<button class="btn btn-primary" onclick="saveSettings()"><svg class="icon icon-sm"><use href="#i-check"/></svg> 保存设置</button>' +
    '<button class="btn" onclick="exportConfig()"><svg class="icon icon-sm"><use href="#i-download"/></svg> 导出配置</button>' +
    '</div>' +
    migrateCard() +
    '</div>' +
    '<div class="card"><div class="card-head"><h2>环境信息</h2><div class="desc">单文件二进制运行，无需安装 Go 环境</div></div>' +
    '<table><tbody>' +
    '<tr><td class="sub" style="width:32%">程序版本</td><td>' + esc(STATE.version || '-') + '</td></tr>' +
    '<tr><td class="sub">配置目录</td><td>' + esc(STATE.confDir || '-') + '</td></tr>' +
    '<tr><td class="sub">主机名</td><td>' + esc((STATE.system || {}).hostname || '-') + '</td></tr>' +
    '<tr><td class="sub">运行时长</td><td>' + esc((STATE.system || {}).uptime || '-') + '</td></tr>' +
    '<tr><td class="sub">静态资源</td><td>已内嵌（前端无需单独部署）</td></tr>' +
    '<tr><td class="sub">运行依赖</td><td>无（不依赖 Go / Node 运行时）</td></tr>' +
    '</tbody></table></div>';
}

/* ---------------- 从 Lucky 迁移 ---------------- */

function migrateCard() {
  return '<div class="card" style="margin:18px 0 0"><div class="card-head">' +
    '<h2>从 Lucky 迁移</h2>' +
    '<div class="desc">直接读取 Lucky 的 lucky_*.lkcf 配置（自动解密），导入动态域名与证书任务</div></div>' +
    '<div class="field"><label>Lucky 配置目录</label>' +
    '<input id="m_dir" value="' + esc(STATE.confDir || '/etc/lucky/luckyconf') + '" placeholder="/etc/lucky/luckyconf">' +
    '<div class="tip">目录内需含 lucky_ddns.lkcf / lucky_ssl.lkcf。若配置使用 zstd 压缩，需先在设备上安装 zstd</div></div>' +
    '<div class="field"><label class="switch"><input type="checkbox" id="m_keep"><span class="track"></span>' +
    '<span>导入后保持 Lucky 中的启用状态（默认停用，避免与仍在运行的 Lucky 冲突）</span></label></div>' +
    '<div style="display:flex;gap:8px;flex-wrap:wrap">' +
    '<button class="btn" onclick="doImport(false)"><svg class="icon icon-sm"><use href="#i-check"/></svg> 预览</button>' +
    '<button class="btn btn-primary" onclick="doImport(true)"><svg class="icon icon-sm"><use href="#i-plus"/></svg> 导入</button>' +
    '</div>' +
    '<div id="m_result" class="sub" style="margin-top:12px;white-space:pre-line"></div></div>';
}

async function doImport(apply) {
  const dir = $('m_dir').value.trim();
  const keep = $('m_keep').checked;
  $('m_result').textContent = '处理中...';
  try {
    const data = await api('/api/import/lucky', { method: 'POST', body: { dir: dir, apply: apply, keepEnabled: keep } });
    const r = data.result || {};
    const lines = [];
    lines.push('读取文件: ' + (data.files || []).join(', '));
    (r.reports || []).forEach(rep => {
      lines.push(rep.title + ': 发现 ' + rep.found + ' 条，可导入 ' + rep.imported + ' 条');
      (rep.warnings || []).slice(0, 8).forEach(w => lines.push('  ! ' + w));
    });
    lines.push(apply ? ('已导入 DDNS ' + r.ddns + ' 条、证书 ' + r.cert + ' 张') : '（预览模式，未写入）');
    $('m_result').textContent = lines.join('\n');
    toast(apply ? '导入完成' : '预览完成', 'ok');
    if (apply) loadState();
  } catch (e) {
    $('m_result').textContent = '失败: ' + e.message;
    toast(e.message, 'err');
  }
}

async function saveSettings() {
  const body = {
    adminWebListenPort: parseInt($('s_port').value, 10) || 5200,
    adminAccount: $('s_user').value.trim(),
    adminPassword: $('s_pass').value,
    allowInternetAccess: $('s_internet').checked,
    enableTLS: $('s_tls').checked,
    tlsCertFile: $('s_cert').value.trim(),
    tlsKeyFile: $('s_key').value.trim(),
    sessionTimeout: parseInt($('s_timeout').value, 10) || 7200,
    logLevel: $('s_level').value,
    autoStart: true
  };
  try {
    await api('/api/base', { method: 'PUT', body: body });
    toast('设置已保存', 'ok');
    loadState();
  } catch (e) { toast(e.message, 'err'); }
}

async function exportConfig() {
  if (DEMO) { toast('演示模式不支持导出', 'info'); return; }
  try {
    const res = await fetch('/api/export', { headers: { 'X-Token': TOKEN } });
    if (!res.ok) throw new Error('导出失败(' + res.status + ')');
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'porch-backup-' + new Date().toISOString().slice(0, 10) + '.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 3000);
    toast('配置已导出', 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

/* ---------------- 显示模式 ---------------- */

function toggleDensity() {
  const on = document.body.classList.toggle('dense');
  localStorage.setItem('porch_dense', on ? '1' : '0');
  const btn = $('densityBtn');
  if (btn) btn.textContent = on ? '舒适' : '紧凑';
  setTimeout(repaintCharts, 60);
}

function applyDensity() {
  const on = localStorage.getItem('porch_dense') === '1' || localStorage.getItem('lucky_dense') === '1';
  if (on) document.body.classList.add('dense');
  const btn = $('densityBtn');
  if (btn) btn.textContent = on ? '舒适' : '紧凑';
}

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    const el = document.documentElement;
    if (el.requestFullscreen) el.requestFullscreen().catch(() => {});
  } else if (document.exitFullscreen) {
    document.exitFullscreen().catch(() => {});
  }
}

/* ---------------- 弹窗 ---------------- */

let modalHandler = null;

// 表单顶部的一句话说明：告诉用户"这一步在做什么、要注意什么"
function guideTip(text) {
  return '<div class="form-tip"><svg class="icon icon-sm"><use href="#i-info"/></svg>' +
    '<span>' + esc(text) + '</span></div>';
}

function openModal(title, html, onSave, saveText) {
  $('modalTitle').textContent = title;
  $('modalBody').innerHTML = html;
  modalHandler = onSave;
  $('modalSave').textContent = saveText || '保存';
  $('mask').classList.add('show');
}

// 统一的二次确认：比浏览器原生 confirm 好看，也不会被内嵌页面屏蔽
function confirmDialog(title, msg, onOk, okText) {
  openModal(title,
    '<div class="confirm"><svg class="icon icon-lg" style="color:var(--danger)"><use href="#i-alert"/></svg>' +
    '<div class="confirm-msg">' + esc(msg) + '</div></div>',
    async () => { await onOk(); closeModal(); },
    okText || '确认删除');
  $('modalSave').classList.add('btn-danger');
}

function closeModal() {
  $('mask').classList.remove('show');
  modalHandler = null;
  $('modalSave').classList.remove('btn-danger');
}

$('modalSave').addEventListener('click', () => { if (modalHandler) modalHandler(); });

async function getMeta() {
  if (STATE.meta) return STATE.meta;
  if (DEMO) { STATE.meta = {}; return STATE.meta; }
  try { STATE.meta = await api('/api/meta'); } catch (e) { STATE.meta = {}; }
  return STATE.meta;
}

/* ---------------- 启动 ---------------- */

document.addEventListener('keydown', e => {
  if (e.key === 'Enter' && !$('login').classList.contains('hidden')) doLogin();
  if (e.key === 'Escape') closeModal();
});

applyDensity();

if (TOKEN || DEMO) {
  showApp();
  syncHeader();
  loadState();
  if (PAGE === 'dashboard') startMetrics();
} else {
  showLogin();
}

setInterval(() => {
  if (!TOKEN || DEMO || PAGE === 'logs') return;
  // 用户正在输入时跳过自动刷新，避免打断搜索框与表单
  const el = document.activeElement;
  if (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) return;
  loadState();
}, 15000);
