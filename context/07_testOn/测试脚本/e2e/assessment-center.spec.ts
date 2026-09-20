import { test, expect } from '@playwright/test';

// UI-CENTER / UI-FAILURE-RERUN / UI-EXPLORE 评测运营中心页面用例。
// 前置：后端 8080 + 前端 3000 已起，admin/Qa@Test2026 可登录，库内有多状态批次。

const BASE = 'http://[::1]:3000';

async function login(page) {
  await page.goto(BASE + '/login');
  await page.getByPlaceholder('请输入账号').fill('admin');
  await page.getByPlaceholder('请输入密码').fill('Qa@Test2026');
  await page.getByRole('button', { name: '登录' }).click();
  await page.waitForURL('**/', { timeout: 15000 });
}

test.describe('评测运营中心', () => {
  test('页面加载：统计卡/计划卡/tab/列表字段', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    // tab 容器与首期唯一 tab
    await expect(page.getByRole('tab', { name: 'AI 使用能力' })).toBeVisible();
    // 统计卡三字段
    await expect(page.getByText('本期评测次数')).toBeVisible();
    await expect(page.getByText('已完成评估人次')).toBeVisible();
    await expect(page.getByText('进行中批次数')).toBeVisible();
    // 计划卡字段
    await expect(page.getByText('下次执行')).toBeVisible();
    await expect(page.getByText('周期长度')).toBeVisible();
    await expect(page.getByText('评估维度')).toBeVisible();
    await expect(page.getByText(/底层 \d+ 维 \+ 上层 \d+ 维/)).toBeVisible();
    // 列表表头
    for (const col of ['批次号', '触发方式', '评估时段', '状态', '覆盖会话', '失败人数', '触发时间']) {
      await expect(page.getByRole('columnheader', { name: col })).toBeVisible();
    }
    // 批次号等宽字体（mono class）
    const batchNoCell = page.locator('td.font-mono, td [class*="mono"]').first();
    await expect(batchNoCell).toBeVisible();
    await page.screenshot({ path: 'shots/ui-center-full.png', fullPage: true });
  });

  test('筛选与重置', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    // shadcn Select 是 button[role=combobox] + 弹层列表项，非原生 select
    await page.getByRole('combobox').first().click();
    await page.getByRole('option', { name: '手动发起' }).click();
    await page.getByRole('button', { name: '查询' }).click();
    await page.waitForTimeout(800);
    // 触发方式列全部为手动发起
    const cells = page.getByRole('cell').filter({ hasText: '手动发起' });
    expect(await cells.count()).toBeGreaterThan(0);
    await expect(page.getByRole('cell').filter({ hasText: '定时评估' })).toHaveCount(0);
    await page.screenshot({ path: 'shots/ui-filter-manual.png' });
    // 重置
    await page.getByRole('button', { name: '重置' }).click();
    await page.waitForTimeout(800);
    await expect(page.getByRole('cell').filter({ hasText: '手动发起' }).first()).toBeVisible();
  });

  test('发起评测弹窗：类型卡片/互斥/时段回填/校验', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    await page.getByRole('button', { name: '发起评测' }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    // 对话分析默认选中，另两态置灰
    await expect(dialog.getByText('对话分析')).toBeVisible();
    await expect(dialog.getByText('AI 管理能力')).toBeVisible();
    await expect(dialog.getByText('九型人格')).toBeVisible();
    // 时段默认回填（weekly → 上周一至上周日，非空）；type=date 输入框以 aria-label 定位
    const start = dialog.getByLabel('开始日期');
    const end = dialog.getByLabel('结束日期');
    expect(await start.inputValue()).not.toBe('');
    expect(await end.inputValue()).not.toBe('');
    await page.screenshot({ path: 'shots/ui-create-dialog.png' });
    // 空对象提交 → 字段级校验（不弹 toast）
    await dialog.getByRole('button', { name: '提交' }).click();
    await expect(dialog.getByText('请至少选择一名评估对象')).toBeVisible();
    // 取消（对象为空无二次确认）
    await dialog.getByRole('button', { name: '取消' }).click();
    await expect(dialog).toBeHidden();
  });

  test('失败明细弹窗与查看结果置灰', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    // 失败人数非零的批次行有失败明细入口
    const failRow = page.getByRole('row').filter({ hasText: '手动发起' }).filter({ hasText: '失败' }).first();
    await failRow.getByRole('button', { name: '失败明细' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText(/失败明细 · B\d+/)).toBeVisible();
    await expect(dlg.getByText('姓名')).toBeVisible();
    await expect(dlg.getByText('失败原因摘要')).toBeVisible();
    // 只读：无单人重试/删除按钮
    await expect(dlg.getByRole('button', { name: '重试' })).toHaveCount(0);
    await page.screenshot({ path: 'shots/ui-failure-dialog.png' });
    // 按失败对象重新发起 → 预填（人数随选中失败批次名单变化，断言「已选 N 人」存在即可）
    await dlg.getByRole('button', { name: '按失败对象重新发起' }).click();
    const createDlg = page.getByRole('dialog');
    await expect(createDlg.getByText(/已选 \d+ 人/)).toBeVisible();
    await page.screenshot({ path: 'shots/ui-rerun-prefill.png' });
    await createDlg.getByRole('button', { name: '取消' }).click();
    // 取消有已选人员 → 二次确认
    await expect(page.getByRole('alertdialog').getByText('已选择评估对象，确定放弃并关闭？')).toBeVisible();
    await page.getByRole('button', { name: '放弃并关闭' }).click();
  });

  test('查看结果置灰与悬浮提示', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    const viewBtn = page.getByRole('button', { name: '查看结果' }).first();
    await expect(viewBtn).toBeDisabled();
    await expect(viewBtn).toHaveAttribute('title', '个人画像功能建设中');
  });
});

