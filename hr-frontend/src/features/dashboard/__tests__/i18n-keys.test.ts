// dashboard i18n 键校验（T7 验收锚点：zh/en 键集合一致性 + 代码引用键全覆盖）。
// 覆盖：zh/en dashboard 命名空间顶层结构与扁平键集合一致、插值变量集合一致、
// dashboard 域源码（含 trend.tsx 的 TAB_LABEL_KEY/SOURCE_KEY 映射常量）引用键在 zh/en 中存在、
// 九型特征描述 trait1-9 双语齐备、enneagram-panel 复用的 profile ns 型名键存在。
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import en from '@/i18n/locales/en.json';
import zh from '@/i18n/locales/zh.json';

function flat(obj: Record<string, unknown>, prefix = ''): string[] {
  const out: string[] = [];
  for (const k of Object.keys(obj)) {
    const v = obj[k];
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) out.push(...flat(v as Record<string, unknown>, key));
    else out.push(key);
  }
  return out;
}

function interpVars(obj: Record<string, unknown>, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const k of Object.keys(obj)) {
    const v = obj[k];
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      for (const [kk, vv] of interpVars(v as Record<string, unknown>, key)) out.set(kk, vv);
    } else {
      out.set(key, (String(v).match(/\{\{(\w+)\}\}/g) ?? []).sort().join(','));
    }
  }
  return out;
}

describe('dashboard i18n zh/en 一致性（T7 验收锚点）', () => {
  it('dashboard 命名空间顶层结构对齐', () => {
    const zhNs = zh.dashboard as Record<string, unknown>;
    const enNs = en.dashboard as Record<string, unknown>;
    expect(Object.keys(enNs).sort()).toEqual(Object.keys(zhNs).sort());
    expect(Object.keys(zh)).toContain('dashboard');
  });

  it('dashboard 扁平键集合完全一致', () => {
    const zhKeys = new Set(flat(zh.dashboard as Record<string, unknown>));
    const enKeys = new Set(flat(en.dashboard as Record<string, unknown>));
    expect([...zhKeys].filter((k) => !enKeys.has(k))).toEqual([]);
    expect([...enKeys].filter((k) => !zhKeys.has(k))).toEqual([]);
  });

  it('dashboard 插值变量集合一致（{{var}} 名单逐键相同）', () => {
    const zhVars = interpVars(zh.dashboard as Record<string, unknown>);
    const enVars = interpVars(en.dashboard as Record<string, unknown>);
    const mismatches: string[] = [];
    for (const [key, vars] of zhVars) {
      if (enVars.get(key) !== vars) mismatches.push(`${key}: zh=[${vars}] en=[${enVars.get(key) ?? ''}]`);
    }
    for (const key of enVars.keys()) {
      if (!zhVars.has(key)) mismatches.push(`${key}: en 独有`);
    }
    expect(mismatches).toEqual([]);
  });
});

describe('dashboard 域代码引用键全覆盖', () => {
  // 源码中静态可收集的键：useTranslation('dashboard') 下的 t('yyy') 字面量、
  // trend.tsx 映射常量（TAB_LABEL_KEY/SOURCE_KEY 值）等 ns 内裸键字符串。
  // dashboard 域源文件清单：新增组件/纯函数文件须同步补录，漏录即漏校验。
  const used = new Set<string>();
  const files = [
    'src/features/dashboard/types.ts',
    'src/features/dashboard/api.ts',
    'src/features/dashboard/hooks.ts',
    'src/features/dashboard/components/activity-overview.tsx',
    'src/features/dashboard/components/enneagram-panel.tsx',
    'src/features/dashboard/components/module-radar-card.tsx',
    'src/features/dashboard/components/period-toolbar.tsx',
    'src/features/dashboard/components/suggestion-panel.tsx',
    'src/features/dashboard/components/trend-change-table.tsx',
    'src/features/dashboard/components/trend-dimension-card.tsx',
    'src/routes/_authenticated/dashboard/index.tsx',
    'src/routes/_authenticated/dashboard/trend.tsx',
  ];
  for (const f of files) {
    // vitest 进程 cwd 为 hr-frontend 根（package.json 所在），按仓库内相对路径读源码
    const src = readFileSync(resolve(process.cwd(), f), 'utf8');
    for (const m of src.matchAll(/\bt\('([a-zA-Z][a-zA-Z0-9_.]*)'/g)) used.add(m[1]);
    // 映射常量与三元分支里的裸键（如 TAB_LABEL_KEY 的 'module.AI_USAGE'）
    for (const m of src.matchAll(/'((?:page|toolbar|activity|module|enneagram|suggestion|trend)\.[a-zA-Z0-9_]+)'/g)) {
      used.add(m[1]);
    }
  }
  // 模板键动态后缀值域（源码常量为证，值域变更须同步此清单）
  for (const k of ['active', 'low_freq', 'unused']) used.add(`activity.${k}`); // cards/pieData key 常量
  for (const m of ['AI_USAGE', 'AI_MGMT']) used.add(`module.${m}`); // module 编码值域
  for (const n of ['1', '2', '3', '4', '5', '6', '7', '8', '9']) used.add(`enneagram.trait${n}`); // 恒 9 序列

  it('引用键均存在于 zh.dashboard', () => {
    const zhDashKeys = new Set(flat(zh.dashboard as Record<string, unknown>));
    const missing = [...used].filter((k) => !zhDashKeys.has(k));
    expect(missing).toEqual([]);
  });

  it('引用键均存在于 en.dashboard（zh/en 同步）', () => {
    const enDashKeys = new Set(flat(en.dashboard as Record<string, unknown>));
    const missing = [...used].filter((k) => !enDashKeys.has(k));
    expect(missing).toEqual([]);
  });
});

describe('九型特征描述与 profile ns 复用键（specs §4.1.2 D）', () => {
  const types = ['1', '2', '3', '4', '5', '6', '7', '8', '9'];

  it('型别特征描述 trait1-9 双语齐备且非空', () => {
    const z = (zh.dashboard as { enneagram: Record<string, string> }).enneagram;
    const e = (en.dashboard as { enneagram: Record<string, string> }).enneagram;
    for (const n of types) {
      expect(z[`trait${n}`].length).toBeGreaterThan(0);
      expect(e[`trait${n}`].length).toBeGreaterThan(0);
    }
  });

  it('enneagram-panel 复用的 profile 型名键存在于 zh/en.profile', () => {
    const zhProfileKeys = new Set(flat(zh.profile as Record<string, unknown>));
    const enProfileKeys = new Set(flat(en.profile as Record<string, unknown>));
    for (const n of types) {
      expect(zhProfileKeys.has(`enneagram.type${n}`)).toBe(true);
      expect(enProfileKeys.has(`enneagram.type${n}`)).toBe(true);
    }
  });
});
