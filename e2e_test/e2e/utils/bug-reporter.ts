// /e2e_test/e2e/utils/bug-reporter.ts
//
// 人话版 Bug 报告生成器（自定义 Playwright Reporter）
//
// 目的：测试失败时，把「开发者向」的默认报告转译成一份面向 bug 提报的人话报告。
// 每个失败用例列出复现步骤、期望、现状、截图，并附带可一键复制的纯文本工单文案，
// 方便直接贴到工单系统。调试用的原始报告仍由 Playwright HTML reporter 生成，
// 落在同一运行目录的 a-playwright-report.html，两者互不替代。
//
// 数据来源：
// - 复现步骤  ← 测试代码里的 test.step(...) 调用（硬性规则 M8 强制包裹业务动作）
// - 期望/现状 ← 失败断言的 matcherResult.expected / actual
// - 截图      ← Playwright 失败时自动截图（config 中 screenshot: 'only-on-failure'）
//
// 产出位置：与 html reporter 共用同一个本次运行目录 /e2e_test/e2e-report/<时间戳>-<前缀>/。
// 目录内平铺，靠前缀排序：a-playwright-report.html（整份调试报告，本文件把 html 写的
// index.html 改名而来）、bug-<test-id>.html（每个失败 test-id 一份）、z-screenshots/
// （公共截图，所有 bug 报告共用）。运行目录名由 playwright.config.ts 加载时算出，
// 形如 <时间戳>-<前缀>，时间戳在前、精确到秒，前缀取 E2E_REPORT_NAME（不设为 default），每次运行独立、互不覆盖。
// 由 e2e测试 技能在生成阶段首次拷贝到项目，之后每次 npx playwright test 自动产出，无需额外命令。

import type {
  Reporter,
  FullConfig,
  Suite,
  TestCase,
  TestResult,
  TestStep,
} from '@playwright/test/reporter';
import * as fs from 'fs';
import * as path from 'path';

interface FailureEntry {
  testId: string;
  title: string;
  suitePath: string[];
  status: string;
  steps: string[];
  expected: string;
  actual: string;
  error: string;
  locator: string;
  screenshots: string[];
  duration: number;
}

const DEFAULT_OUTPUT_DIR = 'e2e-report';
const SCREENSHOT_SUBDIR = 'z-screenshots';
const PLAYWRIGHT_REPORT_FILE = 'a-playwright-report.html';

class BugReportReporter implements Reporter {
  private outputDir: string;
  private failures: FailureEntry[] = [];

  constructor(options: { outputDir?: string } = {}) {
    this.outputDir = options.outputDir ?? DEFAULT_OUTPUT_DIR;
  }

  // 项目里若有多个 reporter 共存，config 加载阶段会被调用，此处无需处理
  onBegin(_config: FullConfig, _suite: Suite): void {}

  onTestEnd(test: TestCase, result: TestResult): void {
    // 只处理真正失败的用例；跳过、通过、中断都不进 bug 报告
    const status = result.status as string;
    const failed = status === 'failed' || status === 'timedOut' || status === 'flaky';
    if (!failed) return;

    const file = test.location?.file ?? '';
    const expectation = extractExpectation(result.errors ?? []);

    this.failures.push({
      testId: inferTestId(file),
      title: test.title,
      suitePath: test.titlePath().slice(0, -1),
      status: result.status,
      steps: extractSteps(result.steps ?? []),
      expected: expectation.expected,
      actual: expectation.actual,
      error: expectation.error,
      locator: expectation.locator,
      screenshots: extractScreenshotPaths(result.attachments ?? []),
      duration: result.duration,
    });
  }

