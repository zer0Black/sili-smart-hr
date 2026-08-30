import { type Page, type Locator, expect } from '@playwright/test';

/**
 * 维度与权重配置页页面对象（/system/dimension）。
 * 单页双栏：左树（四模块三层）+ 右侧三形态（模块/分组汇总、活跃度规则、叶子配置表单）。
 * 定位器来自 playwright-cli 探索快照（2026-08-16）与 feature 源码 features/dimension/。
 */

export class DimensionConfigPage {
  readonly page: Page;

  // 顶部信息条与标题
  readonly heading: Locator;
  readonly effectNotice: Locator;

  // 左侧维度树
  readonly searchInput: Locator;
  readonly createButton: Locator;
  readonly treeNav: Locator;

  // 叶子维度配置表单（形态 C）
  readonly nameInput: Locator;
  readonly promptInput: Locator;
  readonly anchorInput: Locator;
  readonly weightSpinbutton: Locator;
  readonly weightSlider: Locator;
  readonly includeOverviewSwitch: Locator;
  readonly enabledSwitch: Locator;
  readonly descriptionInput: Locator;
  readonly resetButton: Locator;
  readonly saveButton: Locator;
  readonly deleteButton: Locator;

  // 只读标签（维度编码 / 所属模块 / 数据来源，探索确认无输入控件）
  readonly codeText: Locator;
  readonly moduleText: Locator;
  readonly dataSourceText: Locator;

  // 活跃度规则面板（形态 B）
  readonly activeThresholdInput: Locator;
  readonly lowThresholdInput: Locator;
  readonly ruleSummaryText: Locator;
  readonly resetRuleButton: Locator;
  readonly saveRuleButton: Locator;

  // 模块汇总视图（形态 A）
  readonly weightSummaryText: Locator;
  readonly weightDeviationText: Locator;
  readonly summaryTable: Locator;

  // 保存二次确认弹窗（配置与活跃度规则两种标题，按钮文案相同）
  readonly saveConfirmDialog: Locator;
  readonly saveConfirmOkButton: Locator;

  // 删除 popconfirm（radix AlertDialog）
  readonly deleteConfirmDialog: Locator;
  readonly deleteConfirmOkButton: Locator;

  constructor(page: Page) {
    this.page = page;

    this.heading = page.getByRole('heading', { name: '维度与权重', exact: true });
    this.effectNotice = page.getByText('所有配置改动在下次评估跑批时生效');

    this.searchInput = page.getByRole('textbox', { name: '搜索维度名称' });
    // 新增维度按钮有两个入口（树顶部 + 未选中空状态区，spec 4.1.3），取树顶部的第一个
    this.createButton = page.getByRole('button', { name: '新增维度' }).first();
    // 树容器：顶栏也是 navigation，按树特有节点（模块标记）限定到第二个
    this.treeNav = page.getByRole('navigation').filter({ has: page.getByText('模块', { exact: true }) });

    this.nameInput = page.getByRole('textbox', { name: '维度名称', exact: true });
    this.promptInput = page.getByRole('textbox', { name: '评分提示词' });
    this.anchorInput = page.getByRole('textbox', { name: '评分锚点' });
    // 权重数字输入：无 id 无 aria-label，用「聚合权重」标签的父行限定（源码 leaf-config-form.tsx:298）
    this.weightSpinbutton = page.getByText('聚合权重', { exact: true }).locator('..').locator('input[type="number"]');
    this.weightSlider = page.getByRole('slider');
    this.includeOverviewSwitch = page.getByRole('switch', { name: '参与总览分' });
    this.enabledSwitch = page.getByRole('switch', { name: '是否启用' });
    this.descriptionInput = page.getByRole('textbox', { name: '维度说明' });
    this.resetButton = page.getByRole('button', { name: '重置', exact: true });
    this.saveButton = page.getByRole('button', { name: '保存配置' });
    this.deleteButton = page.getByRole('button', { name: '删除维度' });

    // 只读标签：标签文本 + 相邻 generic 值。限定主区域避免与弹窗串扰。
    this.codeText = page.getByText('维度编码', { exact: true }).locator('..').getByText(/^[A-Z][A-Z0-9_]*$/);
    this.moduleText = page.getByText('所属模块', { exact: true }).locator('..').getByText(/^(使用活跃度|AI 使用能力|AI 管理能力|九型人格)$/);
    this.dataSourceText = page.getByText('数据来源', { exact: true }).locator('..').getByText(/^(对话分析|主动测试|规则统计)$/);

    this.activeThresholdInput = page.getByRole('spinbutton', { name: '活跃判定下限' });
    this.lowThresholdInput = page.getByRole('spinbutton', { name: '低频判定下限' });
    this.ruleSummaryText = page.getByText(/当前规则：/);
    this.resetRuleButton = page.getByRole('button', { name: '重置规则' });
    this.saveRuleButton = page.getByRole('button', { name: '保存规则' });

    this.weightSummaryText = page.getByText('权重合计', { exact: true }).locator('..').getByText(/^\d+%$/);
    this.weightDeviationText = page.getByText(/偏离 100%/);
    this.summaryTable = page.getByRole('table');

    this.saveConfirmDialog = page.getByRole('dialog', { name: /确认保存(配置|活跃度规则)？/ });
    this.saveConfirmOkButton = this.saveConfirmDialog.getByRole('button', { name: '确认保存' });

    this.deleteConfirmDialog = page.getByRole('alertdialog', { name: '确认删除该维度？' });
    this.deleteConfirmOkButton = this.deleteConfirmDialog.getByRole('button', { name: '确认删除' });
  }

