// module-meta 八类标签映射校验（specs §4.1.4 规则1 枚举 + §4.1.5 配色 + §4.2.5 互斥判据）。
import { describe, expect, it } from 'vitest';

import { hasChanges, moduleBadgeTone, moduleI18nKeys } from '../module-meta';
import type { OperationLogItem } from '../types';

/** specs §4.1.4 规则1 八类枚举（03 §3 A1 module 可选值）。 */
const EIGHT_MODULES = [
  'login',
  'account',
  'dimension',
  'system_params',
  'llm_config',
  'question_bank',
  'assessment',
  'system_job',
] as const;

const VALID_TONES = ['default', 'secondary', 'destructive', 'outline'];

function makeItem(partial: Partial<OperationLogItem>): OperationLogItem {
  return {
    id: '1790000000000001001',
    created_at: '2026-10-07 14:23:05',
    operator: '李学涛',
    module: 'dimension',
    target: '维度「任务适配判断力」聚合权重',
    summary: '调整维度聚合权重 30 → 50',
    result: 'success',
    changes: null,
    detail: '',
    ...partial,
  };
}

describe('八类 module 映射全覆盖（验收锚点）', () => {
  it('TestModuleI18nKeysEightModules：八类键逐一命中 operationLog:module.* 无 undefined', () => {
    for (const m of EIGHT_MODULES) {
      expect(moduleI18nKeys[m]).toBe(`operationLog:module.${m}`);
    }
  });

  it('TestModuleBadgeToneEightModules：八类键逐一命中合法 variant 无 undefined', () => {
    for (const m of EIGHT_MODULES) {
      expect(moduleBadgeTone[m]).toBeDefined();
      expect(VALID_TONES).toContain(moduleBadgeTone[m]);
    }
  });

  it('TestModuleMapsKeySetExact：两映射键集合与八类枚举完全一致（无多余键）', () => {
    expect(Object.keys(moduleI18nKeys).sort()).toEqual([...EIGHT_MODULES].sort());
    expect(Object.keys(moduleBadgeTone).sort()).toEqual([...EIGHT_MODULES].sort());
  });

  it('TestBadgeToneSemanticAnchors：BR1 语义色锚点（llm_config 红点缀、assessment 主色、system_job outline 弱化）', () => {
    expect(moduleBadgeTone.llm_config).toBe('destructive');
    expect(moduleBadgeTone.assessment).toBe('default');
    expect(moduleBadgeTone.system_job).toBe('outline');
    // 其余五类均为 secondary 底色，附加 text-* 由组件层拼接（specs §4.1.5）
    for (const m of ['login', 'account', 'dimension', 'system_params', 'question_bank']) {
      expect(moduleBadgeTone[m]).toBe('secondary');
    }
  });

  it('TestUnknownModuleUndefined：未知 module 值查表为 undefined（组件层自行兜底）', () => {
    expect(moduleI18nKeys.unknown_module).toBeUndefined();
    expect(moduleBadgeTone.unknown_module).toBeUndefined();
  });
});

describe('hasChanges 互斥渲染判据（specs §4.2.5，验收锚点）', () => {
  it('TestHasChangesNull：changes 为 null 时 false（渲染文本详情）', () => {
    expect(hasChanges(makeItem({ changes: null }))).toBe(false);
  });

  it('TestHasChangesEmptyArray：changes 为空数组时 false', () => {
    expect(hasChanges(makeItem({ changes: [] }))).toBe(false);
  });

  it('TestHasChangesPresent：changes 有对比项时 true（渲染对比表）', () => {
    expect(
      hasChanges(
        makeItem({ changes: [{ field: '权重', before: '30', after: '50' }] }),
      ),
    ).toBe(true);
  });

  it('TestHasChangesMultiple：多项对比数组同样 true', () => {
    expect(
      hasChanges(
        makeItem({
          changes: [
            { field: '姓名', before: '张三', after: '李四' },
            { field: '状态', before: '启用', after: '停用' },
          ],
        }),
      ),
    ).toBe(true);
  });
});