  onEnd(): void {
    // html reporter 注册在本 reporter 之前，其 onEnd 先执行：清空运行目录并写入 index.html。
    // 这里把它改名为 a-playwright-report.html，让运行目录内报告按 a-/bug-/z- 前缀排序。
    // 无条件执行：调试报告每次都产出，哪怕本次全绿也要改名。index.html 不存在时跳过。
    renamePlaywrightReport(this.outputDir);

    // 全绿时没有 bug 可报，直接返回（bug 报告只在有失败时产出）
    if (this.failures.length === 0) return;

    // 截图统一拷到运行目录的公共 z-screenshots/，所有 bug 报告共用，不重复存储
    materializeScreenshots(this.failures, this.outputDir);

    // 按 test-id 分组：每个 test-id 一份 bug 报告，命名 bug-<test-id>.html，直接落在运行目录
    const groups = new Map<string, FailureEntry[]>();
    for (const failure of this.failures) {
      if (!groups.has(failure.testId)) groups.set(failure.testId, []);
      groups.get(failure.testId)!.push(failure);
    }

    for (const [testId, failures] of groups) {
      const html = renderHtml(testId, failures);
      fs.writeFileSync(path.join(this.outputDir, `bug-${testId}.html`), html, 'utf-8');
    }
  }
}

export default BugReportReporter;

// ---------------------------------------------------------------------------
// 解析辅助
// ---------------------------------------------------------------------------

/** 从 e2e/[test-id]/xxx.spec.ts 的路径里提取模块名 test-id */
function inferTestId(file: string): string {
  const normalized = file.replace(/\\/g, '/');
  const marker = '/e2e/';
  const idx = normalized.lastIndexOf(marker);
  if (idx >= 0) {
    const segment = normalized.slice(idx + marker.length).split('/')[0];
    if (segment) return segment;
  }
  // 回退到文件名
  return path.basename(file, path.extname(file)) || 'unknown';
}

/**
 * 递归收集 test.step 的标题。Playwright 给用户写的步骤打 category 'test.step'；
 * fixture、hook、expect 等其他类别不收集，以免把框架内部噪音混进复现步骤。
 * 嵌套步骤按先序展开，保留用户书写的顺序。
 */
function extractSteps(steps: TestStep[]): string[] {
  const collected: string[] = [];
  const walk = (list: TestStep[]): void => {
    for (const step of list) {
      if (step.category === 'test.step') collected.push(step.title);
      if (step.steps && step.steps.length > 0) walk(step.steps);
    }
  };
  walk(steps);
  return collected;
}

/**
 * 从失败错误里提取「期望 / 现状」。两条路径：
 * 1. matcherResult（locator 类 matcher，如 toHaveText）—— Playwright 在 error.matcherResult
 *    上挂 expected / actual / locator，结构化最准，优先用。
 * 2. message 文本解析（toBe / toEqual 等值 matcher）—— 这类断言的 error 不挂 matcherResult，
 *    期望/现状只写在 message 里（"Expected: ..." / "Received: ..."），用正则提取。
 *
 * message 里常混入 ANSI 终端颜色码（如 \u001b[31m），不清除会在报告里出现 [31m 这类乱码，
 * 因此所有来自 message 的文本统一经过 stripAnsi。
 */
function extractExpectation(errors: unknown[]): {
  expected: string;
  actual: string;
  error: string;
  locator: string;
} {
  const list = errors as Array<Record<string, unknown>>;
  const withMatcher = list.find((e) => e && typeof e === 'object' && 'matcherResult' in e) as
    | (Record<string, unknown> & { matcherResult?: Record<string, unknown> })
    | undefined;

  // 路径一：matcherResult 存在时结构化提取
  if (withMatcher?.matcherResult) {
    const matcher = withMatcher.matcherResult;
    return {
      expected: stringify(matcher.expected),
      actual: stringify(matcher.actual),
      error: stripAnsi(String(matcher.message ?? withMatcher.message ?? '')),
      locator: stringify(matcher.locator),
    };
  }

  // 路径二：从 message 文本解析（Playwright 较新版本对值类 matcher 不挂 matcherResult）
  const chosen = list[0];
  const message = stripAnsi(String(chosen?.message ?? chosen?.snippet ?? ''));
  const parsed = parseExpectedReceived(message);

  // 解析出期望/现状后，error 留空，避免「现状」栏重复堆一整段 message
  return {
    expected: parsed.expected,
    actual: parsed.actual,
    error: parsed.hasExpectedReceived ? '' : message,
    locator: '',
  };
}

