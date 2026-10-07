// 工作台域页面侧私有类型与展示纯函数（specs P2_WRK_001 §4.1.2 D）。
// 跨域共享的接口契约在 lib/contracts.ts。

import type { WorkspaceAttentionRow } from '@/lib/contracts';

/** 模块联合缩写：attention 行与格式化参数共用。 */
export type WorkspaceModuleCode = 'AI_USAGE' | 'AI_MGMT';

/**
 * 关注原因文案拼装：weak 行列出低于 60 的各模块与短板维度，unused 行显示天数
 * （specs §4.1.2 D）。i18n 文案经 formatModule/formatDimension 注入保持纯函数可测。
 * 拼装格式：「{模块名} {score} 分（短板：{维度名×n}）」，多模块以「；」分隔；
 * unused 行：「最近活跃距今 {n} 天」。载体为空（无模块条目/无天数）返回空串。
 */
export function buildAttentionReason(
  row: WorkspaceAttentionRow,
  formatModule: (m: WorkspaceModuleCode) => string,
  formatDimension: (code: string) => string,
): string {
  if (row.category === 'unused') {
    if (row.days_since_active == null) return '';
    return `最近活跃距今 ${row.days_since_active} 天`;
  }
  return row.weak_modules
    .map((wm) => {
      const dims = wm.weak_dims.map(formatDimension).join('、');
      const weaknessPart = dims ? `（短板：${dims}）` : '';
      return `${formatModule(wm.module)} ${wm.score} 分${weaknessPart}`;
    })
    .join('；');
}
