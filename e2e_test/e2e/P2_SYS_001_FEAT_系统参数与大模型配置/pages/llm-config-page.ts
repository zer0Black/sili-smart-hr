import { type Page, type Locator } from '@playwright/test';

/**
 * 大模型配置页页面对象（/system/llm）。
 * 覆盖：模型列表 + 搜索 + 新增/编辑模型弹窗 + 查看模型弹窗 + 删除二次确认 + 集成密钥卡片。
 * 定位器来自 playwright-cli 探索快照与 llm/index.tsx + zh.json(llmConfig) 源码。
 */
export class LlmConfigPage {
  readonly page: Page;

  // 列表区
  readonly searchInput: Locator;
  readonly addButton: Locator;
  readonly table: Locator;
  readonly emptyState: Locator;

  // 新增/编辑模型弹窗
  readonly formDialog: Locator;
  readonly modelNameInput: Locator;
  readonly providerTrigger: Locator;
  readonly modelIdInput: Locator;
  readonly apiUrlInput: Locator;
  readonly apiKeyInput: Locator;
  readonly apiKeyToggle: Locator;
  readonly formSubmitButton: Locator;
  readonly formCancelButton: Locator;

  // 查看模型弹窗
  readonly viewDialog: Locator;

  // 删除确认弹窗
  readonly deleteDialog: Locator;
  readonly deleteConfirmButton: Locator;

  // 集成密钥卡片：按钮按文案直接定位（未配置态「配置」/ 已配置态「更新」）
  readonly secretConfigButton: Locator; // 未配置态「配置」/ 已配置态「更新」
  readonly secretRevealButton: Locator; // 显示明文
  readonly secretConnectButton: Locator; // 连通验证
  readonly secretUpdateDialog: Locator;
  readonly secretValueInput: Locator;
  readonly secretSubmitButton: Locator;

  constructor(page: Page) {
    this.page = page;

    this.searchInput = page.getByRole('textbox', { name: '搜索模型名称或模型 ID' });
    this.addButton = page.getByRole('button', { name: '新增模型' });
    this.table = page.getByRole('table');
    this.emptyState = page.getByText('暂无模型');

    // 弹窗统一用 dialog 容器限定，避免与页面其他元素串扰
    this.formDialog = page.getByRole('dialog', { name: /新增模型|编辑模型/ });
    this.modelNameInput = this.formDialog.getByRole('textbox', { name: '模型名称' });
    this.providerTrigger = this.formDialog.getByRole('combobox', { name: '服务商' });
    this.modelIdInput = this.formDialog.getByRole('textbox', { name: '模型 ID' });
    this.apiUrlInput = this.formDialog.getByRole('textbox', { name: 'API 地址' });
    this.apiKeyInput = this.formDialog.getByRole('textbox', { name: 'API Key' });
    this.apiKeyToggle = this.formDialog.getByRole('button', { name: /toggle api key visibility/i });
    this.formSubmitButton = this.formDialog.getByRole('button', { name: /确认新增|保存修改/ });
    this.formCancelButton = this.formDialog.getByRole('button', { name: '取消' });

    this.viewDialog = page.getByRole('dialog', { name: /查看模型/ });

    // 删除弹窗是 AlertDialog（radix alert-dialog），ARIA role 是 alertdialog 而非 dialog，
    // getByRole('dialog') 匹配不到会稳定超时。改用 data-slot 定位，绕开 role 歧义，name 断言交由调用方。
    this.deleteDialog = page.locator('[data-slot="alert-dialog-content"]');
    this.deleteConfirmButton = this.deleteDialog.getByRole('button', { name: '确认删除' });

    // 集成密钥卡片：按钮按文案直接定位（未配置态「配置」/ 已配置态「更新」）
    this.secretConfigButton = page.getByRole('button', { name: /^(配置|更新)$/ });
    this.secretRevealButton = page.getByRole('button', { name: /显示明文|隐藏明文/ });
    this.secretConnectButton = page.getByRole('button', { name: '连通验证' });
    this.secretUpdateDialog = page.getByRole('dialog', { name: /配置集成密钥|集成密钥更新/ });
    this.secretValueInput = this.secretUpdateDialog.getByRole('textbox', { name: '新集成密钥' });
    this.secretSubmitButton = this.secretUpdateDialog.getByRole('button', { name: /确认配置|确认更新/ });
  }

