// 维度表单提交错误统一映射：字段级校验错误落内联报错，其余 toast（specs §4.1.4 规则9）。
// 文案由调用方传 t() 结果，helper 不绑定命名空间。
import { toast } from 'sonner';

import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export type DimensionField = 'name' | 'anchor' | 'prompt';

export interface DimensionSubmitErrorMessages {
  nameInvalid: string;
  anchorRequired: string;
  promptRequired: string;
  toastBadRequest: string;
  toastGeneric: string;
}

/** 额外业务 code → toast 文案（如 leaf 的版本冲突），命中即 toast 并终止。 */
export type ExtraToastMap = Record<number, string>;

/** 把 mutation onError 映射为字段内联报错或 toast，全部路径都消化。 */
export function handleDimensionSubmitError(
  err: unknown,
  onFieldError: (field: DimensionField, message: string) => void,
  messages: DimensionSubmitErrorMessages,
  extraToasts: ExtraToastMap = {},
): void {
  const code = err instanceof ApiError ? err.code : undefined;
  if (code === ErrCode.DimensionNameInvalid) {
    onFieldError('name', messages.nameInvalid);
    return;
  }
  if (code === ErrCode.DimensionAnchorRequired) {
    onFieldError('anchor', messages.anchorRequired);
    return;
  }
  if (code === ErrCode.DimensionPromptRequired) {
    onFieldError('prompt', messages.promptRequired);
    return;
  }
  if (code !== undefined && extraToasts[code] !== undefined) {
    toast.error(extraToasts[code]);
    return;
  }
  if (code === ErrCode.BadRequest) {
    toast.error(messages.toastBadRequest);
    return;
  }
  toast.error(messages.toastGeneric);
}
