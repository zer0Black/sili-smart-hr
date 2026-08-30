import { test, expect, authedRequest } from '../auth';
import { DimensionConfigPage, CreateDimensionDialog } from './pages/dimension-config-page';
import {
  EXISTING,
  VALID_CREATE,
  BOUNDARY,
  ACTIVITY_RULE,
  MODULE_CODE,
  uniqueSuffix,
  repeat,
  NAME_BOUNDARY_CHAR,
} from './constants/test-data';

// sonner toast 文案断言辅助：取最后一条（region 限定在旧版 playwright-cli 快照里不稳定，用 .last()）
function toast(page: import('@playwright/test').Page, text: string) {
  return page.getByText(text, { exact: false }).last();
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** 树响应中查找维度节点：modules[].dimensions + modules[].groups[].dimensions（service DTO 实测结构） */
async function findTreeNodes(request: import('@playwright/test').APIRequestContext): Promise<
  Array<{ id: string; name: string }>
> {
  const resp = await request.get('/api/dimensions/tree');
  const body = await resp.json();
  const found: Array<{ id: string; name: string }> = [];
  for (const mod of body?.data?.modules ?? []) {
    for (const node of [...(mod.dimensions ?? []), ...((mod.groups ?? []).flatMap((g: { dimensions?: unknown[] }) => g.dimensions ?? []))]) {
      if (node?.id) found.push({ id: node.id, name: node.name });
    }
  }
  return found;
}

/**
 * 删除维度（仅停用态可调）：全量 update 置停用（name/anchor required，须带全量字段）再 delete。
 * 仅按传入 id 操作，不按名称模糊匹配，杜绝误删既有维度。
 */
async function disableAndDelete(
  request: import('@playwright/test').APIRequestContext,
  id: string,
): Promise<void> {
  const detail = (await (await request.get(`/api/dimensions/${id}`)).json()).data;
  if (!detail) return;
  if (detail.enabled) {
    await request.post('/api/dimensions/update', {
      data: {
        id,
        name: detail.name,
        prompt: detail.prompt ?? '',
        anchor: detail.anchor,
        weight: detail.weight ?? 0,
        include_overview: detail.include_overview ?? false,
        enabled: false,
        description: detail.description ?? '',
        version: detail.version,
      },
    });
    await request.post('/api/dimensions/delete', { data: { id, version: detail.version + 1 } });
  } else {
    await request.post('/api/dimensions/delete', { data: { id, version: detail.version } });
  }
}

/**
 * 既有维度基线自愈：历轮测试失败可能遗留「23」被软删/改名/停用。
 * 在两组依赖既有数据的 describe 前调用，按锚点内容定位（名称可变）恢复为 spec 探索时基线。
 * 通过 API 走后端连接，避免 SQLite 外部写与后端连接池的快照错乱。
 */
async function ensureBaselineDimension(request: import('@playwright/test').APIRequestContext): Promise<void> {
  const tree = await (await request.get('/api/dimensions/tree')).json();
  const aiUsage = (tree?.data?.modules ?? []).find((m: { module_code?: string }) => m.module_code === 'AI_USAGE');
  const nodes = [
    ...(aiUsage?.dimensions ?? []),
    ...((aiUsage?.groups ?? []).flatMap((g: { dimensions?: unknown[] }) => g.dimensions ?? [])),
  ];
  // 基线存在（名为 23 且启用）则无需自愈
  if (nodes.some((n: { name?: string; enabled?: boolean }) => n.name === EXISTING.LEAF_NAME && n.enabled)) return;
  // 找锚点为 32 的旧维度（探索时 23 的锚点），恢复名称与启用态
  for (const n of nodes) {
    const detail = (await (await request.get(`/api/dimensions/${n.id}`)).json()).data;
    if (!detail) continue;
    if (detail.anchor === '32' && detail.code === 'AI') {
      await request.post('/api/dimensions/update', {
        data: {
          id: n.id,
          name: EXISTING.LEAF_NAME,
          prompt: detail.prompt,
          anchor: detail.anchor,
          weight: detail.weight,
          include_overview: detail.include_overview,
          enabled: true,
          description: detail.description,
          version: detail.version,
        },
      });
      return;
    }
  }
}

// ============================================================
// 第一部分：页面加载与浏览（只读为主，「启用态删除禁用」用例自建自清理 + afterAll 兜底）
// ============================================================
test.describe.serial('维度与权重配置页 - 加载与浏览', () => {
  let cleanupName = '';

  // 基线自愈 + E2E 残留清理（历轮失败遗留），跑前保证既有维度「23」可用
  test.beforeAll(async () => {
    const request = await authedRequest();
    if (!request) return;
    await ensureBaselineDimension(request);
    for (const node of await findTreeNodes(request)) {
      if (node.name.startsWith('E2E_')) await disableAndDelete(request, node.id);
    }
    await request.dispose();
  });

  test.afterAll(async () => {
    const request = await authedRequest();
    if (!request || !cleanupName) return;
    for (const node of await findTreeNodes(request)) {
      if (node.name === cleanupName) await disableAndDelete(request, node.id);
    }
    await request.dispose();
  });
  test('页面加载展示四模块树与默认选中叶子', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);

    await test.step('进入维度与权重配置页', async () => {
      await dimPage.goto();
    });
    await test.step('验证页面标题与生效提示信息条', async () => {
      await expect(dimPage.heading).toBeVisible();
      await expect(dimPage.effectNotice).toBeVisible();
    });
    await test.step('验证四模块按固定顺序展示', async () => {
      await expect(dimPage.moduleNode('使用活跃度')).toBeVisible();
      await expect(dimPage.moduleNode('AI 使用能力')).toBeVisible();
      await expect(dimPage.moduleNode('AI 管理能力')).toBeVisible();
      await expect(dimPage.moduleNode('九型人格')).toBeVisible();
    });
    await test.step('验证默认选中首个可配置叶子并展示配置表单', async () => {
      await expect(dimPage.nameInput).toBeVisible();
      await expect(dimPage.anchorInput).toBeVisible();
      await expect(dimPage.saveButton).toBeVisible();
    });
  });

  test('树搜索按名称模糊过滤', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页', async () => {
      await dimPage.goto();
    });
    await test.step('输入既有维度名片段过滤', async () => {
      await dimPage.searchInput.fill(EXISTING.LEAF_NAME);
      await expect(dimPage.leafNode(EXISTING.LEAF_NAME)).toBeVisible();
      await expect(dimPage.leafNode(EXISTING.OTHER_LEAF_NAME)).toBeHidden();
    });
    await test.step('清空关键词恢复全树', async () => {
      await dimPage.searchInput.fill('');
      await expect(dimPage.leafNode(EXISTING.LEAF_NAME)).toBeVisible();
      await expect(dimPage.leafNode(EXISTING.OTHER_LEAF_NAME)).toBeVisible();
    });
  });

  test('模块汇总视图展示权重合计与偏离提示', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    // 期望合计动态计算：从树节点徽标读取两个既有维度的权重求和，避免其他用例改权重后污染静态期望
    let expectedTotal = 0;

    await test.step('进入配置页并读取两既有维度权重徽标', async () => {
      await dimPage.goto();
      for (const leaf of [EXISTING.LEAF_NAME, EXISTING.OTHER_LEAF_NAME]) {
        expectedTotal += await dimPage.leafWeight(leaf);
      }
    });
    await test.step('选中 AI 使用能力模块', async () => {
      await dimPage.selectModule(EXISTING.MODULE_AI_USAGE);
    });
    await test.step('验证汇总表列头与行数据', async () => {
      await expect(dimPage.summaryTable).toBeVisible();
      for (const col of ['序号', '维度名称', '数据来源', '权重', '参与总览', '状态']) {
        await expect(dimPage.summaryTable.getByRole('columnheader', { name: col })).toBeVisible();
      }
      await expect(dimPage.summaryTable.getByRole('cell', { name: EXISTING.LEAF_NAME, exact: true })).toBeVisible();
    });
    await test.step('验证权重合计为两维度之和', async () => {
      await expect(dimPage.weightSummaryText).toHaveText(`${expectedTotal}%`);
    });
    await test.step('验证合计偏离 100% 时显示警示', async () => {
      await expect(dimPage.weightDeviationText).toBeVisible();
    });
  });

  test('使用活跃度模块展示判定关系而非权重合计', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页并选中使用活跃度模块', async () => {
      await dimPage.goto();
      await dimPage.selectModule(EXISTING.MODULE_ACTIVITY);
    });
    await test.step('验证活跃度规则配置表单与默认值', async () => {
      await expect(dimPage.activeThresholdInput).toHaveValue(String(ACTIVITY_RULE.DEFAULT_ACTIVE));
      await expect(dimPage.lowThresholdInput).toHaveValue(String(ACTIVITY_RULE.DEFAULT_LOW));
    });
    await test.step('验证展示判定关系说明', async () => {
      await expect(dimPage.ruleSummaryText).toBeVisible();
      await expect(dimPage.ruleSummaryText).toContainText(`≥${ACTIVITY_RULE.DEFAULT_ACTIVE} 判活跃`);
    });
    await test.step('验证活跃度模块无权重合计条', async () => {
      await expect(dimPage.weightSummaryText).toBeHidden();
    });
  });

  test('编辑态维度编码/所属模块/数据来源只读', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中既有叶子维度', async () => {
      await dimPage.goto();
      await dimPage.selectLeaf(EXISTING.LEAF_NAME);
    });
    await test.step('验证维度编码为只读文本且格式为 AI 前缀全大写', async () => {
      await expect(dimPage.codeText).toHaveText(/^AI[A-Z0-9_]*$/);
    });
    await test.step('验证所属模块与数据来源为只读标签', async () => {
      await expect(dimPage.moduleText).toHaveText(EXISTING.MODULE_AI_USAGE);
      await expect(dimPage.dataSourceText).toHaveText('对话分析');
    });
  });

  test('启用态维度删除按钮禁用', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);
    const leafName = uniqueSuffix();
    cleanupName = leafName;

    await test.step('进入配置页新建启用态测试维度', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: leafName,
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
      await dialog.submit();
      await dimPage.waitForLeafFormLoaded(leafName);
    });
    await test.step('验证新建维度启用态且删除按钮禁用', async () => {
      await expect(dimPage.enabledSwitch).toBeChecked();
      await expect(dimPage.deleteButton).toBeDisabled();
    });
    await test.step('测试后自清理：停用并删除测试维度', async () => {
      await dimPage.enabledSwitch.click();
      await dimPage.saveLeafConfig();
      await expect(dimPage.deleteButton).toBeEnabled();
      await dimPage.deleteButton.click();
      await dimPage.deleteConfirmOkButton.click();
      await expect(dimPage.leafNode(leafName)).toBeHidden();
    });
  });
});

