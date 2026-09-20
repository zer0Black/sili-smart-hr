import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 60000,
  retries: 0,
  use: {
    baseURL: 'http://[::1]:3000',
    headless: true,
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    screenshot: 'only-on-failure',
    trace: 'off',
  },
  outputDir: './test-results',
  reporter: [['list']],
});
