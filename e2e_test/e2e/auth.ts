// /e2e_test/e2e/auth.ts
// 共享认证 fixture（单角色）。sili-smart-hr 系统已全量移除角色概念，平台账号统一全权，故只需单角色。
//
// 认证策略：globalSetup 登录一次落盘 storageState，各 test 的 context 从该文件初始化，自带登录态。
// 这是为了规避后端登录限流（IP 30/min + username 20/min）：每 test 全流程登录会很快触发 429。
// globalSetup 由 playwright.config.ts 的 globalSetup 字段指向此文件的 setupAuth 导出。
import { test as base, expect, request as pwRequest, type APIRequestContext } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

// ── 认证凭据（从环境变量读取，|| 后为默认值便于本地调试） ──
export const APP_URL = process.env.APP_URL || 'http://localhost:3000';
export const API_URL = process.env.API_URL || 'http://localhost:8080';
const USERNAME = process.env.TEST_USERNAME || 'admin';
const PASSWORD = process.env.TEST_PASSWORD || 'hzwlsoft.com';

// ── Storage State 持久化（供 globalSetup 落盘、各 test 复用、afterAll request 解析 token） ──
export const AUTH_DIR = path.join(__dirname, '.auth');
export const USER_STATE_PATH = path.join(AUTH_DIR, 'user.json');

// 全局登录：globalSetup 调一次。用独立 chromium 上下文走完整登录 UI 流程，
// 注入中文语言偏好（i18n 检测顺序 localStorage→navigator，新 context 回退 en-US 会渲染英文失配中文断言），
// 落盘 storageState 供所有 test 复用。返回供 config 控制台打印。
export async function setupAuth(): Promise<void> {
  const { chromium } = await import('@playwright/test');
  if (!fs.existsSync(AUTH_DIR)) fs.mkdirSync(AUTH_DIR, { recursive: true });
  const browser = await chromium.launch();
  const context = await browser.newContext();
  // 锁定中文：注入到每个新文档的 localStorage，登录页首次渲染即中文。
  await context.addInitScript(() => {
    localStorage.setItem('sili-smart-hr-lang', 'zh');
  });
  const page = await context.newPage();
  await page.goto(`${APP_URL}/login`);
  await page.waitForLoadState('domcontentloaded');
  await page.getByRole('textbox', { name: '账号' }).fill(USERNAME);
  await page.getByRole('textbox', { name: '密码' }).fill(PASSWORD);
  await page.getByRole('button', { name: '登录' }).click();
  // 等待 URL 不再含 login（跳到 / 或其他受保护页）。
  await page.waitForURL((url) => !url.pathname.includes('login'), { timeout: 30_000 });
  await context.storageState({ path: USER_STATE_PATH });
  await browser.close();
}

// 建一个携带登录态的 APIRequestContext，供 afterAll 数据清理用（afterAll 不能用 page/request fixture）。
// storageState 的 localStorage 按 origin 隔离：浏览器里 token 挂在 APP_URL origin 下，而清理请求直打后端 API_URL，
// 跨 origin localStorage 不跟随，故不依赖 storageState，而是从落盘文件解析 token 后用 Authorization 头手动带上。
// storageState 文件由 globalSetup 落盘；若文件不存在（globalSetup 未跑或被跳过）返回 null，调用方需判空跳过。
export async function authedRequest(): Promise<APIRequestContext | null> {
  if (!fs.existsSync(USER_STATE_PATH)) return null;
  const raw = JSON.parse(fs.readFileSync(USER_STATE_PATH, 'utf-8')) as {
    origins?: Array<{ origin?: string; localStorage?: Array<{ name?: string; value?: string }> }>;
  };
  const authEntry = (raw.origins ?? [])
    .flatMap((o) => o.localStorage ?? [])
    .find((kv) => kv.name === 'sili-smart-hr-auth');
  if (!authEntry?.value) return null;
  const token = (JSON.parse(authEntry.value) as { state?: { token?: string } }).state?.token;
  if (!token) return null;
  return pwRequest.newContext({
    baseURL: API_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
}

// 各 test 用 base（非自定义 page fixture）：storageState 由 config use.storageState 全局注入，
// 每个 test 的 page/context 自带登录态与中文偏好，无需每 test 重登。
export const test = base;
export { expect };

// globalSetup 入口：Playwright 取模块 default 导出（须为 async 函数）。
export default setupAuth;
