import { test, expect } from '@playwright/test';

const BASE = 'http://[::1]:3000';

test('页面体验探索截图', async ({ page }) => {
  await page.goto(BASE + '/login');
  await page.getByPlaceholder('请输入账号').fill('admin');
  await page.getByPlaceholder('请输入密码').fill('Qa@Test2026');
  await page.getByRole('button', { name: '登录' }).click();
  await page.waitForURL('**/', { timeout: 15000 });
  await page.goto(BASE + '/assessment');
  await page.waitForTimeout(2000);
  await page.screenshot({ path: 'shots/explore-main.png', fullPage: true });
  // 列表分页控件
  const pager = page.getByText(/共 \d+ 条/);
  await expect(pager).toBeVisible();
  // 打开发起弹窗再截一张（含人员下拉错误态观察）
  await page.getByRole('button', { name: '发起评测' }).click();
  await page.getByRole('dialog').getByText('点击选择评估对象').click();
  await page.getByPlaceholder('输入人名搜索').fill('张三不存在的人');
  await page.waitForTimeout(1200);
  await page.screenshot({ path: 'shots/explore-staff-empty.png', fullPage: true });
  await expect(page.getByText('未找到匹配的人员')).toBeVisible();
});