// ============================================================
// 第二部分：新增维度（类型 D3：id 服务端生成，afterAll 按名称定位软删）
// ============================================================
test.describe.serial('新增维度弹窗', () => {
  // 本组可能创建多个维度（下边界 2 字符、上边界 30 字符、成功用例），全量收集统一清理
  const createdNames: string[] = [];

  test.afterAll(async () => {
    const request = await authedRequest();
    if (!request || createdNames.length === 0) return;
    // 按名称定位 id 软删（树中含分组嵌套）；已删则查不到，天然幂等
    for (const node of await findTreeNodes(request)) {
      if (createdNames.includes(node.name)) await disableAndDelete(request, node.id);
    }
    await request.dispose();
  });

  test('必填校验：名称为空提交被拒', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('维度名称留空，其余必填项填齐', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: '',
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
    });
    await test.step('点击确认新增', async () => {
      await dialog.submit();
    });
    await test.step('验证字段内联报错且弹窗未关闭', async () => {
      // i18n: create.nameRequired/nameMinLength（zh.json）。限定「至少/不超过」开头的报错行，避开字段标签本身
      await expect(dialog.dialog.getByText(/维度名称(至少|不超过|不合法)/)).toBeVisible();
      await expect(dialog.dialog).toBeVisible();
    });
  });

  test('必填校验：评分锚点为空提交被拒', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('锚点留空，其余必填项填齐', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: uniqueSuffix(),
        prompt: VALID_CREATE.prompt,
        anchor: '',
      });
    });
    await test.step('点击确认新增', async () => {
      await dialog.submit();
    });
    await test.step('验证锚点内联报错且弹窗未关闭', async () => {
      // i18n: create.anchorRequired = 「评分锚点必填」（zh.json）
      await expect(dialog.dialog.getByText('评分锚点必填')).toBeVisible();
      await expect(dialog.dialog).toBeVisible();
    });
  });

  test('必填校验：对话分析维度提示词为空提交被拒', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('模块选 AI 使用能力（联动对话分析），提示词留空', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await expect(dialog.promptInput).toBeVisible();
      await dialog.fillForm({
        name: uniqueSuffix(),
        prompt: '',
        anchor: VALID_CREATE.anchor,
      });
    });
    await test.step('点击确认新增', async () => {
      await dialog.submit();
    });
    await test.step('验证提示词内联报错且弹窗未关闭', async () => {
      // i18n: create.promptRequired（zh.json）
      await expect(dialog.dialog.getByText('对话分析维度必须填写评分提示词')).toBeVisible();
      await expect(dialog.dialog).toBeVisible();
    });
  });

  test('提示词字段仅对话分析维度显示', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('模块切换为 AI 管理能力（主动测试）', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_MGMT);
    });
    await test.step('验证提示词字段不显示且展示题库维护说明', async () => {
      await expect(dialog.promptInput).toBeHidden();
      await expect(dialog.dialog.getByText(/题库管理维护/)).toBeVisible();
    });
  });

  test('四模块联动默认值矩阵', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('切换使用活跃度：规则统计/权重 0 禁用/不参与总览', async () => {
      await dialog.selectModule(EXISTING.MODULE_ACTIVITY);
      // 数据来源 disabled select：trigger span 与原生 option 都含「规则统计」，取 combobox 断言
      await expect(dialog.dataSourceTrigger).toContainText('规则统计');
      await expect(dialog.weightSpinbutton).toBeDisabled();
      await expect(dialog.includeOverviewSwitch).toBeDisabled();
    });
    await test.step('切换九型人格：主动测试/权重 0 禁用/不参与总览/参考性提示', async () => {
      await dialog.selectModule(EXISTING.MODULE_ENNEAGRAM);
      await expect(dialog.dataSourceTrigger).toContainText('主动测试');
      await expect(dialog.weightSpinbutton).toBeDisabled();
      await expect(dialog.includeOverviewSwitch).toBeDisabled();
      await expect(dialog.dialog.getByText(/参考性维度，不进入硬性聚合/)).toBeVisible();
    });
    await test.step('切换 AI 管理能力：主动测试/权重 5/参与总览', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_MGMT);
      await expect(dialog.dataSourceTrigger).toContainText('主动测试');
      await expect(dialog.weightSpinbutton).toHaveValue('5');
      await expect(dialog.includeOverviewSwitch).toBeEnabled();
      await expect(dialog.includeOverviewSwitch).toBeChecked();
    });
    await test.step('切换 AI 使用能力：对话分析/权重 5/参与总览/分组下拉出现', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await expect(dialog.dataSourceTrigger).toContainText('对话分析');
      await expect(dialog.weightSpinbutton).toHaveValue('5');
      await expect(dialog.includeOverviewSwitch).toBeChecked();
      await expect(dialog.groupTrigger).toBeVisible();
    });
    await test.step('取消关闭弹窗', async () => {
      await dialog.cancelButton.click();
      await expect(dialog.dialog).toBeHidden();
    });
  });

  test('维度名称下边界：1 字符拒绝 2 字符通过', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);
    const two = uniqueSuffix().slice(0, 2);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('输入 1 字符名称提交', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({ name: '边', prompt: VALID_CREATE.prompt, anchor: VALID_CREATE.anchor });
      await dialog.submit();
    });
    await test.step('验证 1 字符被拒且弹窗保留', async () => {
      // i18n: create.nameMinLength = 「维度名称至少 2 个字符」
      await expect(dialog.dialog.getByText('维度名称至少 2 个字符')).toBeVisible();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('改为 2 字符名称提交成功', async () => {
      await dialog.nameInput.fill(two);
      await dialog.submit();
      await expect(dialog.dialog).toBeHidden();
      createdNames.push(two);
    });
    await test.step('验证树中出现新维度并自动选中', async () => {
      await dimPage.waitForLeafFormLoaded(two);
    });
  });

  test('维度名称上边界：31 字符拒绝 30 字符通过', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);
    const thirty = repeat(NAME_BOUNDARY_CHAR, BOUNDARY.NAME_MAX);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('输入 31 字符名称提交', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: repeat(NAME_BOUNDARY_CHAR, BOUNDARY.NAME_OVER),
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
      await dialog.submit();
    });
    await test.step('验证 31 字符被拒且弹窗保留', async () => {
      // i18n: create.nameMaxLength = 「维度名称不超过 30 个字符」
      await expect(dialog.dialog.getByText('维度名称不超过 30 个字符')).toBeVisible();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('改为 30 字符名称提交成功', async () => {
      await dialog.nameInput.fill(thirty);
      await dialog.submit();
      await expect(dialog.dialog).toBeHidden();
      createdNames.push(thirty);
    });
    await test.step('验证树中出现 30 字符新维度', async () => {
      await dimPage.waitForLeafFormLoaded(thirty);
    });
  });

  test('新增维度成功：编码自动生成并自动选中', async ({ page }) => {
    const dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);
    const createdName = uniqueSuffix();
    createdNames.push(createdName);

    await test.step('进入配置页打开新增维度弹窗', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      await expect(dialog.dialog).toBeVisible();
    });
    await test.step('填写 AI 使用能力维度完整信息', async () => {
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.selectGroup(EXISTING.GROUP_BASE);
      await dialog.fillForm({
        name: createdName,
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
        description: VALID_CREATE.description,
      });
    });
    await test.step('点击确认新增', async () => {
      await dialog.submit();
    });
    await test.step('验证弹窗关闭树中出现新维度并自动选中', async () => {
      await expect(dialog.dialog).toBeHidden();
      await dimPage.waitForLeafFormLoaded(createdName);
    });
    await test.step('验证维度编码只读且为 AI 前缀全大写下划线格式', async () => {
      await expect(dimPage.codeText).toHaveText(/^AI[A-Z0-9_]+$/);
    });
  });
});