test.describe('补充检查', () => {
  test('全员互斥与提交流程', async ({ page }) => {
    await login(page);
    await page.goto(BASE + '/assessment');
    await page.getByRole('button', { name: '发起评测' }).click();
    const dialog = page.getByRole('dialog');
    // 打开人员选择（自研下拉：button 列表非 role=option），选全员
    await dialog.getByText('点击选择评估对象').click();
    await dialog.getByRole('button', { name: /全员/ }).first().click();
    await expect(dialog.getByText('全员').first()).toBeVisible();
    // 再选具体人员 → 全员自动移除（互斥）。搜索「李」后无结果（staffs 数据源是 username 口径
    // 只有 admin/肖文宇，token_name 人员检索不到——口径缺陷的页面侧表现），改搜「肖」选肖文宇。
    const dropdown = dialog.locator('div.z-50');
    await dialog.getByPlaceholder('输入人名搜索').fill('肖');
    await dropdown.getByRole('button', { name: '肖文宇', exact: true }).click();
    await expect(dialog.getByText('已选 1 人')).toBeVisible();
    // 点弹窗外空白处收起下拉（Escape/外侧点击会触发取消确认弹窗，先处理它）
    await page.mouse.click(560, 200);
    const confirmDlg = page.getByRole('alertdialog');
    if (await confirmDlg.count() > 0) {
      await confirmDlg.getByRole('button', { name: '取消' }).click();
    }
    // 提交（用已回填的默认时段）→ 弹窗关闭、列表顶部出现新进行中批次
    await dialog.getByRole('button', { name: '提交' }).click();
    await expect(dialog).toBeHidden({ timeout: 15000 });
    await page.waitForTimeout(1500);
    const firstRow = page.getByRole('row').nth(1);
    await expect(firstRow.getByText('进行中')).toBeVisible({ timeout: 10000 });
    await page.screenshot({ path: 'shots/ui-submitted.png', fullPage: true });
  });
});