  async goto(): Promise<void> {
    await this.page.goto('/system/dimension');
    await this.treeNav.waitFor();
  }

  /** 树节点：模块（名称含「模块」标记） */
  moduleNode(name: string): Locator {
    return this.treeNav.getByRole('button', { name: new RegExp(`^${escapeRe(name)}( 参考)? 模块$`) });
  }

  /** 树节点：分组 */
  groupNode(name: string): Locator {
    return this.treeNav.getByRole('button', { name: new RegExp(`^${escapeRe(name)} 分组$`) });
  }

  /** 树节点：叶子（名称 + 可选「已停用」标记 + 权重徽标） */
  leafNode(name: string): Locator {
    return this.treeNav.getByRole('button', { name: new RegExp(`^${escapeRe(name)}( 已停用)?( 参考)? \\d+%$`) });
  }

  /** 叶子节点权重徽标值（数字）。名称可含数字，textContent 拼接会污染解析，故取节点内独立的百分比子元素 */
  async leafWeight(name: string): Promise<number> {
    const badge = await this.leafNode(name).getByText(/\d+%/).textContent();
    return Number((badge ?? '').replace(/[^0-9]/g, ''));
  }

  async selectModule(name: string): Promise<void> {
    await this.moduleNode(name).click();
  }

  async selectGroup(name: string): Promise<void> {
    await this.groupNode(name).click();
  }

  async selectLeaf(name: string): Promise<void> {
    await this.leafNode(name).click();
    // 表单数据异步加载，点选后等表单切换到目标维度，避免后续操作落在旧表单上
    await expect(this.nameInput).toHaveValue(name);
  }

  /**
   * 新建维度提交后的就绪等待：树节点出现后手工点选。
   * 注意：系统存在已知 Bug（新建后自动选中被树 refetch 间隙的 BR2 兜底覆盖，表单停留旧维度），
   * 测试以手工点选绕开，Bug 单独记录在测试报告。
   */
  async waitForLeafFormLoaded(name: string): Promise<void> {
    await expect(this.leafNode(name)).toBeVisible();
    await this.leafNode(name).click();
    await expect(this.nameInput).toHaveValue(name);
  }

