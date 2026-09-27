// 题库域写操作错误处理共享辅助：特定业务码 toast + 废弃缓存，其余通用失败。
// 四个写组件（列表/编辑弹窗/审核视图/批次卡区）单点维护错误码分支。
import { toast } from 'sonner';

import { queryClient } from '@/lib/query-client';
import { ApiError } from '@/lib/http-client';
import { ErrCode } from '@/lib/contracts';

type Translate = (key: string) => string;

/** 题库域当前消费的写冲突码：被引用 1705 / 批次已关闭 1706 / 版本冲突 1713。 */
type WriteConflictCode =
  | typeof ErrCode.QuestionReferenced
  | typeof ErrCode.QuestionBatchClosed
  | typeof ErrCode.QuestionVersionConflict;

export interface WriteErrorAction {
  /** 命中的业务错误码。 */
  code: WriteConflictCode;
  /** 命中时的 toast 文案 key（questionBank ns）。 */
  toastKey: string;
  /** 命中后的额外动作（关弹窗/退视图等），在 toast 与缓存废弃之后执行。 */
  onHit?: () => void;
}

/** 写操作异常统一处理：命中特定码 invalidate question-bank 缓存，未命中通用 toast。 */
export function handleWriteError(err: unknown, t: Translate, action: WriteErrorAction): void {
  const code = err instanceof ApiError ? err.code : undefined;
  if (code === action.code) {
    toast.error(t(action.toastKey));
    void queryClient.invalidateQueries({ queryKey: ['question-bank'] });
    action.onHit?.();
    return;
  }
  toast.error(t('toastGeneric'));
}