/**
 * 从已清 ANSI 的 message 里解析「Expected: ...」「Received: ...」。
 * 兼容 toBe（"Expected:"）与 toHaveText（"Expected string:"）等写法。
 * 解析值去掉外层成对引号，让报告显示裸值而非带引号字面量。
 */
function parseExpectedReceived(message: string): {
  expected: string;
  actual: string;
  hasExpectedReceived: boolean;
} {
  const expMatch = message.match(/Expected[^:\n]*:\s*(.*)/);
  const actMatch = message.match(/Received[^:\n]*:\s*(.*)/);
  const expected = expMatch ? trimQuotes(expMatch[1].trim()) : '';
  const actual = actMatch ? trimQuotes(actMatch[1].trim()) : '';
  return { expected, actual, hasExpectedReceived: Boolean(expMatch && actMatch) };
}

/** 去掉外层成对引号，否则会原样出现在报告里 */
function trimQuotes(s: string): string {
  if (
    s.length >= 2 &&
    ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'")))
  ) {
    return s.slice(1, -1);
  }
  return s;
}

/** 清除 ANSI 转义序列（颜色、样式），终端码不应进入报告文本 */
function stripAnsi(text: string): string {
  // ESC(0x1b) 或 CSI(0x9b) 开头，形如 \x1b[31m
  return text.replace(/[\u001b]\[[0-9;]*[a-zA-Z]/g, '');
}

/** 收集截图附件的绝对路径 */
function extractScreenshotPaths(attachments: TestResult['attachments']): string[] {
  const paths: string[] = [];
  for (const a of attachments ?? []) {
    if (!a) continue;
    const contentType = (a as { contentType?: string }).contentType ?? '';
    const p = (a as { path?: string }).path;
    if (contentType.startsWith('image/') && p) paths.push(p);
  }
  return paths;
}

/**
 * 把截图从 Playwright 的 output 目录拷到运行目录的公共 z-screenshots/ 下，并把 failure 上的路径
 * 改写成相对路径，供 HTML 引用。所有失败用例共用这一个截图目录，不重复存储。
 * 文件名带 test-id 前缀，避免不同失败用例的截图在公共目录撞名。
 */
function materializeScreenshots(failures: FailureEntry[], reportDir: string): void {
  const targetDir = path.join(reportDir, SCREENSHOT_SUBDIR);
  fs.mkdirSync(targetDir, { recursive: true });
  const usedNames = new Set<string>();

  for (const failure of failures) {
    const relocated: string[] = [];
    for (let i = 0; i < failure.screenshots.length; i++) {
      const src = failure.screenshots[i];
      if (!src || !fs.existsSync(src)) continue;
      const ext = path.extname(src) || '.png';
      const base = `${sanitize(failure.testId)}-${sanitize(failure.title)}-${i + 1}${ext}`;
      const name = uniqueName(base, usedNames);
      usedNames.add(name);
      const dest = path.join(targetDir, name);
      try {
        fs.copyFileSync(src, dest);
      } catch {
        // 拷贝失败则回退到原始绝对路径，至少让图片有机会显示
        relocated.push(src);
        continue;
      }
      relocated.push(`${SCREENSHOT_SUBDIR}/${name}`);
    }
    failure.screenshots = relocated;
  }
}

/**
 * 把 html reporter 写的 index.html 改名为 a-playwright-report.html。
 * html reporter 注册在本 reporter 之前，onEnd 先执行并清空 outputDir、写入 index.html，
 * 因此调用时 index.html 已存在。改名只动文件名、不挪目录，内部对 ./data 等附属文件的
 * 相对引用仍然有效。index.html 不存在或改名失败时跳过，保留原 index.html 不影响可用性。
 */
function renamePlaywrightReport(outputDir: string): void {
  const src = path.join(outputDir, 'index.html');
  if (!fs.existsSync(src)) return;
  const dest = path.join(outputDir, PLAYWRIGHT_REPORT_FILE);
  try {
    fs.renameSync(src, dest);
  } catch {
    try {
      fs.writeFileSync(dest, fs.readFileSync(src, 'utf-8'), 'utf-8');
      fs.unlinkSync(src);
    } catch {
      // 兜底也失败则放弃，保留 index.html
    }
  }
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

/** 生成单个失败用例的纯文本工单文案，藏在卡片里供「复制」按钮使用 */
function renderPlainText(f: FailureEntry): string {
  const lines: string[] = [];
  lines.push(`【模块】${f.testId}`);
  const scene = [...f.suitePath, f.title].filter(Boolean).join(' > ');
  lines.push(`【场景】${scene}`);
  lines.push(`【状态】${statusLabel(f.status)}`);
  lines.push('');
  if (f.steps.length > 0) {
    lines.push('【复现步骤】');
    f.steps.forEach((step, i) => lines.push(`${i + 1}. ${step}`));
  } else {
    lines.push('【复现步骤】该用例未用 test.step 拆分，无法还原操作路径');
  }
  lines.push('');
  lines.push('【期望】' + (f.expected || '用例应通过'));
  const actuality = [f.actual, f.error, f.locator && `定位器：${f.locator}`]
    .filter(Boolean)
    .join('；');
  lines.push('【现状】' + (actuality || '失败'));
  if (f.screenshots.length > 0) {
    lines.push('');
    lines.push('【截图】' + f.screenshots.join('，'));
  }
  return lines.join('\n');
}

function renderHtml(testId: string, failures: FailureEntry[]): string {
  const cards = failures.map((f, idx) => renderCard(f, idx)).join('\n');
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Bug 报告 · ${escapeHtml(testId)}</title>
<style>
  * { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; color: #24292f; background: #f6f8fa; line-height: 1.6; }
  header { padding: 24px 32px; background: #fff; border-bottom: 1px solid #d0d7de; }
  header h1 { margin: 0 0 8px; font-size: 20px; }
  .meta { color: #57606a; font-size: 14px; }
  .meta strong { color: #cf222e; }
  .hint { margin: 8px 0 0; color: #6e7781; font-size: 13px; }
  main { max-width: 960px; margin: 0 auto; padding: 24px 16px 64px; }
  article { background: #fff; border: 1px solid #d0d7de; border-radius: 8px; padding: 20px 24px; margin-bottom: 20px; }
  .card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .card-head h2 { margin: 0; font-size: 16px; color: #1f2328; }
  .suite { color: #6e7781; font-size: 12px; margin: 4px 0 12px; }
  .copy-btn { flex-shrink: 0; background: #1f6feb; color: #fff; border: none; border-radius: 6px; padding: 6px 12px; font-size: 13px; cursor: pointer; }
  .copy-btn:hover { background: #1158c7; }
  .copy-btn.ok { background: #1a7f37; }
  h3 { margin: 16px 0 6px; font-size: 13px; color: #57606a; font-weight: 600; }
  ol { margin: 0; padding-left: 22px; }
  ol li { margin: 4px 0; }
  .placeholder { color: #9aa4ad; font-style: italic; }
  .expectation { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 8px; }
  .col { background: #f6f8fa; border-radius: 6px; padding: 10px 12px; }
  .col.expected { border-left: 3px solid #1a7f37; }
  .col.actual { border-left: 3px solid #cf222e; }
  .col h4 { margin: 0 0 6px; font-size: 12px; color: #57606a; }
  pre { margin: 0; white-space: pre-wrap; word-break: break-word; font-size: 13px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .shots { display: flex; flex-wrap: wrap; gap: 12px; margin-top: 8px; }
  .shots a { display: block; border: 1px solid #d0d7de; border-radius: 6px; overflow: hidden; }
  .shots img { display: block; max-width: 240px; max-height: 160px; object-fit: cover; }
</style>
</head>
<body>
<header>
  <h1>Bug 报告 · ${escapeHtml(testId)}</h1>
  <div class="meta">模块 <strong>${escapeHtml(testId)}</strong> ｜ 失败 <strong>${failures.length}</strong> 项</div>
  <p class="hint">每张卡片可一键复制为工单文案。调试用的原始堆栈、trace、完整调用树见同目录的 a-playwright-report.html。</p>
</header>
<main>
${cards}
</main>
<script>
  document.querySelectorAll('.copy-btn').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var plain = btn.getAttribute('data-plain') || '';
      var done = function () { btn.classList.add('ok'); btn.textContent = '已复制'; setTimeout(function () { btn.classList.remove('ok'); btn.textContent = '复制工单文案'; }, 1500); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(plain).then(done, function () { fallback(plain, done); });
      } else { fallback(plain, done); }
    });
  });
  function fallback(text, cb) {
    var ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); cb(); } catch (e) {} document.body.removeChild(ta);
  }
</script>
</body>
</html>`;
}

function renderCard(f: FailureEntry, index: number): string {
  const plain = renderPlainText(f);
  const stepsHtml =
    f.steps.length > 0
      ? `<ol>${f.steps.map((s) => `<li>${escapeHtml(s)}</li>`).join('')}</ol>`
      : `<p class="placeholder">该用例未用 test.step 拆分业务动作，无法还原复现路径。按硬性规则 M8 补充后可生成步骤。</p>`;
  const shotsHtml =
    f.screenshots.length > 0
      ? `<div class="shots">${f.screenshots
          .map(
            (s) =>
              `<a href="${escapeAttr(s)}" target="_blank"><img src="${escapeAttr(s)}" alt="失败截图"></a>`
          )
          .join('')}</div>`
      : `<p class="placeholder">无截图（确认 config 中 screenshot 已设为 only-on-failure）</p>`;
  const locatorLine = f.locator
    ? `\n定位器：${escapeHtml(f.locator)}`
    : '';
  return `<article>
  <div class="card-head">
    <div>
      <h2>${index + 1}. ${escapeHtml(f.title)}</h2>
      <div class="suite">${escapeHtml(f.suitePath.join(' › '))} · ${escapeHtml(statusLabel(f.status))} · ${(f.duration / 1000).toFixed(1)}s</div>
    </div>
    <button class="copy-btn" data-plain="${escapeAttr(plain)}">复制工单文案</button>
  </div>
  <h3>复现步骤</h3>
  ${stepsHtml}
  <h3>期望 / 现状</h3>
  <div class="expectation">
    <div class="col expected"><h4>期望</h4><pre>${escapeHtml(f.expected || '用例应通过')}</pre></div>
    <div class="col actual"><h4>现状</h4><pre>${escapeHtml([f.actual, f.error].filter(Boolean).join('\n') || '失败')}${escapeHtml(locatorLine)}</pre></div>
  </div>
  <h3>截图</h3>
  ${shotsHtml}
</article>`;
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

function stringify(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'failed':
      return '失败';
    case 'timedOut':
      return '超时';
    case 'flaky':
      return '不稳定（重试后通过）';
    default:
      return status;
  }
}

function sanitize(text: string): string {
  return text.replace(/[\\/:*?"<>|\s]+/g, '_').slice(0, 60) || 'screenshot';
}

function uniqueName(base: string, used: Set<string>): string {
  if (!used.has(base)) return base;
  const ext = path.extname(base);
  const stem = path.basename(base, ext);
  let n = 2;
  while (used.has(`${stem}-${n}${ext}`)) n++;
  return `${stem}-${n}${ext}`;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function escapeAttr(text: string): string {
  return escapeHtml(text).replace(/'/g, '&#39;');
}
