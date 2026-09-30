// 对话区（specs §4.1.2 B / §4.1.3 发送回复与提交并结束 / §4.1.5 交互逻辑，原型同构）。
// 消息流状态由 script.ts 纯函数推导，本组件只管渲染、输入、mutation 编排与异常分支。
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Bot, SendHorizonal, User } from 'lucide-react';
import type { JSX, KeyboardEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { AnswerApiError } from '../answer-api';
import { useAnswerReply, useAnswerSubmit } from '../answer-hooks';
import type { AnswerContextResult } from '../answer-types';
import { applyReplySuccess, buildInitialMessages, isValidLocalInput, type ChatMessage } from '../script';

/** ai_mgmt 回复上限（specs §4.1.2 B）：计数器 + maxLength 双重预拦，服务端兜底 1902。 */
const AIMGMT_MAX_CHARS = 500;

export function AnswerChat({
  token,
  ctx,
  onSubmitSuccess,
}: {
  token: string;
  ctx: AnswerContextResult;
  onSubmitSuccess: (taskNo: string) => void;
}): JSX.Element {
  const { t } = useTranslation('answer');
  const replyMutation = useAnswerReply(token);
  const submitMutation = useAnswerSubmit(token);

  const [script, setScript] = useState(() => buildInitialMessages(t, ctx));
  const [input, setInput] = useState('');
  // 当前题号：初值从记录推算（ctx.answered_count + 1），成功回复后以服务端回传题号对齐推进
  //（specs §5.2.4 规则1：客户端题号仅展示，响应回传实际题号）。
  const [currentSeq, setCurrentSeq] = useState(Math.min(ctx.answered_count + 1, ctx.question_total));
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
      // 本地拦截（specs §5.2.5 提交前）：九型非 1-5 整数按剧本提示重答，停留本题。
      if (ctx.test_type === 'enneagram') {
        setScript((prev) => ({
          ...prev,
          messages: [
            ...prev.messages,
            { id: `local-${prev.messages.length}`, role: 'ai', lines: [t('script.invalidReply')] },
          ],
        }));
      }
      return;
    }
    replyMutation.mutate(
      { content, questionSeq: currentSeq },
      {
        onSuccess: (reply) => {
          setInput('');
          // 服务端题号权威：以响应回传 question_seq 对齐当前题，next_question 全文即对齐后的题面
          //（specs §5.2.4 规则1 前端对齐义务）。
          setCurrentSeq(reply.question_seq);
          setScript((prev) => applyReplySuccess(t, prev, ctx, reply, content));
        },
        onError: (err) => {
          if (err instanceof AnswerApiError && err.code === 1902) {
            // 服务端格式校验兜底（specs §4.1.5 / §5.2.4 规则2）：AI 剧本提示重答，输入保留。
            setScript((prev) => ({
              ...prev,
              messages: [
                ...prev.messages,
                { id: `invalid-${prev.messages.length}`, role: 'ai', lines: [t('script.invalidReply')] },
              ],
            }));
            return;
          }
          // 1500/网络错误：保留输入可重试（specs §5.2.5 落库失败）。
          if (err instanceof AnswerApiError && err.code === 1901) {
            toast.error(t('invalid.title'));
            return;
          }
          toast.error(t('error.loadFailed'));
        },
      },
    );
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
        if (err instanceof AnswerApiError && err.code === 1903) {
          // 完整性失败：toast 后回作答态断点续答（specs §4.1.3 / A3 错误码）。
          toast.error(t('error.incomplete'));
          return;
        }
        if (err instanceof AnswerApiError && err.code === 1901) {
          toast.error(t('invalid.title'));
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
              onChange={(e) => setInput(ctx.test_type === 'ai_mgmt' ? e.target.value.slice(0, AIMGMT_MAX_CHARS) : e.target.value)}
              onKeyDown={onKeyDown}
              disabled={sending}
              // 发送中输入区禁用至响应（specs §4.1.3 加载状态）；ai_mgmt maxLength 双保险。
              maxLength={ctx.test_type === 'ai_mgmt' ? AIMGMT_MAX_CHARS : undefined}
              placeholder={placeholder}
              rows={ctx.test_type === 'enneagram' ? 1 : 2}
              className="field-sizing-fixed resize-none"
              aria-label={placeholder}
            />
            {ctx.test_type === 'ai_mgmt' && (
              <span
                className={`text-muted-foreground pointer-events-none absolute right-2.5 bottom-2 text-[11px] tabular-nums ${input.length >= AIMGMT_MAX_CHARS ? 'text-destructive' : ''}`}
              >
                {t('input.charCount', { count: input.length })}
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
        className={`flex size-8 shrink-0 items-center justify-center rounded-full text-white ${
          isAI ? 'bg-primary' : 'bg-success'
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
      <div aria-hidden className="bg-primary flex size-8 shrink-0 items-center justify-center rounded-full text-white">
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
