import type { DimensionBrief } from '@/lib/contracts';

// 测试辅助：快速构造树叶子。
export function brief(code: string, overrides: Partial<DimensionBrief> = {}): DimensionBrief {
  return {
    id: code,
    code,
    name: code,
    module_code: 'AI_USAGE',
    group_code: null,
    data_source: 'CONVERSATION',
    weight: 1,
    include_overview: true,
    enabled: true,
    ...overrides,
  };
}
