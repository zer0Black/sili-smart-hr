import { type Page, type Locator } from '@playwright/test';

/**
 * 系统参数页页面对象（/system/params）。
 * 定位器来自 playwright-cli 探索快照与 params.tsx 源码：
 * 周期长度为 shadcn Select，触发时点为 Input(id=config-trigger-time)，
 * 触发日/评估区间为 Label + 只读 div 结构，评估对象为原生 radio。
 */
export class SystemParamsPage {
  readonly page: Page;

  readonly periodTrigger: Locator; // 周期长度 Select 触发器
  readonly triggerTimeInput: Locator; // 触发时点
  readonly targetAllRadio: Locator;
  readonly targetSpecifiedRadio: Locator;
  readonly memberSelectButton: Locator;
  readonly resetButton: Locator;
  readonly saveButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.periodTrigger = page.getByRole('combobox', { name: '周期长度' });
    this.triggerTimeInput = page.locator('#config-trigger-time');
    this.targetAllRadio = page.getByRole('radio', { name: '全员' });
    this.targetSpecifiedRadio = page.getByRole('radio', { name: '指定人员' });
    this.memberSelectButton = page.getByRole('button', { name: '点击选择指定人员' });
    this.resetButton = page.getByRole('button', { name: '恢复默认' });
    this.saveButton = page.getByRole('button', { name: '保存配置' });
  }

  async goto(): Promise<void> {
    await this.page.goto('/system/params');
    await this.page.waitForLoadState('networkidle');
    await this.page.getByRole('heading', { name: '系统参数' }).waitFor({ state: 'visible' });
  }

  // 选周期长度（shadcn Select：点 trigger 展开 listbox 再点 option）
  async selectPeriod(label: string): Promise<void> {
    await this.periodTrigger.click();
    await this.page.getByRole('option', { name: label, exact: true }).click();
  }

  async fillTriggerTime(value: string): Promise<void> {
    await this.triggerTimeInput.fill(value);
  }

  async clearTriggerTime(): Promise<void> {
    await this.triggerTimeInput.fill('');
  }

  async clickSave(): Promise<void> {
    await this.saveButton.click();
  }

  async clickReset(): Promise<void> {
    await this.resetButton.click();
  }

  async periodDisplayText(): Promise<string> {
    return (await this.periodTrigger.innerText()).trim();
  }

  // 触发日只读区文本：周期与触发 Card 内的 bg-muted/30 只读 div，按出现顺序第 1 个是触发日、第 2 个是评估区间。
  // 不依赖 Label 文案定位（triggerDay/interval 是 i18n 对象 key，Label 实际渲染 i18next 报错文案）。
  // 断言处再校验文案正确性以暴露 i18n bug。
  async triggerDayDisplayText(): Promise<string> {
    const value = this.page.locator('div.bg-muted\\/30').first();
    return (await value.innerText()).trim();
  }

  async intervalDisplayText(): Promise<string> {
    const value = this.page.locator('div.bg-muted\\/30').nth(1);
    return (await value.innerText()).trim();
  }
}
