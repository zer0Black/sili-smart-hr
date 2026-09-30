// 对话区（specs §4.1.2 B / §4.1.3 发送回复与提交并结束 / §4.1.5 交互逻辑，原型同构）。
// 消息流状态由 script.ts 纯函数推导，本组件只管渲染、输入、mutation 编排与异常分支。
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Bot, SendHorizonal, User } from 'lucide-react';
import type { JSX, KeyboardEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { ErrCode } from '@/lib/contracts';
import { AnswerApiError } from '../answer-api';
import { useAnswerReply, useAnswerSubmit } from '../answer-hooks';
import type { AnswerContextResult } from '../answer-types';
import { applyReplySuccess, buildInitialMessages, isValidLocalInput, runeCount, type ChatMessage } from '../script';

/** ai_mgmt 回复上限（specs §4.1.2 B）：计数器 + maxLength 双重预拦，服务端兜底 1902。
 *  上限按 rune 计数（与服务端 utf8.RuneCountInString 同口径），maxLength 由浏览器
 *  按 code unit 生效，emoji 等增补平面字符以计数器与切片为准。 */
const AIMGMT_MAX_RUNES = 500;

export function AnswerChat({
  token,
  ctx,
  onAnsweredChange,
  onInvalid,
  onSubmitSuccess,
}: {
  token: string;
  ctx: AnswerContextResult;
  /** reply 成功后以服务端 answered_count 上提，驱动页面进度即时更新（specs §4.1.2 B）。 */
  onAnsweredChange?: (answered: number) => void;
  /** 发送/提交中令牌失效转页面失效态（specs §4.1.4 规则5 交互时校验兜底）。 */
  onInvalid: () => void;
  onSubmitSuccess: (taskNo: string) => void;
}): JSX.Element {
  const { t } = useTranslation('answer');
  const replyMutation = useAnswerReply(token);
  const submitMutation = useAnswerSubmit(token);

  const [script, setScript] = useState(() => buildInitialMessages(t, ctx));
  const [input, setInput] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);

  const sending = replyMutation.isPending;
  const phase = script.phase;

  // 消息流变化自动滚底（specs §4.1.5），含 typing 气泡出现时刻。
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [script.messages.length, sending]);

  const send = () => {
    const content = input;
    if (sending || phase === 'finished') return;
    if (!isValidLocalInput(ctx.test_type, content)) {
      // 本地拦截（specs §5.2.5 提交前）：按 test_type 提示重答，停留本题。
      appendInvalidLine();
      return;
    }
    replyMutation.mutate(
      { content },
      {
        onSuccess: (reply) => {
          setInput('');
          // 进度以落库记录为准（specs §4.1.2 B/§4.1.5：每题确认后即时更新计数与进度条）。
          onAnsweredChange?.(reply.answered_count);
          setScript((prev) => applyReplySuccess(t, prev, ctx, reply, content));
        },
        onError: (err) => {
          if (err instanceof AnswerApiError && err.code === ErrCode.AnswerReplyInvalid) {
            // 服务端格式校验兜底（specs §4.1.5 / §5.2.4 规则2）：按 test_type 剧本提示重答，输入保留。
            appendInvalidLine();
            return;
          }
          // 令牌失效（作答中被取消/链接重发作废）：转页面失效态（specs §4.1.4 规则5 / §5.2.5）。
          if (err instanceof AnswerApiError && err.code === ErrCode.AnswerTokenInvalid) {
            onInvalid();
            return;
          }
          toast.error(t('error.loadFailed'));
        },
      },
    );
  };

  // 非法回复提示行：文案按 test_type 取剧本（九型数字指引 / ai_mgmt 通用指引）。
  const appendInvalidLine = () => {
    setScript((prev) => ({
      ...prev,
      messages: [
        ...prev.messages,
        {
          id: `invalid-${prev.messages.length}`,
          role: 'ai',
          lines: [t(ctx.test_type === 'enneagram' ? 'script.invalidReply' : 'script.invalidReplyAIMgmt')],
        },
      ],
    }));
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // 回车发送、Shift+回车换行（specs §4.1.3）。
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    }
  };

  const submit = () => {
    submitMutation.mutate(undefined, {
      onSuccess: (result) => onSubmitSuccess(result.task_no),
      onError: (err) => {
        if (err instanceof AnswerApiError && err.code === ErrCode.AnswerIncomplete) {
          // 完整性失败：toast 后回作答态断点续答（specs §4.1.3 / A3 错误码）。
          toast.error(t('error.incomplete'));
          return;
        }
        if (err instanceof AnswerApiError && err.code === ErrCode.AnswerTokenInvalid) {
          // 提交时令牌失效：转页面失效态（specs §5.3.5）。
          onInvalid();
          return;
        }
        // 1500/网络错误：toast 后留在作答态可重试（specs §4.1.3）。
        toast.error(t('error.submitFailed'));
      },
    });
  };

  const canSend =
    !sending && phase === 'asking' && (ctx.test_type === 'enneagram' ? input.length > 0 : input.trim().length > 0);
  const placeholder =
    ctx.test_type === 'enneagram' ? t('input.placeholderEnne') : t('input.placeholderAIMgmt');
  // rune 口径截断：与后端 utf8.RuneCountInString 同口径，防 surrogate pair 被切半。
  const clampAIMgmt = (value: string) => [...value].slice(0, AIMGMT_MAX_RUNES).join('');

  return (
    <section className="bg-muted/40 overflow-hidden rounded-xl border">
      <div
        ref={scrollRef}
        className="flex h-[420px] flex-col gap-3.5 overflow-y-auto px-4 py-4 sm:px-5"
        aria-live="polite"
      >
        {script.messages.map((m) => (
          <MessageBubble key={m.id} message={m} />
        ))}
        {sending && <TypingBubble />}
      </div>
      {phase === 'asking' ? (
        <div className="flex items-end gap-2.5 border-t bg-card p-3">
          <div className="relative flex-1">
            <Textarea
              value={input}
              onChange={(e) => setInput(ctx.test_type === 'ai_mgmt' ? clampAIMgmt(e.target.value) : e.target.value)}
              onKeyDown={onKeyDown}
              disabled={sending}
              // 发送中输入区禁用至响应（specs §4.1.3 加载状态）。
              placeholder={placeholder}
              rows={ctx.test_type === 'enneagram' ? 1 : 2}
              className="field-sizing-fixed resize-none"
              aria-label={placeholder}
            />
            {ctx.test_type === 'ai_mgmt' && (
              <span
                className={`text-muted-foreground pointer-events-none absolute right-2.5 bottom-2 text-[11px] tabular-nums ${runeCount(input) >= AIMGMT_MAX_RUNES ? 'text-destructive' : ''}`}
              >
                {t('input.charCount', { count: runeCount(input) })}
              </span>
            )}
          </div>
          <Button
            type="button"
            size="icon"
            disabled={!canSend}
            onClick={send}
            aria-label={t('input.send')}
            className="size-10 shrink-0 self-end"
          >
            <SendHorizonal aria-hidden />
          </Button>
        </div>
      ) : (
        // 完成待提交态：输入区原位替换为提示与提交按钮（specs §4.1.3/§4.1.5）。
        <div className="flex items-center gap-2.5 border-t bg-card p-3">
          <p className="text-muted-foreground flex-1 px-1 py-2 text-[13px]">{t('input.finishTip')}</p>
          <Button type="button" disabled={submitMutation.isPending} onClick={submit} className="shrink-0">
            {t('input.submit')}
          </Button>
        </div>
      )}
    </section>
  );
}

