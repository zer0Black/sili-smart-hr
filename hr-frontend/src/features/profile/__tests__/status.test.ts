import { describe, it, expect } from 'vitest';

import {
  roundScore,
  scoreGrade,
  activityLevelKey,
  dataStatusKey,
  confidenceKey,
  enneagramTypeKey,
} from '../status';

describe('roundScore 分数取整（specs §4.2.4 规则3）', () => {
  it('82.35 取整 82', () => {
    expect(roundScore(82.35)).toBe(82);
  });
  it('78.6 取整 79', () => {
    expect(roundScore(78.6)).toBe(79);
  });
  it('维度分为整数时原样返回', () => {
    expect(roundScore(75)).toBe(75);
  });
  it('null 分数不适用（调用方判 null，函数只收 number）', () => {
    // 契约约束：无聚合行 score 为 null，由消费方先判；此处锁 .5 恰好进位的边界
    expect(roundScore(84.5)).toBe(85);
  });
});

describe('scoreGrade 等级映射（specs §4.2.4 规则3，先取整再判级）', () => {
  it('85 为 excellent（阈值含）', () => {
    expect(scoreGrade(85)).toBe('excellent');
  });
  it('84.5 取整 85 后为 excellent（验证先取整再判级）', () => {
    expect(scoreGrade(84.5)).toBe('excellent');
  });
  it('84 为 good、70 为 good（70-84 良好，下界含）', () => {
    expect(scoreGrade(84)).toBe('good');
    expect(scoreGrade(70)).toBe('good');
  });
  it('60 为 medium、69 为 medium（60-69 中等）', () => {
    expect(scoreGrade(60)).toBe('medium');
    expect(scoreGrade(69)).toBe('medium');
  });
  it('59 为 poor（<60 待提升）', () => {
    expect(scoreGrade(59)).toBe('poor');
  });
});

describe('枚举键映射 re-export 自 types.ts（profile:* 命名空间）', () => {
  it('activityLevelKey 三态齐备', () => {
    expect(activityLevelKey).toEqual({
      active: 'profile:activityLevel.active',
      low_freq: 'profile:activityLevel.low_freq',
      unused: 'profile:activityLevel.unused',
    });
  });
  it('dataStatusKey 四态齐备（specs §4.2.2 B）', () => {
    expect(dataStatusKey).toEqual({
      complete: 'profile:dataStatus.complete',
      degraded: 'profile:dataStatus.degraded',
      missing: 'profile:dataStatus.missing',
      pending: 'profile:dataStatus.pending',
    });
  });
  it('confidenceKey 三档齐备（specs §4.2.4 规则5）', () => {
    expect(confidenceKey).toEqual({
      high: 'profile:confidence.high',
      medium: 'profile:confidence.medium',
      low: 'profile:confidence.low',
    });
  });
  it('enneagramTypeKey 九型齐备', () => {
    expect(Object.keys(enneagramTypeKey)).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9']);
    expect(enneagramTypeKey['1']).toBe('profile:enneagram.type1');
    expect(enneagramTypeKey['9']).toBe('profile:enneagram.type9');
  });
});
