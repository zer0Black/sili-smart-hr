import { defineConfig } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

// 轻量 .env 加载：Playwright 默认不读 .env，这里手动解析注入 process.env（不引入 dotenv）。
// 仅设置尚未存在的键，避免覆盖运行时已 export 的值。
const envFile = path.join(__dirname, '.env');
if (fs.existsSync(envFile)) {
  for (const line of fs.readFileSync(envFile, 'utf-8').split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eq = trimmed.indexOf('=');
    if (eq < 0) continue;
    const key = trimmed.slice(0, eq).trim();
    const val = trimmed.slice(eq + 1).trim();
    if (key && process.env[key] === undefined) process.env[key] = val;
  }
}

// globalSetup 登录一次落盘 storageState，各 test 复用登录态，规避后端登录限流（IP 30/min + username 20/min）。
const USER_STATE_PATH = path.join(__dirname, 'e2e', '.auth', 'user.json');

// 本次运行的报告目录：<e2e_test>/e2e-report/<时间戳>-<前缀>/。
// 时间戳在前、精确到秒，按目录名排序即时间序；后接前缀 E2E_REPORT_NAME（给本次运行
// 打标签，如 test-id），不设时为 default。每次运行落到独立目录、互不覆盖。
const now = new Date();
const pad = (n: number) => String(n).padStart(2, '0');
const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
const reportDir = path.join(__dirname, 'e2e-report', `${stamp}-${process.env.E2E_REPORT_NAME || 'default'}`);

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  timeout: 60_000,
  fullyParallel: false,
  // globalSetup 登录一次生成 storageState，再由 use.storageState 注入每个 test 的 context。
  globalSetup: path.join(__dirname, 'e2e', 'auth.ts'),
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : 1,
  // 本地两份报告共用同一个本次运行目录 reportDir，目录内平铺：
  // a-playwright-report.html（调试用，含堆栈 trace、完整调用树）、bug-<test-id>.html
  // （人话版，每个失败 test-id 一份）、z-screenshots/（公共截图）。
  reporter: process.env.CI
    ? [['blob', { outputFile: 'blob-report/report.zip' }]]
    : [
        ['html', { outputFolder: reportDir, open: 'never' }],
        ['./e2e/utils/bug-reporter.ts', { outputDir: reportDir }],
      ],
  use: {
    baseURL: process.env.APP_URL || 'http://localhost:3000',
    // 复用 globalSetup 落盘的登录态与中文语言偏好，每 test 免登录、绕开限流。
    storageState: USER_STATE_PATH,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
  },
  expect: {
    timeout: 10_000,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
