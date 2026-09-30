// 对话剧本纯函数测试（specs §4.1.4 规则1 规则化施测 / §4.1.3 对话恢复 / §5.2.5 前端本地校验）。
// t 用直通 fake：返回 key + JSON.stringify(options)，断言只对 key 与插值不敏感的结构语义。
import { describe, expect, it } from 'vitest';

import type { TFunction } from 'i18next';

import type { AnswerContextResult, AnswerReplyResult } from '../answer-types';
import {
  applyReplySuccess,
  buildInitialMessages,
  isValidLocalInput,
  progressPercent,
} from '../script';

const t = ((key: string, opts?: Record<string, unknown>) =>
  key + JSON.stringify(opts ?? {})) as unknown as TFunction;

function q(seq: number, dimension = `维度${seq}`, scenario?: string, requirement?: string) {
  return {
    seq,
    dimension_name: dimension,
    scenario: scenario ?? `情境${seq}`,
    requirement: requirement ?? `要求${seq}`,
  };
}

function ctx(partial: Partial<AnswerContextResult>): AnswerContextResult {
  return {
    task_no: 'T202609290001',
    test_type: 'ai_mgmt',
    question_total: 5,
    answered_count: 0,
    finished: false,
    questions: [q(1), q(2), q(3), q(4), q(5)],
    replies: [],
    ...partial,
  };
}

describe('buildInitialMessages', () => {
  it('TestBuildInitialFirstEntry：首次进入开场白 + 第 1 题包装，phase=asking', () => {
    const s = buildInitialMessages(t, ctx({}));
    expect(s.phase).toBe('asking');
    // 首条 AI 开场白两段
    expect(s.messages[0]).toMatchObject({ role: 'ai' });
    expect(s.messages[0].lines[0]).toContain('script.ai.opening1');
    expect(s.messages[0].lines[1]).toContain('script.ai.opening2');
    // 末条第 1 题包装：题号行 + 情境 + 作答要求
    const last = s.messages[s.messages.length - 1];
    expect(last.role).toBe('ai');
    expect(last.lines[0]).toContain('script.questionTitle');
    expect(last.lines[0]).toContain('"seq":1');
    expect(last.lines).toContain('情境1');
    expect(last.lines).toContain('要求1');
  });

  it('TestBuildInitialResume：answered=2 回放 2 条 user 消息与 2 条题目包装，当前题为 seq=3', () => {
    const s = buildInitialMessages(
      t,
      ctx({
        answered_count: 2,
        replies: [
          { seq: 1, content: 'C，因为……' },
          { seq: 2, content: '第二个回答' },
        ],
      }),
    );
    const users = s.messages.filter((m) => m.role === 'user');
    expect(users).toHaveLength(2);
    expect(users[0].lines).toEqual(['C，因为……']);
    expect(users[1].lines).toEqual(['第二个回答']);
    // 题目回放两条 + 当前题 seq=3（AI 消息总数 6 = 开场白 1 + 题 3 + ack 2）
    const titles = s.messages.filter((m) => m.role === 'ai').map((m) => m.lines[0]);
    expect(titles).toHaveLength(6);
    const titleLines = titles.filter((l) => l.startsWith('script.questionTitle'));
    expect(titleLines).toHaveLength(3);
    expect(titles[titles.length - 1]).toContain('"seq":3');
    expect(s.phase).toBe('asking');
    // 回放中每答完一题后间插题间确认（answered=2 → 2 条确认）
    const ackLines = s.messages.map((m) => m.lines[0]).filter((l) => l.startsWith('script.ackAIMgmt'));
    expect(ackLines).toHaveLength(2);
  });

  it('TestBuildInitialFinished：finished=true 直接完成待提交，末条为 finish 剧本两段', () => {
    const s = buildInitialMessages(
      t,
      ctx({
        answered_count: 5,
        finished: true,
        replies: [
          { seq: 1, content: 'a' },
          { seq: 2, content: 'b' },
          { seq: 3, content: 'c' },
          { seq: 4, content: 'd' },
          { seq: 5, content: 'e' },
        ],
      }),
    );
    expect(s.phase).toBe('finished');
    const last = s.messages[s.messages.length - 1];
    expect(last.role).toBe('ai');
    expect(last.lines).toEqual(['script.finish1{}', 'script.finish2{}']);
    // 完成态无当前题包装（第 5 题已回放，不再重复出题）
    const titles = s.messages.filter((m) => m.lines[0].startsWith('script.questionTitle')).length;
    expect(titles).toBe(5);
  });

  it('TestBuildInitialEnneagram：九型开场白与两段题面（题号行内插陈述 + 作答说明）', () => {
    const s = buildInitialMessages(
      t,
      ctx({
        test_type: 'enneagram',
        questions: [q(1, '', '我做事追求严谨和完美。', '请在 1（完全不符合）到 5（完全符合）之间回复')],
      }),
    );
    expect(s.messages[0].lines[0]).toContain('script.enne.opening1');
    const last = s.messages[s.messages.length - 1];
    // 两段结构：scaleTitle 内插陈述，第二段为 1-5 级作答说明；陈述不另起独立行。
    expect(last.lines).toEqual(['script.scaleTitle{"seq":1,"statement":"我做事追求严谨和完美。"}', '请在 1（完全不符合）到 5（完全符合）之间回复']);
  });
});

