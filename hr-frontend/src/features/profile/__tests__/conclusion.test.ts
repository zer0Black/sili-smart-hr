import { describe, it, expect } from 'vitest';

import type {
  DimensionTreeNode,
  ProfileDetail,
  ProfileDimensionRow,
  ProfileModuleCard,
} from '@/lib/contracts';

import {
  buildConclusion,
  CONCLUSION_LIMITED_THRESHOLD,
  STRENGTH_THRESHOLD,
  WEAKNESS_THRESHOLD,
} from '../conclusion';
import { brief } from './helpers';

function moduleCard(overrides: Partial<ProfileModuleCard> = {}): ProfileModuleCard {
  return {
    module: 'AI_USAGE',
    score: 80,
    change_vs_prev: null,
    evaluated_at: '2026-09-28',
    data_status: 'complete',
    insufficient_count: 0,
    failed_count: 0,
    missing_count: 0,
    ...overrides,
  };
}

function dimRow(overrides: Partial<ProfileDimensionRow> = {}): ProfileDimensionRow {
  return {
    module: 'AI_USAGE',
    dimension_code: 'D_A',
    dimension_name: '维度A',
    group_code: null,
    score: 75,
    status: 'normal',
    rationale: '',
    evidences: [],
    trend: [],
    company_avg: null,
    ...overrides,
  };
}

function detail(overrides: Partial<ProfileDetail> = {}): ProfileDetail {
  return {
    staff_name: '张三',
    periods: [],
    selected_period: null,
    activity_level: 'active',
    enneagram: null,
    modules: [moduleCard(), moduleCard({ module: 'AI_MGMT', score: 72 })],
    dimensions: [],
    ...overrides,
  };
}

function treeWith(dims: DimensionTreeNode['modules']): DimensionTreeNode {
  return { modules: dims };
}

// 基准树：使用模块 A/B 两维 + 管理模块 M 一维，均参与聚合。
function baseTree(): DimensionTreeNode {
  return treeWith([
    {
      module_code: 'AI_USAGE',
      name: 'AI 使用能力',
      data_source: 'CONVERSATION',
      is_reference: false,
      groups: null,
      dimensions: [
        brief('D_A', { module_code: 'AI_USAGE' }),
        brief('D_B', { module_code: 'AI_USAGE' }),
      ],
    },
    {
      module_code: 'AI_MGMT',
      name: 'AI 管理能力',
      data_source: 'ACTIVE_TEST',
      is_reference: false,
      groups: null,
      dimensions: [brief('D_M', { module_code: 'AI_MGMT', data_source: 'ACTIVE_TEST' })],
    },
  ]);
}

describe('buildConclusion 常量（specs §4.2.4 规则2）', () => {
  it('阈值 4/75/60 锁值', () => {
    expect(CONCLUSION_LIMITED_THRESHOLD).toBe(4);
    expect(STRENGTH_THRESHOLD).toBe(75);
    expect(WEAKNESS_THRESHOLD).toBe(60);
  });
});

describe('buildConclusion 各类产出（核心断言）', () => {
  const d = detail({
    modules: [
      moduleCard({ module: 'AI_USAGE', score: 82.4, change_vs_prev: 3 }),
      moduleCard({ module: 'AI_MGMT', score: null, change_vs_prev: null, data_status: 'pending' }),
    ],
    dimensions: [
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_A', dimension_name: '维度A', score: 85 }),
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_B', dimension_name: '维度B', score: 50 }),
      dimRow({
        module: 'AI_MGMT', dimension_code: 'D_M', dimension_name: '管理维', score: null, status: 'missing',
      }),
    ],
  });

  it('strength 含 A 不含 B（≥75）', () => {
    const { findings } = buildConclusion(d, baseTree());
    const strengths = findings.filter((f) => f.kind === 'strength');
    expect(strengths).toHaveLength(1);
    expect(JSON.stringify(strengths[0].text)).toContain('D_A');
    expect(JSON.stringify(findings.filter((f) => f.kind === 'strength'))).not.toContain('D_B');
  });

  it('weakness 含 B（<60）', () => {
    const { findings } = buildConclusion(d, baseTree());
    const weaknesses = findings.filter((f) => f.kind === 'weakness');
    expect(weaknesses).toHaveLength(1);
    expect(JSON.stringify(weaknesses[0].text)).toContain('D_B');
  });

  it('risk：管理 pending 拼待评估条目', () => {
    const { findings } = buildConclusion(d, baseTree());
    const risks = findings.filter((f) => f.kind === 'risk');
    expect(risks).toHaveLength(1);
    expect(risks[0].text).toContain('riskMgmtPending');
  });

  it('trend：使用模块 +3 产出条目', () => {
    const { findings } = buildConclusion(d, baseTree());
    const trends = findings.filter((f) => f.kind === 'trend');
    expect(trends).toHaveLength(1);
    expect(JSON.stringify(trends[0].text)).toContain('3');
  });

  it('主文键非谨慎参考提示（数据充足）', () => {
    const { headlineKey } = buildConclusion(d, baseTree());
    expect(headlineKey).not.toBe('profile:conclusion.limited');
  });
});