  /** 填写叶子表单（undefined 字段跳过） */
  async fillLeafForm(data: {
    name?: string;
    prompt?: string;
    anchor?: string;
    weight?: number;
    description?: string;
  }): Promise<void> {
    if (data.name !== undefined) await this.nameInput.fill(data.name);
    if (data.prompt !== undefined) await this.promptInput.fill(data.prompt);
    if (data.anchor !== undefined) await this.anchorInput.fill(data.anchor);
    if (data.weight !== undefined) await this.weightSpinbutton.fill(String(data.weight));
    if (data.description !== undefined) await this.descriptionInput.fill(data.description);
  }

  /** 保存配置全链路：点击 → 二次确认 → 确认保存（确认按钮文案两种弹窗相同） */
  async saveLeafConfig(): Promise<void> {
    await this.saveButton.click();
    await this.saveConfirmOkButton.click();
  }

  /** 活跃度规则保存全链路（弹窗标题不同，按钮同） */
  async saveActivityRule(): Promise<void> {
    await this.saveRuleButton.click();
    await this.saveConfirmOkButton.click();
  }
}

/**
 * 新增维度弹窗页面对象。shadcn Select：点击 trigger 展开原生 listbox，再点 option。
 */
export class CreateDimensionDialog {
  readonly dialog: Locator;
  readonly nameInput: Locator;
  readonly moduleTrigger: Locator;
  readonly groupTrigger: Locator;
  readonly dataSourceTrigger: Locator;
  readonly promptInput: Locator;
  readonly anchorInput: Locator;
  readonly weightSpinbutton: Locator;
  readonly includeOverviewSwitch: Locator;
  readonly descriptionInput: Locator;
  readonly cancelButton: Locator;
  readonly submitButton: Locator;
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
    this.dialog = page.getByRole('dialog', { name: '新增维度' });
    this.nameInput = this.dialog.getByRole('textbox', { name: '维度名称' });
    this.moduleTrigger = this.dialog.getByRole('combobox', { name: '所属模块' });
    this.groupTrigger = this.dialog.getByRole('combobox', { name: '所属分组' });
    // 数据来源：disabled radix Select 丢失 accessible name（trace 快照实证），按「数据来源」标签的容器断言
    this.dataSourceTrigger = this.dialog
      .getByText('数据来源', { exact: true })
      .locator('..')
      .getByRole('combobox');
    this.promptInput = this.dialog.getByRole('textbox', { name: '评分提示词' });
    this.anchorInput = this.dialog.getByRole('textbox', { name: '评分锚点' });
    this.weightSpinbutton = this.dialog.getByRole('spinbutton').first();
    this.includeOverviewSwitch = this.dialog.getByRole('switch', { name: '参与总览分' });
    this.descriptionInput = this.dialog.getByRole('textbox', { name: '维度说明' });
    this.cancelButton = this.dialog.getByRole('button', { name: '取消' });
    this.submitButton = this.dialog.getByRole('button', { name: '确认新增' });
  }

  /** 选择所属模块（shadcn Select 交互） */
  async selectModule(option: string): Promise<void> {
    await this.moduleTrigger.click();
    await this.page.getByRole('option', { name: option, exact: true }).click();
  }

  /** 选择所属分组（仅 AI 使用能力模块渲染） */
  async selectGroup(option: string): Promise<void> {
    await this.groupTrigger.click();
    await this.page.getByRole('option', { name: option, exact: true }).click();
  }

  async fillForm(data: {
    name: string;
    anchor?: string;
    prompt?: string;
    weight?: number;
    description?: string;
  }): Promise<void> {
    await this.nameInput.fill(data.name);
    if (data.prompt !== undefined) await this.promptInput.fill(data.prompt);
    if (data.anchor !== undefined) await this.anchorInput.fill(data.anchor);
    if (data.weight !== undefined) await this.weightSpinbutton.fill(String(data.weight));
    if (data.description !== undefined) await this.descriptionInput.fill(data.description);
  }

  async submit(): Promise<void> {
    await this.submitButton.click();
  }
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