describe('applyReplySuccess', () => {
  it('TestApplyReplyNext：追加 user 消息 + ack + 第 4 题包装，phase 仍 asking', () => {
    const prev = buildInitialMessages(t, ctx({}));
    const len = prev.messages.length;
    const s = applyReplySuccess(t, prev, ctx({}), {
      question_seq: 3,
      answered_count: 3,
      question_total: 5,
      action: 'next',
      next_question: q(4),
    } satisfies AnswerReplyResult, 'C，因为……');
    expect(s.phase).toBe('asking');
    expect(s.messages).toHaveLength(len + 3);
    expect(s.messages[len]).toMatchObject({ role: 'user', lines: ['C，因为……'] });
    expect(s.messages[len + 1].lines[0]).toBe('script.ackAIMgmt{}');
    expect(s.messages[len + 2].lines[0]).toContain('"seq":4');
    expect(s.messages[len + 2].lines).toContain('情境4');
    expect(s.messages[len + 2].lines).toContain('要求4');
  });

  it('TestApplyReplyFinished：action=finished 追加完成剧本两段并置 finished', () => {
    const prev = buildInitialMessages(
      t,
      ctx({ answered_count: 4, replies: [{ seq: 1, content: 'a' }, { seq: 2, content: 'b' }, { seq: 3, content: 'c' }, { seq: 4, content: 'd' }] }),
    );
    const len = prev.messages.length;
    const s = applyReplySuccess(t, prev, ctx({}), {
      question_seq: 5,
      answered_count: 5,
      question_total: 5,
      action: 'finished',
      next_question: null,
    } satisfies AnswerReplyResult, '最后一题回答');
    expect(s.phase).toBe('finished');
    // user 消息 + finish 两段（无 ack 无下一题）
    expect(s.messages).toHaveLength(len + 2);
    expect(s.messages[len].role).toBe('user');
    expect(s.messages[len + 1].lines).toEqual(['script.finish1{}', 'script.finish2{}']);
  });

  it('TestApplyReplyEnneagramAck：九型 ack 用 ackEnneagram 剧本', () => {
    const c = ctx({ test_type: 'enneagram', questions: [q(1, '', '陈述1', '要求'), q(2, '', '陈述2', '要求')] });
    const prev = buildInitialMessages(t, c);
    const s = applyReplySuccess(t, prev, c, {
      question_seq: 1,
      answered_count: 1,
      question_total: 2,
      action: 'next',
      next_question: q(2, '', '陈述2', '要求'),
    } satisfies AnswerReplyResult, '4');
    expect(s.messages.some((m) => m.lines[0] === 'script.ackEnneagram{}')).toBe(true);
  });
});

describe('isValidLocalInput', () => {
  it('TestValidLocalInputEnneagram：仅 1-5 单字符整数通过，空格拒绝', () => {
    expect(isValidLocalInput('enneagram', '3')).toBe(true);
    expect(isValidLocalInput('enneagram', '1')).toBe(true);
    expect(isValidLocalInput('enneagram', '5')).toBe(true);
    expect(isValidLocalInput('enneagram', '0')).toBe(false);
    expect(isValidLocalInput('enneagram', '6')).toBe(false);
    expect(isValidLocalInput('enneagram', '3.5')).toBe(false);
    expect(isValidLocalInput('enneagram', 'a')).toBe(false);
    expect(isValidLocalInput('enneagram', ' 3 ')).toBe(false);
    expect(isValidLocalInput('enneagram', '')).toBe(false);
  });

  it('TestValidLocalInputAIMgmt：trim 非空即通过', () => {
    expect(isValidLocalInput('ai_mgmt', '  ')).toBe(false);
    expect(isValidLocalInput('ai_mgmt', '')).toBe(false);
    expect(isValidLocalInput('ai_mgmt', '选 C 因为……')).toBe(true);
    expect(isValidLocalInput('ai_mgmt', ' x ')).toBe(true);
  });
});

describe('progressPercent', () => {
  it('TestProgressPercent：0/满分/常规三档取整', () => {
    expect(progressPercent(0, 5)).toBe(0);
    expect(progressPercent(5, 5)).toBe(100);
    expect(progressPercent(2, 5)).toBe(40);
    expect(progressPercent(1, 3)).toBe(33);
  });
});
