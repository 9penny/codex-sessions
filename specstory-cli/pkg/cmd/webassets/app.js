'use strict';
const $ = id => document.getElementById(id);
const el = (tag, cls, text) => { const node = document.createElement(tag); if (cls) node.className = cls; if (text !== undefined) node.textContent = text; return node; };
const saved = (key, fallback) => { try { return JSON.parse(localStorage.getItem(key)) ?? fallback; } catch { return fallback; } };
const save = (key, value) => { try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* Storage is optional. */ } };
let token = new URLSearchParams(location.hash.slice(1)).get('token');
try { if (token) sessionStorage.setItem('csessions-token', token); else token = sessionStorage.getItem('csessions-token'); } catch { /* The fragment still works in private browsers. */ }
if (location.hash) history.replaceState(null, '', location.pathname);
let state, items = [], active, detail, directory = '', view = 'all', offset = 0, total = 0;
let listSeq = 0, detailSeq = 0, toastTimer, searchTimer, lastScan = '', polling = false;
let expanded = new Set(saved('csessions-expanded', []));
let marks = [], currentMark = -1;
document.documentElement.dataset.theme = saved('csessions-theme', 'light');
async function api(path, options = {}) {
 const response = await fetch('/api/' + path, { ...options, headers: { 'Authorization': 'Bearer ' + (token || ''), 'Content-Type': 'application/json', ...options.headers } });
 const data = await response.json();
 if (!response.ok) throw new Error(data.error || '请求失败');
 return data;
}
function toast(message) { $('toast').textContent = message; $('toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => $('toast').hidden = true, 3500); }
function error(message) { $('connection').textContent = message; $('connection').hidden = false; }
function pretty(path) { if (!path) return '未记录项目目录'; return state && (path === state.home || path.startsWith(state.home + '/')) ? '~' + path.slice(state.home.length) : path; }
function basename(path) { return path.split('/').filter(Boolean).pop() || '/'; }
function date(value, full = false) { const d = new Date(value); return Number.isNaN(+d) ? '时间未知' : new Intl.DateTimeFormat('zh-CN', full ? { dateStyle: 'medium', timeStyle: 'short' } : { month: 'short', day: 'numeric' }).format(d); }
function inDirectory(path, parent) { return path === parent || path.startsWith(parent === '/' ? '/' : parent + '/'); }
async function copy(text) {
 if (!text) return;
 try { await navigator.clipboard.writeText(text); toast('已复制，粘贴到当前机器的 WSL / SSH 终端即可'); }
 catch { $('copy-text').value = text; $('copy-dialog').showModal(); $('copy-text').select(); }
}
function chooseScope(path, nextView = 'all') {
 directory = path; view = nextView; offset = 0;
 save('csessions-scope-' + state.machine, { directory, view });
 renderTree(); updateScope(); showListLoading(); loadList();
}
function updateScope() {
 document.querySelectorAll('[data-view]').forEach(n => n.classList.toggle('active', !directory && n.dataset.view === view));
 $('scope-title').textContent = directory ? (directory === state.home ? '~' : basename(directory)) : ({ all: '全部项目', favorites: '我的收藏', hidden: '已隐藏' }[view]);
 $('scope-path').textContent = directory ? pretty(directory) + ' · 包含所有子目录' : (view === 'hidden' ? '随时恢复，不删除原始会话' : '当前机器的所有项目');
}
function renderTree() {
 const container = $('project-tree'); container.replaceChildren();
 if (!state.projects.length) { container.append(el('p', 'muted empty-small', state.scan.running ? '正在扫描，发现的项目会陆续出现…' : '尚未发现会话。可添加其他扫描位置。')); return; }
 const filter = $('project-filter').value.toLowerCase().trim();
 const projects = state.projects.filter(p => p.path === state.home || inDirectory(p.path, state.home) || !inDirectory(state.home, p.path));
 const map = new Map(projects.map(p => [p.path, p]));
 const children = new Map();
 for (const p of projects) { const parent = p.path.slice(0, p.path.lastIndexOf('/')) || '/'; const key = map.has(parent) && parent !== p.path ? parent : ''; if (!children.has(key)) children.set(key, []); children.get(key).push(p); }
 const sort = $('project-sort').value;
 for (const group of children.values()) group.sort((a, b) => sort === 'recent' ? b.updated.localeCompare(a.updated) || a.path.localeCompare(b.path) : a.path.localeCompare(b.path));
 const append = (p, depth) => {
  if (filter && !p.path.toLowerCase().includes(filter) && !projects.some(c => inDirectory(c.path, p.path) && c.path.toLowerCase().includes(filter))) return;
  const kids = children.get(p.path) || []; const open = filter || expanded.has(p.path);
  const line = el('div', 'tree-line');
  // Use a CSS custom property through indentation elements, keeping CSP free of inline styles.
  for (let i = 0; i < Math.min(depth, 8); i++) line.append(el('span', 'tree-indent'));
  const toggle = el('button', 'tree-toggle', kids.length ? (open ? '▾' : '▸') : '·');
  toggle.setAttribute('aria-label', (open ? '折叠 ' : '展开 ') + pretty(p.path)); toggle.disabled = !kids.length;
  if (kids.length) toggle.setAttribute('aria-expanded', String(!!open));
  toggle.onclick = () => { if (open) expanded.delete(p.path); else expanded.add(p.path); save('csessions-expanded', [...expanded]); renderTree(); };
  const row = el('button', 'tree-row' + (directory === p.path ? ' active' : '') + (p.missing ? ' missing' : ''));
  row.title = pretty(p.path) + (p.missing ? ' · 原目录不存在' : '');
  row.append(el('span', 'folder-icon', p.missing ? '◌' : '▱'), el('span', 'tree-name', p.path === state.home ? '~ / 主目录' : basename(p.path)), el('span', 'count', p.count));
  row.onclick = () => chooseScope(p.path);
  line.append(toggle, row); container.append(line);
  if (open) kids.forEach(c => append(c, depth + 1));
 };
 (children.get('') || []).forEach(p => append(p, 0));
 if (!container.childNodes.length) container.append(el('p', 'empty-small muted', '没有匹配的项目目录'));
}
function renderCards() {
 const list = $('session-list'); const scroll = list.scrollTop; list.replaceChildren();
 $('list-count').textContent = total;
 if (!items.length) list.append(el('p', 'empty-small muted', $('search').value ? '没有匹配的会话，试试其他关键词。' : view === 'hidden' ? '没有隐藏的会话。' : view === 'favorites' ? '还没有收藏。打开会话后点击「收藏」。' : state?.scan.running ? '正在扫描，发现的会话会陆续出现…' : '这个目录暂时没有会话。'));
 for (const s of items) {
  const card = el('button', 'session-card' + (active === s.id ? ' selected' : '')); card.dataset.id = s.id;
  const meta = el('div', 'card-meta'); meta.append(el('span', 'folder-icon', '▱'), el('span', 'card-project', basename(s.directory)), el('time', '', date(s.updated)));
  const title = el('div', 'card-title', s.title);
  card.append(meta, title);
  if (s.snippet) {
   const summary = el('div', 'card-summary'); let highlight = false;
   for (const part of s.snippet.split(/([\x02\x03])/)) { if (part === '\x02') highlight = true; else if (part === '\x03') highlight = false; else summary.append(el(highlight ? 'mark' : 'span', '', part)); }
   card.append(summary);
  } else if (s.summary) card.append(el('div', 'card-summary', s.summary));
  const footer = el('div', 'card-footer'); footer.append(el('span', '', s.turns + ' 轮对话'));
  if (s.aiTitle || s.summary) footer.append(el('span', '', '✦ AI 摘要'));
  if (s.background) footer.append(el('span', '', '后台'));
  if (s.favorite) footer.append(el('span', 'card-star', '★ 已收藏'));
  card.append(footer); card.onclick = () => openSession(s.id); list.append(card);
 }
 list.scrollTop = scroll;
 $('pagination').hidden = total <= 80;
 $('previous').disabled = offset === 0; $('next').disabled = offset + 80 >= total;
 $('page-info').textContent = (offset + 1) + '–' + Math.min(offset + 80, total) + ' / ' + total;
}
function showListLoading() {
 ++listSeq;
 $('session-list').replaceChildren(el('p', 'empty-small muted', '正在加载会话…'));
}
async function loadList() {
 const seq = ++listSeq;
 const params = new URLSearchParams({ directory, view, offset, q: $('search').value, background: $('background').checked });
 try { const data = await api('sessions?' + params); if (seq !== listSeq) return; items = data.items; total = data.total; offset = data.offset; renderCards();
  const selected = items.find(s => s.id === active);
  if (detail && selected && (selected.updated !== detail.session.updated || selected.title !== detail.session.title || selected.summary !== detail.session.summary)) openSession(active, true); }
 catch (e) { if (seq === listSeq) toast(e.message); }
}
function renderDetailHeader() {
 const s = detail.session;
 $('reader-title').textContent = s.title; $('reader-path').textContent = pretty(s.directory);
 $('reader-date').textContent = date(s.updated, true) + ' · ' + s.turns + ' 轮对话';
 $('reader-kind').textContent = s.background ? '后台会话' : 'Codex CLI';
 $('favorite').textContent = s.favorite ? '★ 已收藏' : '☆ 收藏'; $('hide').textContent = s.hidden ? '恢复显示' : '隐藏';
 $('resume').disabled = !detail.resume; $('enrich').disabled = !detail.enrich; $('enrich-preview').disabled = !detail.enrichPreview;
 $('resume-note').textContent = detail.missingDirectory ? '原项目目录不存在，恢复与生成命令暂不可用。历史内容仍可阅读。' : '粘贴到这台机器的 WSL / SSH 终端，自动进入原项目目录。';
 const summary = $('ai-summary'); summary.replaceChildren(); summary.hidden = !s.summary && !s.aiTitle;
 if (!summary.hidden) { summary.append(el('span', 'ai-label', '✦ AI 生成 · 仅供参考')); if (s.aiTitle && s.aiTitle !== s.title) summary.append(el('p', '', s.aiTitle)); if (s.summary) summary.append(el('div', '', s.summary)); if (s.tags?.length) summary.append(el('p', 'muted', s.tags.join(' · '))); }
}
async function openSession(id, keepPosition = false) {
 const position = $('reader-scroll').scrollTop;
 const atEnd = $('reader-scroll').scrollHeight - position - $('reader-scroll').clientHeight < 80;
 const seq = ++detailSeq; active = id; detail = undefined; renderCards();
 if (!keepPosition) { $('reader-content').hidden = true; $('reader-empty').hidden = false; }
 $('reader-empty').querySelector('h2').textContent = '正在读取会话…';
 try {
  const data = await api('session?id=' + encodeURIComponent(id)); if (seq !== detailSeq) return; detail = data;
  renderDetailHeader(); $('reader-content').hidden = false; $('reader-empty').hidden = true;
  // HTML is rendered by Goldmark with unsafe HTML/URLs disabled on the authenticated backend.
  $('transcript').innerHTML = detail.html;
  $('transcript').querySelectorAll('a').forEach(a => { a.target = '_blank'; a.rel = 'noreferrer noopener'; });
  $('transcript').querySelectorAll('pre').forEach(pre => { const code = pre.querySelector('code'); const button = el('button', 'copy-code', '复制代码'); button.onclick = async () => { const text = code ? code.textContent : pre.textContent; try { await navigator.clipboard.writeText(text); toast('代码已复制'); } catch { $('copy-text').value = text; $('copy-dialog').showModal(); $('copy-text').select(); } }; pre.append(button); });
  $('reader-scroll').scrollTop = 0;
  if (!keepPosition) $('find').value = $('search').value.replaceAll('"', '').trim(); highlightFind();
  if (keepPosition) $('reader-scroll').scrollTop = atEnd ? $('reader-scroll').scrollHeight : position;
 } catch (e) { if (seq !== detailSeq) return; $('reader-empty').querySelector('h2').textContent = '暂时无法打开会话'; $('reader-empty').querySelector('p').textContent = e.message; }
}
function highlightFind() {
 const article = $('transcript');
 article.querySelectorAll('mark').forEach(mark => mark.replaceWith(document.createTextNode(mark.textContent))); article.normalize();
 marks = []; currentMark = -1;
 const query = $('find').value.trim().toLowerCase();
 if (query) {
  const walker = document.createTreeWalker(article, NodeFilter.SHOW_TEXT); const texts = [];
  while (walker.nextNode()) if (!walker.currentNode.parentElement.closest('button')) texts.push(walker.currentNode);
  for (const text of texts) {
   const value = text.textContent; const lower = value.toLowerCase(); let start = 0, index = lower.indexOf(query);
   if (index < 0) continue;
   const fragment = document.createDocumentFragment();
   while (index >= 0) { fragment.append(document.createTextNode(value.slice(start, index))); const mark = el('mark', '', value.slice(index, index + query.length)); fragment.append(mark); marks.push(mark); start = index + query.length; index = lower.indexOf(query, start); }
   fragment.append(document.createTextNode(value.slice(start))); text.replaceWith(fragment);
  }
 }
 $('find-count').textContent = query ? marks.length + ' 处' : '';
 if (marks.length) nextMatch();
}
function nextMatch() { if (!marks.length) return; marks[currentMark]?.classList.remove('current'); currentMark = (currentMark + 1) % marks.length; marks[currentMark].classList.add('current'); marks[currentMark].scrollIntoView({ block: 'center' }); $('find-count').textContent = (currentMark + 1) + ' / ' + marks.length; }
async function annotate(changes) {
 if (!detail) return false;
 const id = detail.session.id, s = detail.session;
 try {
  await api('annotation?id=' + encodeURIComponent(id), { method: 'PATCH', body: JSON.stringify(changes) });
  // The user may have opened another session while a save was in flight.
  if (detail?.session.id === id) { Object.assign(s, { manualTitle: changes.title ?? s.manualTitle, favorite: changes.favorite ?? s.favorite, hidden: changes.hidden ?? s.hidden }); if (changes.title !== undefined) { const updated = await api('session?id=' + encodeURIComponent(id)); if (detail?.session.id === id) detail = updated; } if (detail?.session.id === id) renderDetailHeader(); }
  await poll(); await loadList(); toast('已保存'); return true;
 } catch (e) { toast(e.message); return false; }
}
async function poll() {
 if (polling) return; polling = true;
 try {
  const next = await api('state'); const first = !state; state = next; $('connection').hidden = true;
  if (first) { const scope = saved('csessions-scope-' + state.machine, {}); directory = scope.directory || (state.projects.some(p => p.path === state.home) ? state.home : ''); view = scope.view || 'all'; if (!['all', 'favorites', 'hidden'].includes(view)) view = 'all'; if (!expanded.size) { expanded.add(state.home); expanded.add(state.home + '/Projects'); } }
  $('machine').textContent = state.machine + ' / 当前机器'; $('total-count').textContent = state.total; $('favorite-count').textContent = state.favorites; $('hidden-count').textContent = state.hidden;
  $('scan-label').textContent = state.scan.running ? '◌ 正在扫描…' : state.scan.error ? '扫描未完成' : state.scan.lastFinished ? '✓ 会话库已更新' : '准备扫描';
  $('scan-detail').textContent = '发现 ' + state.scan.found + ' 个会话 · 本次更新 ' + state.scan.indexed;
  $('scan-time').textContent = state.scan.running ? '已遍历 ' + state.scan.directories + ' 个目录' : state.scan.errors ? state.scan.errors + ' 个位置无法读取，可刷新重试' : state.scan.lastFinished ? date(state.scan.lastFinished, true) : '首次扫描范围：~/';
  renderTree(); updateScope();
  if (first || state.scan.running || lastScan !== state.scan.lastFinished) { lastScan = state.scan.lastFinished; await loadList(); }
 } catch (e) { error(e.message + (token ? ' · 服务可能已停止，请检查终端。' : '')); }
 finally { polling = false; }
}
document.querySelectorAll('[data-view]').forEach(n => n.onclick = () => chooseScope('', n.dataset.view));
$('project-filter').oninput = renderTree; $('project-sort').onchange = renderTree;
$('search').oninput = () => { offset = 0; showListLoading(); clearTimeout(searchTimer); searchTimer = setTimeout(loadList, 240); };
$('background').onchange = () => { offset = 0; loadList(); };
$('previous').onclick = () => { offset = Math.max(0, offset - 80); loadList(); };
$('next').onclick = () => { offset += 80; loadList(); };
$('find').oninput = highlightFind; $('find-next').onclick = nextMatch;
$('find').onkeydown = e => { if (e.key === 'Enter') nextMatch(); };
$('resume').onclick = () => copy(detail?.resume);
$('enrich').onclick = () => copy(detail?.enrich); $('enrich-preview').onclick = () => copy(detail?.enrichPreview);
$('favorite').onclick = () => detail && annotate({ favorite: !detail.session.favorite });
$('hide').onclick = () => detail && annotate({ hidden: !detail.session.hidden });
$('rename').onclick = () => { if (!detail) return; $('title-input').value = detail.session.manualTitle; $('rename-dialog').showModal(); $('title-input').focus(); };
$('rename-form').onsubmit = async e => { e.preventDefault(); if (await annotate({ title: $('title-input').value })) $('rename-dialog').close(); };
$('settings').onclick = () => { $('roots').replaceChildren(...(state?.roots || []).map(path => el('li', '', pretty(path)))); $('settings-dialog').showModal(); };
$('root-form').onsubmit = async e => { e.preventDefault(); try { await api('roots', { method: 'POST', body: JSON.stringify({ path: $('root-input').value }) }); $('settings-dialog').close(); $('root-input').value = ''; toast('扫描位置已添加，正在扫描'); await poll(); } catch (e) { toast(e.message); } };
$('refresh').onclick = async () => { try { await api('scan', { method: 'POST' }); toast('已开始重新扫描目录'); await poll(); } catch (e) { toast(e.message); } };
$('theme').onclick = () => { const theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'; document.documentElement.dataset.theme = theme; save('csessions-theme', theme); };
document.querySelectorAll('[data-close]').forEach(n => n.onclick = () => $(n.dataset.close).close());
document.addEventListener('keydown', e => { if ((e.ctrlKey || e.metaKey) && e.key === 'k') { e.preventDefault(); $('search').focus(); } });
document.querySelector('.brand').onclick = e => { e.preventDefault(); if (state) chooseScope(''); };
poll(); setInterval(poll, 4000);