/** 单条消息气泡：AI 左侧卡片底、员工右侧主色底，多段 lines 逐段 p 呈现（原型同构）。 */
function MessageBubble({ message }: { message: ChatMessage }): JSX.Element {
  const isAI = message.role === 'ai';
  return (
    <div className={`flex max-w-[88%] gap-2.5 ${isAI ? '' : 'ml-auto flex-row-reverse'}`}>
      <div
        aria-hidden
        className={`flex size-8 shrink-0 items-center justify-center rounded-full ${
          isAI ? 'bg-primary text-primary-foreground' : 'bg-success text-success-foreground'
        }`}
      >
        {isAI ? <Bot className="size-[18px]" /> : <User className="size-[18px]" />}
      </div>
      <div
        className={`rounded-md border px-3.5 py-2.5 text-sm leading-[1.7] ${
          isAI
            ? 'bg-card rounded-tl-sm'
            : 'rounded-tr-sm border-transparent bg-primary text-primary-foreground'
        }`}
      >
        {message.lines.map((line, i) => (
          <p key={i} className="whitespace-pre-line">
            {line}
          </p>
        ))}
      </div>
    </div>
  );
}

/** typing 三点气泡（specs §4.1.5）：发送中至接口响应的过渡呈现。 */
function TypingBubble(): JSX.Element {
  return (
    <div className="flex max-w-[88%] gap-2.5">
      <div aria-hidden className="bg-primary text-primary-foreground flex size-8 shrink-0 items-center justify-center rounded-full">
        <Bot className="size-[18px]" />
      </div>
      <div className="bg-card flex items-center gap-1 rounded-md rounded-tl-sm border px-4 py-3.5" role="status">
        <span className="bg-muted-foreground size-1.5 animate-bounce rounded-full [animation-delay:0ms]" />
        <span className="bg-muted-foreground size-1.5 animate-bounce rounded-full [animation-delay:150ms]" />
        <span className="bg-muted-foreground size-1.5 animate-bounce rounded-full [animation-delay:300ms]" />
      </div>
    </div>
  );
}
