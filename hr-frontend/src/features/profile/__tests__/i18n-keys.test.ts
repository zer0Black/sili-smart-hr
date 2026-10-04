// profile i18n 键校验（T7 验收锚点：zh/en 键集合一致性 + 代码引用键全覆盖）。
// 覆盖：zh/en 顶层命名空间与扁平键集合一致、插值变量集合一致、
// profile 域源码（含 types.ts 四组映射与 conclusion.ts finding 键）引用键在 zh 中存在、
// 九型 1-9 中文名与后端 profile_export.go 的 enneagramTypeNames 对齐（03 A2）。
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import en from '@/i18n/locales/en.json';
import zh from '@/i18n/locales/zh.json';

import { enneagramTypeKey } from '../types';

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

describe('i18n zh/en 一致性（T7 验收锚点）', () => {
  it('顶层命名空间集合一致', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    expect(Object.keys(zh)).toContain('profile');
  });

  it('扁平键集合完全一致', () => {
    const zhKeys = new Set(flat(zh));
    const enKeys = new Set(flat(en));
    expect([...zhKeys].filter((k) => !enKeys.has(k))).toEqual([]);
    expect([...enKeys].filter((k) => !zhKeys.has(k))).toEqual([]);
  });

  it('插值变量集合一致（{{var}} 名单逐键相同）', () => {
    const zhVars = interpVars(zh);
    const mismatches: string[] = [];
    for (const [key, vars] of zhVars) {
      const enVars = interpVars(en).get(key);
      if (enVars !== vars) mismatches.push(`${key}: zh=[${vars}] en=[${enVars ?? ''}]`);
    }
    expect(mismatches).toEqual([]);
  });
});

describe('profile 域代码引用键全覆盖', () => {
  // 源码中静态可收集的键：'profile:xxx' 字面量（types.ts 映射 + conclusion.ts finding 键）
  // 与组件 useTranslation('profile') 下的 t('yyy') 字面量（含 warnKey/titleKey 属性透传）。
  // profile 域源文件清单：新增组件/纯函数文件须同步补录，漏录即漏校验。
  const used = new Set<string>();
  const files = [
    'src/features/profile/types.ts',
    'src/features/profile/conclusion.ts',
    'src/features/profile/status.ts',
    'src/features/profile/components/profile-table.tsx',
    'src/features/profile/components/summary-cards.tsx',
    'src/features/profile/components/dimension-panel.tsx',
    'src/features/profile/components/conclusion-panel.tsx',
    'src/features/profile/components/enneagram-panel.tsx',
    'src/features/profile/components/period-select.tsx',
    'src/routes/_authenticated/profile/index.tsx',
    'src/routes/_authenticated/profile/$staffName.tsx',
  ];
  for (const f of files) {
    // vitest 进程 cwd 为 hr-frontend 根（package.json 所在），按仓库内相对路径读源码
    const src = readFileSync(resolve(process.cwd(), f), 'utf8');
    for (const m of src.matchAll(/'profile:([a-zA-Z0-9_.]+)'/g)) used.add(m[1]);
    for (const m of src.matchAll(/\bt\('([a-zA-Z][a-zA-Z0-9_.]*)'/g)) used.add(m[1]);
    // t(map[x] ?? 'fallback') 形态：?? 右侧字面量也是运行时可能渲染的键
    for (const m of src.matchAll(/\?\? '([a-zA-Z][a-zA-Z0-9_.]*)'/g)) used.add(m[1]);
    for (const m of src.matchAll(/titleKey: '([a-zA-Z0-9_.]+)'/g)) used.add(m[1]);
    for (const m of src.matchAll(/warnKey="([a-zA-Z0-9_.]+)"/g)) used.add(m[1]);
  }

  it('引用键均存在于 zh.profile', () => {
    const zhProfileKeys = new Set(flat(zh.profile as Record<string, unknown>));
    const missing = [...used].filter((k) => !zhProfileKeys.has(k));
    expect(missing).toEqual([]);
  });

  it('引用键均存在于 en.profile（zh/en 同步）', () => {
    const enProfileKeys = new Set(flat(en.profile as Record<string, unknown>));
    const missing = [...used].filter((k) => !enProfileKeys.has(k));
    expect(missing).toEqual([]);
  });
});

describe('九型中文名与后端导出常量对齐（03 A2）', () => {
  // 与 hr-backend/internal/service/profile_export.go 的 enneagramTypeNames 一致：
  // 完美型/助人型/成就型/自我型/智慧型/忠诚型/活跃型/领袖型/和平型。
  const backendNames: Record<string, string> = {
    '1': '完美型',
    '2': '助人型',
    '3': '成就型',
    '4': '自我型',
    '5': '智慧型',
    '6': '忠诚型',
    '7': '活跃型',
    '8': '领袖型',
    '9': '和平型',
  };

  it('zh 九型键值逐一与后端常量一致', () => {
    const z = zh.profile as { enneagram: Record<string, string> };
    for (const [type, name] of Object.entries(backendNames)) {
      expect(enneagramTypeKey[type]).toBe(`profile:enneagram.type${type}`);
      expect(z.enneagram[`type${type}`]).toBe(name);
    }
  });

  it('列表「待评估」缺失口径键存在（specs §4.1.2 B/§4.2.2）', () => {
    const z = zh.profile as { list: Record<string, string>; summary: { pending: string } };
    expect(z.list.pendingScore).toBe('待评估');
    expect(z.summary.pending).toBe('待评估');
  });
});
