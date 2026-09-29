import { useEffect, useRef } from 'react';

/**
 * useResetSignal：外部重置信号监听（发起成功重置列表入口，batch/question/test-task
 * 三表格同构口径）。跳过首挂载；值变化时执行 onReset（清筛选回第一页由调用方
 * setState 承载，filter 变化触发 refetch）。
 */
export function useResetSignal(resetKey: number | undefined, onReset: () => void): void {
  const lastRef = useRef<number | undefined>(resetKey);
  useEffect(() => {
    if (resetKey === undefined) {
      lastRef.current = resetKey;
      return;
    }
    if (lastRef.current === resetKey) return;
    lastRef.current = resetKey;
    onReset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);
}

/**
 * usePageClamp：页码钳位（batch-table 先例）：total 收缩（删除/筛选）让当前页
 * 落空时回落到末页，防轮询刷新后渲染「暂无数据」空态误导用户以为记录丢失。
 */
export function usePageClamp(page: number, totalPages: number, clamp: (page: number) => void): void {
  useEffect(() => {
    if (page > totalPages) clamp(totalPages);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [totalPages]);
}