describe('buildConclusion 优势上限与并列（核心断言）', () => {
  it('三维度 ≥75 只取最高两项', () => {
    const tree = treeWith([
      {
        module_code: 'AI_USAGE', name: 'AI 使用能力', data_source: 'CONVERSATION', is_reference: false,
        groups: null,
        dimensions: [
          brief('D_H1', { module_code: 'AI_USAGE' }),
          brief('D_H2', { module_code: 'AI_USAGE' }),
          brief('D_H3', { module_code: 'AI_USAGE' }),
        ],
      },
    ]);
    const d = detail({
      dimensions: [
        dimRow({ module: 'AI_USAGE', dimension_code: 'D_H1', dimension_name: '高1', score: 95 }),
        dimRow({ module: 'AI_USAGE', dimension_code: 'D_H2', dimension_name: '高2', score: 88 }),
        dimRow({ module: 'AI_USAGE', dimension_code: 'D_H3', dimension_name: '高3', score: 76 }),
      ],
    });
    const { findings } = buildConclusion(d, tree);
    const strengths = findings.filter((f) => f.kind === 'strength');
    expect(strengths).toHaveLength(2);
    const text = JSON.stringify(strengths);
    expect(text).toContain('D_H1');
    expect(text).toContain('D_H2');
    expect(text).not.toContain('D_H3');
  });

  it('两模块优势合并排序取最高两项（跨模块）', () => {
    const { findings } = buildConclusion(
      detail({
        dimensions: [
          dimRow({ module: 'AI_USAGE', dimension_code: 'D_A', dimension_name: 'A', score: 86 }),
          dimRow({ module: 'AI_MGMT', dimension_code: 'D_M', dimension_name: 'M', score: 90 }),
        ],
      }),
      baseTree(),
    );
    const strengths = findings.filter((f) => f.kind === 'strength');
    expect(strengths).toHaveLength(2);
  });
});

describe('buildConclusion 严重不足替换主文（核心断言）', () => {
  it('使用能力 score null 替换为谨慎参考提示键', () => {
    const d = detail({
      modules: [
        moduleCard({ module: 'AI_USAGE', score: null, data_status: 'pending' }),
        moduleCard({ module: 'AI_MGMT', score: 70 }),
      ],
    });
    expect(buildConclusion(d, baseTree()).headlineKey).toBe('profile:conclusion.limited');
  });

  it('降权 2 + 缺失 2（和=4）同样替换', () => {
    const d = detail({
      modules: [
        moduleCard({
          module: 'AI_USAGE',
          score: 65,
          data_status: 'degraded',
          insufficient_count: 2,
          missing_count: 2,
        }),
        moduleCard({ module: 'AI_MGMT', score: 70 }),
      ],
    });
    expect(buildConclusion(d, baseTree()).headlineKey).toBe('profile:conclusion.limited');
  });

  it('降权 2 + 缺失 1（和=3 <4）不替换', () => {
    const d = detail({
      modules: [
        moduleCard({
          module: 'AI_USAGE', score: 65, data_status: 'degraded', insufficient_count: 2, missing_count: 1,
        }),
        moduleCard({ module: 'AI_MGMT', score: 70 }),
      ],
    });
    expect(buildConclusion(d, baseTree()).headlineKey).not.toBe('profile:conclusion.limited');
  });
});