// ============================================================
// 第三部分：编辑与保存（既有数据 beforeAll 备份 afterAll 恢复）
// ============================================================
test.describe.serial('编辑维度与保存', () => {
  let dimPage: DimensionConfigPage;
  // 本组自建临时维度（E2E_EDIT_ 前缀），不碰既有数据，失败后 afterAll 兜底删除
  let tempName = '';

  // 基线自愈：清空名称/锚点两个用例直接操作既有维度「23」，跑前须存在
  test.beforeAll(async () => {
    const request = await authedRequest();
    if (!request) return;
    await ensureBaselineDimension(request);
    await request.dispose();
  });

  test.afterAll(async () => {
    const request = await authedRequest();
    if (!request || !tempName) return;
    for (const node of await findTreeNodes(request)) {
      if (node.name === tempName || node.name.startsWith('E2E_EDIT_')) {
        await disableAndDelete(request, node.id);
      }
    }
    await request.dispose();
  });

  test('编辑维度名称与权重保存并持久化', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页新建临时维度', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      tempName = `E2E_EDIT_${uniqueSuffix()}`;
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: tempName,
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
      await dialog.submit();
      // 已知系统 Bug：新建后自动选中被树 refetch 间隙的 BR2 兜底覆盖，表单停留旧维度；
      // waitForLeafFormLoaded 内以手工点选绕开，须等表单切到新维度再编辑，防止改错维度
      await dimPage.waitForLeafFormLoaded(tempName);
    });
    await test.step('修改名称、权重、维度说明', async () => {
      await dimPage.fillLeafForm({
        name: `${tempName}_改`,
        weight: 60,
        description: 'E2E 编辑后的说明',
      });
    });
    await test.step('点击保存配置', async () => {
      await dimPage.saveButton.click();
    });
    await test.step('验证二次确认弹窗文案', async () => {
      await expect(dimPage.saveConfirmDialog).toBeVisible();
      await expect(dimPage.saveConfirmDialog).toContainText('下一次评估周期生效');
    });
    await test.step('确认保存', async () => {
      await dimPage.saveConfirmOkButton.click();
    });
    await test.step('验证保存成功提示', async () => {
      // i18n: leaf.toastSaved = 「已保存，下次评估生效」
      await expect(toast(page, '已保存，下次评估生效')).toBeVisible();
    });
    await test.step('刷新页面验证修改持久化', async () => {
      await dimPage.goto();
      await expect(dimPage.leafNode(`${tempName}_改`)).toBeVisible();
      await dimPage.selectLeaf(`${tempName}_改`);
      await expect(dimPage.nameInput).toHaveValue(`${tempName}_改`);
      await expect(dimPage.weightSpinbutton).toHaveValue('60');
    });
  });

  test('编辑表单清空名称保存被拒', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中既有叶子维度（只读操作前不修改它）', async () => {
      await dimPage.goto();
      await dimPage.selectLeaf(EXISTING.LEAF_NAME);
    });
    await test.step('清空维度名称', async () => {
      await dimPage.nameInput.fill('');
    });
    await test.step('点击保存配置', async () => {
      await dimPage.saveButton.click();
    });
    await test.step('验证内联报错且二次确认不弹出', async () => {
      // i18n: leaf.nameMinLength（清空触发的下界校验）
      await expect(page.getByText('维度名称至少 2 个字符')).toBeVisible();
      await expect(dimPage.saveConfirmDialog).toBeHidden();
    });
  });

  test('编辑表单清空评分锚点保存被拒', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中既有叶子维度', async () => {
      await dimPage.goto();
      await dimPage.selectLeaf(EXISTING.LEAF_NAME);
    });
    await test.step('清空评分锚点', async () => {
      await dimPage.anchorInput.fill('');
    });
    await test.step('点击保存配置', async () => {
      await dimPage.saveButton.click();
    });
    await test.step('验证内联报错且二次确认不弹出', async () => {
      // i18n: leaf.anchorRequired = 「评分锚点必填」
      await expect(page.getByText('评分锚点必填')).toBeVisible();
      await expect(dimPage.saveConfirmDialog).toBeHidden();
    });
  });

  test('聚合权重边界：0 与 100 可保存', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页新建临时维度', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      tempName = `E2E_W_${uniqueSuffix()}`;
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: tempName,
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
      await dialog.submit();
      await dimPage.waitForLeafFormLoaded(tempName);
    });
    await test.step('权重填 0 并保存', async () => {
      await dimPage.fillLeafForm({ weight: 0 });
      await dimPage.saveLeafConfig();
      await expect(dimPage.weightSpinbutton).toHaveValue('0');
    });
    await test.step('权重填 100 并保存', async () => {
      await dimPage.fillLeafForm({ weight: 100 });
      await dimPage.saveLeafConfig();
      await expect(dimPage.weightSpinbutton).toHaveValue('100');
    });
  });

  test('重置回滚到上次保存值且不调接口', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);
    let savedName = '';
    let savedWeight = '';

    await test.step('进入配置页选中既有叶子维度并记录保存值', async () => {
      await dimPage.goto();
      await dimPage.selectLeaf(EXISTING.LEAF_NAME);
      savedName = await dimPage.nameInput.inputValue();
      savedWeight = await dimPage.weightSpinbutton.inputValue();
    });
    await test.step('修改名称与权重', async () => {
      await dimPage.fillLeafForm({ name: 'E2E_RESET_临时改', weight: 99 });
    });
    await test.step('点击重置', async () => {
      await dimPage.resetButton.click();
    });
    await test.step('验证表单回滚到上次保存值', async () => {
      await expect(dimPage.nameInput).toHaveValue(savedName);
      await expect(dimPage.weightSpinbutton).toHaveValue(savedWeight);
    });
  });
});