  async goto(): Promise<void> {
    await this.page.goto('/system/llm');
    await this.page.waitForLoadState('networkidle');
    await this.page.getByRole('heading', { name: '大模型配置' }).waitFor({ state: 'visible' });
  }

  // 打开新增弹窗
  async clickAdd(): Promise<void> {
    await this.addButton.click();
    await this.formDialog.waitFor({ state: 'visible' });
  }

  // 填写模型表单（新增/编辑共用）。data 含 name/provider/modelId/apiUrl/apiKey。
  async fillModelForm(data: {
    name: string;
    provider: string;
    modelId: string;
    apiUrl: string;
    apiKey: string;
  }): Promise<void> {
    await this.modelNameInput.fill(data.name);
    // 服务商 Select
    await this.providerTrigger.click();
    await this.page.getByRole('option', { name: data.provider, exact: true }).click();
    await this.modelIdInput.fill(data.modelId);
    await this.apiUrlInput.fill(data.apiUrl);
    await this.apiKeyInput.fill(data.apiKey);
  }

  async submitForm(): Promise<void> {
    await this.formSubmitButton.click();
  }

  async cancelForm(): Promise<void> {
    await this.formCancelButton.click();
  }

  // 行内操作：按模型名称定位行
  rowByName(name: string): Locator {
    return this.table.getByRole('row', { name: name });
  }

  async clickRowView(name: string): Promise<void> {
    await this.rowByName(name).getByRole('button', { name: '查看', exact: true }).click();
    await this.viewDialog.waitFor({ state: 'visible' });
  }

  async clickRowEdit(name: string): Promise<void> {
    await this.rowByName(name).getByRole('button', { name: '编辑', exact: true }).click();
    await this.formDialog.waitFor({ state: 'visible' });
  }

  async clickRowDelete(name: string): Promise<void> {
    await this.rowByName(name).getByRole('button', { name: '删除', exact: true }).click();
    await this.deleteDialog.waitFor({ state: 'visible' });
  }

  async confirmDelete(): Promise<void> {
    await this.deleteConfirmButton.click();
  }

  // 行内 API Key 显隐
  rowRevealButton(name: string): Locator {
    return this.rowByName(name).getByRole('button', { name: '查看 API Key 明文' });
  }

  // 启用开关
  rowSwitch(name: string): Locator {
    return this.rowByName(name).getByRole('switch');
  }

  // 行内掩码文本。行里有两个 font-mono span（model_id 副标题 + API Key 掩码）class 相同，
  // 取 first 会误中 model_id。掩码与「查看 API Key 明文」按钮同处一个 flex div，用它兜底定位。
  rowMaskedKey(name: string): Locator {
    return this.rowByName(name)
      .getByRole('button', { name: '查看 API Key 明文' })
      .locator('xpath=preceding-sibling::span[1]');
  }

  async search(keyword: string): Promise<void> {
    await this.searchInput.fill(keyword);
  }

  async clearSearch(): Promise<void> {
    await this.searchInput.clear();
  }

  // 等待列表加载完成（「加载中…」消失）
  async waitForListLoaded(): Promise<void> {
    await this.page.getByText('加载中…').waitFor({ state: 'detached' }).catch(() => undefined);
  }

  // 集成密钥：首次配置/更新
  async clickSecretConfig(): Promise<void> {
    await this.secretConfigButton.first().click();
    await this.secretUpdateDialog.waitFor({ state: 'visible' });
  }

  async fillSecretAndSubmit(value: string): Promise<void> {
    await this.secretValueInput.fill(value);
    await this.secretSubmitButton.click();
  }

  async clickConnectTest(): Promise<void> {
    await this.secretConnectButton.first().click();
  }
}