describe('buildConclusion 空类不产出（核心断言）', () => {
  it('无短板 <60 断言无 weakness 条目', () => {
    const d = detail({
      dimensions: [
        dimRow({ module: 'AI_USAGE', dimension_code: 'D_A', dimension_name: '维度A', score: 80 }),
        dimRow({ module: 'AI_MGMT', dimension_code: 'D_M', dimension_name: '管理维', score: 72 }),
      ],
    });
    const { findings } = buildConclusion(d, baseTree());
    expect(findings.filter((f) => f.kind === 'weakness')).toHaveLength(0);
  });

  it('无变化无趋势条目、模块完整无风险条目', () => {
    const d = detail({
      dimensions: [
        dimRow({ module: 'AI_USAGE', dimension_code: 'D_A', dimension_name: '维度A', score: 65 }),
      ],
    });
    const { findings } = buildConclusion(d, baseTree());
    expect(findings.filter((f) => f.kind === 'trend')).toHaveLength(0);
    expect(findings.filter((f) => f.kind === 'risk')).toHaveLength(0);
  });
});

describe('buildConclusion 参与聚合判据（03 §1.9 三条件）', () => {
  function rows(): ProfileDimensionRow[] {
    return [
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_A', score: 90 }),
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_NOOV', score: 55, status: 'normal' }),
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_INS', score: 58, status: 'insufficient' }),
      dimRow({ module: 'AI_USAGE', dimension_code: 'D_NUL', score: null, status: 'missing' }),
    ];
  }

  it('include_overview=false 维度不进优势与短板判定', () => {
    const tree = treeWith([
      {
        module_code: 'AI_USAGE', name: 'AI 使用能力', data_source: 'CONVERSATION', is_reference: false,
        groups: null,
        dimensions: [
          brief('D_A', { module_code: 'AI_USAGE' }),
          brief('D_NOOV', { module_code: 'AI_USAGE', include_overview: false }),
          brief('D_INS', { module_code: 'AI_USAGE' }),
          brief('D_NUL', { module_code: 'AI_USAGE' }),
        ],
      },
    ]);
    const { findings } = buildConclusion(detail({ dimensions: rows() }), tree);
    const weaknessText = JSON.stringify(findings.filter((f) => f.kind === 'weakness'));
    expect(weaknessText).not.toContain('D_NOOV');
    expect(weaknessText).not.toContain('D_INS');
    expect(weaknessText).not.toContain('D_NUL');
  });

  it('ENNEAGRAM 模块维度不进优势判定', () => {
    const tree = treeWith([
      {
        module_code: 'AI_USAGE', name: 'AI 使用能力', data_source: 'CONVERSATION', is_reference: false,
        groups: null,
        dimensions: [brief('D_A', { module_code: 'AI_USAGE' })],
      },
      {
        module_code: 'ENNEAGRAM', name: '九型', data_source: 'CONVERSATION', is_reference: true,
        groups: null,
        dimensions: [brief('E_1', { module_code: 'ENNEAGRAM' })],
      },
    ]);
    const d = detail({
      dimensions: [
        dimRow({ module: 'AI_USAGE' as const, dimension_code: 'D_A', score: 90 }),
        dimRow({ module: 'AI_USAGE' as const, dimension_code: 'E_1', score: 95 }),
      ],
    });
    const { findings } = buildConclusion(d, tree);
    const strengths = findings.filter((f) => f.kind === 'strength');
    expect(strengths).toHaveLength(1);
    expect(JSON.stringify(strengths)).toContain('D_A');
    expect(JSON.stringify(strengths)).not.toContain('E_1');
  });

  it('树中不存在该维度（启用态变化）不进优势判定', () => {
    const tree = treeWith([
      {
        module_code: 'AI_USAGE', name: 'AI 使用能力', data_source: 'CONVERSATION', is_reference: false,
        groups: null,
        dimensions: [brief('D_A', { module_code: 'AI_USAGE' })],
      },
    ]);
    const d = detail({
      dimensions: [
        dimRow({ dimension_code: 'D_A', score: 90 }),
        dimRow({ dimension_code: 'D_GONE', score: 99 }),
      ],
    });
    const { findings } = buildConclusion(d, tree);
    expect(findings.filter((f) => f.kind === 'strength')).toHaveLength(1);
  });
});