// ============================================================
// 第四部分：活跃度规则（系统单例，beforeAll 备份 afterAll 恢复）
// ============================================================
test.describe.serial('活跃度规则配置', () => {
  let dimPage: DimensionConfigPage;

  test.afterAll(async () => {
    const request = await authedRequest();
    if (!request) return;
    await request.post('/api/dimensions/activity-rule/save', {
      data: {
        active_threshold: ACTIVITY_RULE.DEFAULT_ACTIVE,
        low_frequency_threshold: ACTIVITY_RULE.DEFAULT_LOW,
      },
    });
    await request.dispose();
  });

  test('保存活跃度规则并持久化', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中使用活跃度模块并记录初始值', async () => {
      await dimPage.goto();
      await dimPage.selectModule(EXISTING.MODULE_ACTIVITY);
      await expect(dimPage.activeThresholdInput).toHaveValue(String(ACTIVITY_RULE.DEFAULT_ACTIVE));
      await expect(dimPage.lowThresholdInput).toHaveValue(String(ACTIVITY_RULE.DEFAULT_LOW));
    });
    await test.step('修改活跃下限 20 低频下限 8', async () => {
      await dimPage.activeThresholdInput.fill(String(ACTIVITY_RULE.TEST_ACTIVE));
      await dimPage.lowThresholdInput.fill(String(ACTIVITY_RULE.TEST_LOW));
    });
    await test.step('验证判定关系文案实时更新', async () => {
      await expect(dimPage.ruleSummaryText).toContainText(
        `≥${ACTIVITY_RULE.TEST_ACTIVE} 判活跃、${ACTIVITY_RULE.TEST_LOW}~${ACTIVITY_RULE.TEST_ACTIVE - 1} 判低频、<${ACTIVITY_RULE.TEST_LOW} 判未使用`,
      );
    });
    await test.step('点击保存规则并在二次确认弹窗确认', async () => {
      await dimPage.saveRuleButton.click();
      await expect(dimPage.saveConfirmDialog).toBeVisible();
      await dimPage.saveConfirmOkButton.click();
    });
    await test.step('刷新页面验证阈值持久化', async () => {
      await dimPage.goto();
      await dimPage.selectModule(EXISTING.MODULE_ACTIVITY);
      await expect(dimPage.activeThresholdInput).toHaveValue(String(ACTIVITY_RULE.TEST_ACTIVE));
      await expect(dimPage.lowThresholdInput).toHaveValue(String(ACTIVITY_RULE.TEST_LOW));
    });
  });

  test('低频下限须小于活跃下限：相等被拒', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中使用活跃度模块', async () => {
      await dimPage.goto();
      await dimPage.selectModule(EXISTING.MODULE_ACTIVITY);
    });
    await test.step('活跃与低频均填 10', async () => {
      await dimPage.activeThresholdInput.fill('10');
      await dimPage.lowThresholdInput.fill('10');
    });
    await test.step('点击保存规则', async () => {
      await dimPage.saveRuleButton.click();
    });
    await test.step('验证校验拒绝且二次确认不弹出', async () => {
      // i18n: activity.lowLessThanActive = 「低频判定下限须小于活跃判定下限」
      await expect(page.getByText('低频判定下限须小于活跃判定下限')).toBeVisible();
      await expect(dimPage.saveConfirmDialog).toBeHidden();
    });
  });

  test('重置规则回滚到上次保存值', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);

    await test.step('进入配置页选中使用活跃度模块', async () => {
      await dimPage.goto();
      await dimPage.selectModule(EXISTING.MODULE_ACTIVITY);
    });
    await test.step('修改阈值', async () => {
      await dimPage.activeThresholdInput.fill('500');
      await dimPage.lowThresholdInput.fill('400');
    });
    await test.step('点击重置规则', async () => {
      await dimPage.resetRuleButton.click();
    });
    await test.step('验证阈值回滚到上次保存值', async () => {
      await expect(dimPage.activeThresholdInput).toHaveValue(String(ACTIVITY_RULE.TEST_ACTIVE));
      await expect(dimPage.lowThresholdInput).toHaveValue(String(ACTIVITY_RULE.TEST_LOW));
    });
  });
});

