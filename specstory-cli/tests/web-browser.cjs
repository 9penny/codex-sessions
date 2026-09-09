// Optional browser acceptance test. Uses an existing Playwright installation; no production
// npm dependencies are required. See docs/WEB-BROWSER.zh-CN.md for invocation.
const { chromium, expect } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, execFileSync } = require('node:child_process');
const net = require('node:net');

const binary = path.resolve(__dirname, '../bin/csessions');
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'csessions-browser-'));
const home = path.join(root, 'home');
const environment = { ...process.env, HOME: home, CODEX_HOME: path.join(home, '.codex'), XDG_DATA_HOME: path.join(root, 'data'), XDG_CONFIG_HOME: path.join(root, 'config'), XDG_CACHE_HOME: path.join(root, 'cache') };
const ids = ['11111111-1111-1111-1111-111111111110', '11111111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111112'];
let child, browser, forward;
const nativeFiles = [];
function fixture(directory, title, text, id, storage = path.join(home, '.codex/sessions/2026/09/09')) {
 const cwd = path.join(home, directory); fs.mkdirSync(cwd, { recursive: true }); fs.mkdirSync(storage, { recursive: true });
 const file = path.join(storage, 'rollout-' + id + '.jsonl');
 const records = [
  { type: 'session_meta', timestamp: '2026-09-09T04:00:00Z', payload: { id, cwd, source: 'cli' } },
  { type: 'event_msg', timestamp: '2026-09-09T04:01:00Z', payload: { type: 'user_message', message: title } },
  { type: 'event_msg', timestamp: '2026-09-09T04:02:00Z', payload: { type: 'agent_message', message: text } },
 ];
 fs.writeFileSync(file, records.map(r => JSON.stringify(r)).join('\n') + '\n');
 nativeFiles.push([file, fs.readFileSync(file, 'utf8')]); return file;
}
async function start() {
 child = spawn(binary, ['web', '--port', '0'], { env: environment, stdio: ['ignore', 'pipe', 'pipe'] });
 return new Promise((resolve, reject) => {
  let output = ''; const timeout = setTimeout(() => reject(new Error('Server startup timed out')), 15000);
  child.stdout.on('data', chunk => { output += chunk; const match = output.match(/http:\/\/localhost:\d+\/#token=[a-f0-9]+/); if (match) { clearTimeout(timeout); resolve(match[0]); } });
  child.on('exit', code => { clearTimeout(timeout); if (code) reject(new Error('Server exited with ' + code)); });
 });
}
async function stop() { if (!child || child.exitCode !== null) return; await new Promise(resolve => { child.once('exit', resolve); child.kill('SIGTERM'); }); }
(async () => {
 fixture('Projects/atlas', '构建项目的全局会话导航', '## 方案\n\n浏览历史，回到终端继续。\n\n```go\nfmt.Println("hello sessions")\n```', ids[0]);
 fixture('Projects/atlas/frontend', '修复中文搜索', '修复缓存匹配，搜索命中后定位消息。', ids[1]);
 fixture('Projects/orbit', '实现增量索引', '只处理变化的会话。', ids[2]);
 fixture('Projects/ignored', 'ignored dependency', 'should be skipped', 'ignored', path.join(home, 'node_modules'));
 let url = await start();
 browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), args: ['--no-sandbox'] });
 const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, permissions: ['clipboard-read', 'clipboard-write'] });
 const page = await context.newPage(); const errors = [];
 page.on('pageerror', error => errors.push(error.message));
 await page.goto(url);
 await expect(page.locator('.session-card')).toHaveCount(3);
 await page.locator('.tree-row', { has: page.locator('.tree-name', { hasText: /^Projects$/ }) }).click();
 await expect(page.locator('.session-card')).toHaveCount(3);
 await page.locator('.tree-row', { has: page.locator('.tree-name', { hasText: /^atlas$/ }) }).click();
 await expect(page.locator('.session-card')).toHaveCount(2);
 await page.locator('.session-card', { hasText: '构建项目' }).click();
 await expect(page.locator('#transcript pre')).toContainText('hello sessions');
 await page.locator('#resume').click();
 const command = await page.evaluate(() => navigator.clipboard.readText());
 assert(command.includes("codex resume '" + ids[0] + "'")); assert(command.includes('Projects/atlas'));
 await page.locator('.copy-code').click();
 assert((await page.evaluate(() => navigator.clipboard.readText())).includes('hello sessions'));
 await page.locator('#rename').click(); await page.locator('#title-input').fill('我的导航项目'); await page.locator('#rename-form .primary').click();
 await expect(page.locator('#reader-title')).toHaveText('我的导航项目');
 await page.locator('#favorite').click(); await expect(page.locator('#favorite')).toHaveText('★ 已收藏');
 await page.locator('#hide').click(); await expect(page.locator('.session-card')).toHaveCount(1);
 await page.locator('[data-view=hidden]').click(); await expect(page.locator('.session-card')).toHaveCount(1);
 await page.locator('.session-card').click(); await page.locator('#hide').click(); await expect(page.locator('.session-card')).toHaveCount(0);
 await page.locator('[data-view=favorites]').click(); await expect(page.locator('.session-card')).toHaveCount(1);
 await page.locator('[data-view=all]').click(); await page.locator('#search').fill('缓存'); await expect(page.locator('.session-card')).toHaveCount(1);
 await page.locator('.session-card', { hasText: '修复中文搜索' }).click(); await expect(page.locator('#transcript mark')).toHaveCount(1);
 await page.locator('#search').fill(''); await expect(page.locator('.session-card')).toHaveCount(3);
 // Add a storage location outside HOME through the actual UI.
 const extra = path.join(root, 'extra'); fixture('Projects/archive', '额外位置会话', '来自用户选择的扫描位置。', 'extra', path.join(extra, '.codex/sessions'));
 await page.locator('#settings').click(); await page.locator('#root-input').fill(extra); await page.locator('#root-form .primary').click();
 await expect(page.locator('.session-card')).toHaveCount(4, { timeout: 15000 });
 // Simulate completed enrichment with metadata matching the native fingerprint. Never call an AI service.
 execFileSync('python3', ['-c', `import sqlite3,sys\nc=sqlite3.connect(sys.argv[1])\ns=c.execute("select size,mtime,index_version from sessions where session_id=?",(sys.argv[2],)).fetchone()\nc.execute("insert into ai_metadata(agent,session_id,source_size,source_mtime,source_index_version,prompt_version,model,title,summary,tags_json,input_tokens,output_tokens,enriched_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?)",('codex',sys.argv[2],*s,1,'synthetic','AI 项目导航','自动生成的导航摘要','["导航"]',10,5,'2026-09-09T04:03:00Z'))\nc.commit()`, path.join(root, 'data/csessions/sessions.db'), ids[0]]);
 await page.locator('.session-card', { hasText: '我的导航项目' }).click();
 await expect(page.locator('#ai-summary')).toContainText('自动生成的导航摘要');
 await expect(page.locator('#reader-title')).toHaveText('我的导航项目');
 await page.locator('.ai-tools summary').click(); await page.locator('#enrich-preview').click();
 assert((await page.evaluate(() => navigator.clipboard.readText())).includes('--dry-run'));
 await page.locator('#enrich').click(); assert((await page.evaluate(() => navigator.clipboard.readText())).includes('--yes'));
 await page.screenshot({ path: process.env.SCREENSHOT_PATH || path.join(root, 'browser.png'), fullPage: true });
 await page.locator('#theme').click(); await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
 assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
 await page.setViewportSize({ width: 760, height: 1000 });
 assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
 await page.setViewportSize({ width: 1440, height: 1000 });
 // A loopback TCP forward exercises the same HTTP host/origin behavior as SSH -L.
 const targetPort = Number(new URL(url).port);
 forward = net.createServer(socket => { const upstream = net.connect(targetPort, '127.0.0.1'); socket.pipe(upstream); upstream.pipe(socket); socket.on('error', () => upstream.destroy()); upstream.on('error', () => socket.destroy()); socket.on('close', () => upstream.destroy()); });
 await new Promise(resolve => forward.listen(0, '127.0.0.1', resolve));
 const remote = await context.newPage();
 await remote.goto(url.replace(':' + targetPort, ':' + forward.address().port));
 await expect(remote.locator('.session-card')).toHaveCount(4); await remote.close(); forward.close(); forward = null;
 for (const [file, original] of nativeFiles) assert.equal(fs.readFileSync(file, 'utf8'), original, 'Native transcript was modified');
 // Restart and rebuild only the disposable SQLite database; personal edits must survive both.
 await stop();
 for (const suffix of ['', '-wal', '-shm']) fs.rmSync(path.join(root, 'data/csessions/sessions.db' + suffix), { force: true });
 url = await start(); await page.goto(url); await expect(page.locator('.session-card')).toHaveCount(4);
 await page.locator('[data-view=favorites]').click(); await expect(page.locator('.session-card')).toHaveCount(1); await expect(page.locator('.session-card')).toContainText('我的导航项目');
 await page.locator('.session-card').click();
 const nativePath = nativeFiles[0][0];
 fs.appendFileSync(nativePath, JSON.stringify({type:'event_msg',timestamp:'2026-09-09T05:00:00Z',payload:{type:'agent_message',message:'增量更新验收标记'}}) + '\n');
 await expect(page.locator('#transcript')).toContainText('增量更新验收标记', { timeout: 30000 });
 assert.deepEqual(errors, []);
 console.log('PASS: project subtrees, search, masked reading, code/resume copy, rename, favorites, hide/restore, extra roots, AI metadata, theme, responsive layout, TCP forwarding, restart/rebuild persistence, native immutability.');
})().catch(error => { console.error(error); process.exitCode = 1; }).finally(async () => { if (browser) await browser.close(); if (forward) forward.close(); await stop(); fs.rmSync(root, { recursive: true, force: true }); });
