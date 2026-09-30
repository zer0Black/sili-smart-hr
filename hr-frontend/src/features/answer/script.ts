// 对话剧本纯函数（specs §4.1.4 规则1 规则化施测：AI 逐题发问为状态机驱动的规则化包装，无 LLM）。
// 消息流只由 ctx/reply 数据 + i18n 预设剧本推导，组件层零文案决策。
import type { TFunction } from 'i18next';

import type {
  AnswerContextResult,
  AnswerQuestionItem,
  AnswerReplyResult,
  AnswerTestType,
} from './answer-types';

export type ChatRole = 'ai' | 'user';

export interface ChatMessage {
  id: string;
  role: ChatRole;
  lines: string[];
}

export type ScriptPhase = 'asking' | 'finished';

export interface ScriptState {
  messages: ChatMessage[];
  phase: ScriptPhase;
}

/** 消息 id：一次页面会话内自增即可，消息流只增不改（specs §5.2.4 规则3）。 */
let seq = 0;
function nextId(): string {
  seq += 1;
  return `msg-${seq}`;
}

function ai(lines: string[]): ChatMessage {
  return { id: nextId(), role: 'ai', lines };
}

function user(lines: string[]): ChatMessage {
  return { id: nextId(), role: 'user', lines };
}

/** 开场白两段（specs §4.1.4 规则1：按 test_type 取系统预设剧本）。 */
function openingLines(t: TFunction, testType: AnswerTestType): string[] {
  if (testType === 'enneagram') {
    return [t('script.enne.opening1'), t('script.enne.opening2')];
  }
  return [t('script.ai.opening1'), t('script.ai.opening2')];
}

/**
 * 题目包装消息：题号行 + 情境段 + 作答要求段（specs §4.1.5 多段呈现，原型同构）。
 * 九型 scenario 为题项陈述、requirement 为 1-5 级作答说明，语义随 test_type 切换。
 */
function questionLines(t: TFunction, testType: AnswerTestType, question: AnswerQuestionItem): string[] {
  const title =
    testType === 'enneagram'
      ? t('script.scaleTitle', { seq: question.seq, statement: question.scenario })
      : t('script.questionTitle', { seq: question.seq, dimension: question.dimension_name });
  return [title, question.scenario, question.requirement];
}

/** 题间确认剧本（specs §4.1.4 规则1 题间确认）。 */
function ackLine(t: TFunction, testType: AnswerTestType): string {
  return t(testType === 'enneagram' ? 'script.ackEnneagram' : 'script.ackAIMgmt');
}

/** 完成提示两段（specs §4.1.5：全部作答完成后 AI 剧本收尾）。 */
function finishLines(t: TFunction): string[] {
  return [t('script.finish1'), t('script.finish2')];
}

/**
 * 从 A1 上下文组装初始消息流（specs §4.1.3 对话恢复：进度与当前题从记录推算）。
 * 结构：开场白 → 逐条回放（user 回复 / 题间确认 / 已答题包装）→ 当前题或完成提示。
 */
export function buildInitialMessages(t: TFunction, ctx: AnswerContextResult): ScriptState {
  const messages: ChatMessage[] = [ai(openingLines(t, ctx.test_type))];
  const bySeq = new Map(ctx.questions.map((question) => [question.seq, question]));

  for (const reply of ctx.replies) {
    // 回放顺序：本题题面（重进仍可见上文）→ 员工回复 → 题间确认。
    const question = bySeq.get(reply.seq);
    if (question) {
      messages.push(ai(questionLines(t, ctx.test_type, question)));
    }
    messages.push(user([reply.content]));
    messages.push(ai([ackLine(t, ctx.test_type)]));
  }

  if (ctx.finished) {
    // 完成态重进：全部题已回放，仅补完成提示（输入区由组件转提交形态）。
    return { messages: [...messages, ai(finishLines(t))], phase: 'finished' };
  }
  // 断点续答：当前题为下一未作答题（服务端权威 answered_count + 1）。
  const current = bySeq.get(ctx.answered_count + 1) ?? ctx.questions[ctx.answered_count];
  if (current) {
    messages.push(ai(questionLines(t, ctx.test_type, current)));
  }
  return { messages, phase: 'asking' };
}

/**
 * 一次回复成功后追加（specs §4.1.3 发送回复推进）：
 * user 消息 → next 时题间确认 + 下一题包装 / finished 时完成提示两段。
 * 服务端 question_seq 与本地不一致时以 next_question 全文重建当前题（specs §5.2.4 规则1 前端对齐义务）。
 */
export function applyReplySuccess(
  t: TFunction,
  prev: ScriptState,
  ctx: AnswerContextResult,
  reply: AnswerReplyResult,
  content: string,
): ScriptState {
  const messages = [...prev.messages, user([content])];
  if (reply.action === 'finished') {
    return { messages: [...messages, ai(finishLines(t))], phase: 'finished' };
  }
  // 服务端 question_seq 对齐：响应携带的 next_question 即服务端推算的实际下一题（specs §5.2.4 规则1）。
  const next = reply.next_question;
  const lines = next
    ? questionLines(t, ctx.test_type, next)
    : // 防御分支：action=next 必带 next_question（03 §3 A2），缺题时仅呈现确认避免崩页。
      [ackLine(t, ctx.test_type)];
  return {
    messages: [...messages, ai([ackLine(t, ctx.test_type)]), ai(lines)],
    phase: 'asking',
  };
}

/**
 * 本地校验（specs §5.2.5 提交前拦截）：enneagram 须为 1-5 整数；ai_mgmt 去首尾空格非空。
 * 500 上限经组件字数计数器与 maxLength 预拦，此处不复判。
 */
export function isValidLocalInput(testType: AnswerTestType, content: string): boolean {
  if (testType === 'enneagram') {
    return /^[1-5]$/.test(content);
  }
  return content.trim().length > 0;
}

/** 进度百分比（specs §4.1.2 B 对话进度）：已答/总数四舍五入，总数 0 防除零。 */
export function progressPercent(answered: number, total: number): number {
  if (total <= 0) return 0;
  return Math.round((answered / total) * 100);
}