// ============================================================
// 第五部分：停用→删除全链路（场景内自建自清理）
// ============================================================
test.describe.serial('停用删除全链路', () => {
  let dimPage: DimensionConfigPage;
  let leafName: string;

  test.afterAll(async () => {
    const request = await authedRequest();
    if (!request || !leafName) return;
    // 已删则树查不到，天然幂等
    for (const node of await findTreeNodes(request)) {
      if (node.name === leafName) await disableAndDelete(request, node.id);
    }
    await request.dispose();
  });

  test('新建维度后停用删除全链路', async ({ page }) => {
    dimPage = new DimensionConfigPage(page);
    const dialog = new CreateDimensionDialog(page);

    await test.step('进入配置页新建测试维度', async () => {
      await dimPage.goto();
      await dimPage.createButton.click();
      leafName = uniqueSuffix();
      await dialog.selectModule(EXISTING.MODULE_AI_USAGE);
      await dialog.fillForm({
        name: leafName,
        prompt: VALID_CREATE.prompt,
        anchor: VALID_CREATE.anchor,
      });
      await dialog.submit();
      await dimPage.waitForLeafFormLoaded(leafName);
    });
    await test.step('验证启用态删除按钮禁用', async () => {
      await expect(dimPage.enabledSwitch).toBeChecked();
      await expect(dimPage.deleteButton).toBeDisabled();
    });
    await test.step('切换是否启用开关并保存配置', async () => {
      await dimPage.enabledSwitch.click();
      await dimPage.saveButton.click();
      await dimPage.saveConfirmOkButton.click();
      await expect(dimPage.deleteButton).toBeEnabled();
    });
    await test.step('验证树节点出现已停用标记', async () => {
      await expect(
        dimPage.treeNav.getByRole('button', { name: new RegExp(escapeRe(leafName) + ' 已停用') }),
      ).toBeVisible();
    });
    await test.step('点击删除维度弹出 popconfirm', async () => {
      await dimPage.deleteButton.click();
      await expect(dimPage.deleteConfirmDialog).toBeVisible();
      await expect(dimPage.deleteConfirmDialog).toContainText('历史评分保留');
      await expect(dimPage.deleteConfirmDialog).toContainText('不可恢复');
    });
    await test.step('确认删除', async () => {
      await dimPage.deleteConfirmOkButton.click();
    });
    await test.step('验证树中节点消失且右侧切换选中', async () => {
      await expect(dimPage.leafNode(leafName)).toBeHidden();
      await expect(dimPage.nameInput).toBeVisible();
    });
  });
});