describe('buildConclusion 风险与趋势补充边界', () => {
  it('风险计数摘要携带 N/M 与 module 键', () => {
    const d = detail({
      modules: [
        moduleCard({
          module: 'AI_USAGE', score: 70, data_status: 'degraded',
          insufficient_count: 2, failed_count: 1, missing_count: 3,
        }),
        moduleCard({ module: 'AI_MGMT', score: 70, data_status: 'missing', missing_count: 1 }),
      ],
    });
    const { findings } = buildConclusion(d, baseTree());
    const risks = findings.filter((f) => f.kind === 'risk');
    expect(risks).toHaveLength(2);
    const usage = risks.find((r) => JSON.stringify(r.text).includes('AI_USAGE'));
    expect(usage).toBeDefined();
    expect(JSON.stringify(usage!.text)).toContain('2');
    expect(JSON.stringify(usage!.text)).toContain('3');
    expect(JSON.stringify(usage!.text)).not.toContain('1');
  });

  it('降为负值产出 trend 条目', () => {
    const d = detail({
      modules: [
        moduleCard({ module: 'AI_USAGE', score: 70, change_vs_prev: -2 }),
      ],
    });
    const { findings } = buildConclusion(d, baseTree());
    expect(findings.filter((f) => f.kind === 'trend')).toHaveLength(1);
  });
});

describe('buildConclusion 主文（specs §4.2.4 规则2）', () => {
  it('主文键为正常键并携带两模块等级与最高分维度参数', () => {
    const d = detail({
      modules: [
        moduleCard({ module: 'AI_USAGE', score: 86.2 }),
        moduleCard({ module: 'AI_MGMT', score: 72 }),
      ],
      dimensions: [
        dimRow({ dimension_code: 'D_A', dimension_name: '维度A', score: 91 }),
        dimRow({ dimension_code: 'D_B', dimension_name: '维度B', score: 70 }),
        dimRow({ module: 'AI_MGMT', dimension_code: 'D_M', dimension_name: '管理维', score: 72 }),
      ],
    });
    const { headlineKey, headlineParams } = buildConclusion(d, baseTree());
    expect(headlineKey).toBe('profile:conclusion.headline');
    expect(headlineParams['usageGrade']).toBe('excellent');
    expect(headlineParams['mgmtGrade']).toBe('good');
    expect(headlineParams['topDimensionName']).toBe('维度A');
  });

  it('两模块均无聚合行也判严重不足', () => {
    const d = detail({
      modules: [
        moduleCard({ module: 'AI_USAGE', score: null, data_status: 'pending' }),
        moduleCard({ module: 'AI_MGMT', score: null, data_status: 'pending' }),
      ],
    });
    const { headlineKey } = buildConclusion(d, baseTree());
    expect(headlineKey).toBe('profile:conclusion.limited');
  });

  it('空维度（空画像）无 findings、主文走谨慎参考提示', () => {
    const d = detail({
      modules: [
        moduleCard({ module: 'AI_USAGE', score: null, data_status: 'pending', missing_count: 8 }),
        moduleCard({ module: 'AI_MGMT', score: null, data_status: 'pending', missing_count: 3 }),
      ],
      dimensions: [],
    });
    const { headlineKey, findings } = buildConclusion(d, baseTree());
    expect(headlineKey).toBe('profile:conclusion.limited');
    // 空维度无优势/短板；风险仍按模块计数产出（specs 规则2 风险类独立于维度行）。
    expect(findings.every((f) => f.kind === 'risk')).toBe(true);
  });
});
