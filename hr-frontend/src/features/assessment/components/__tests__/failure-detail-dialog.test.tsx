// FailureDetailDialog 测试（specs §4.3 / BR5-BR7）
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

import i18n from '@/i18n/config';

// Radix Dialog 在 jsdom 缺指针捕获与 scrollIntoView，补桩
const origHasPointerCapture = Element.prototype.hasPointerCapture;
const origReleasePointerCapture = Element.prototype.releasePointerCapture;
const origScrollIntoView = Element.prototype.scrollIntoView;

const useBatchFailuresMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/assessment/hooks', () => ({
  useBatchFailures: (...args: unknown[]) => useBatchFailuresMock(...args),
}));

import { FailureDetailDialog } from '@/features/assessment/components/failure-detail-dialog';
import type { CreateBatchPreset } from '@/features/assessment/components/create-batch-dialog';
import type { BatchFailures } from '@/features/assessment/types';

const FAILURES_2: BatchFailures = {
  batch_id: '1780000000000000001',
  batch_no: 'B20260913-001',
  failed_count: 2,
  total_count: 5,
  period_start: '2026-08-31',
  period_end: '2026-09-06',
  list: [
    { token_name: '张三', error_summary: 'LLM 超时，超过 180s' },
    { token_name: '李四', error_summary: '评分结果解析失败' },
  ],
};

function okResult(data: BatchFailures = FAILURES_2) {
  return {
    data,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  };
}

function renderDialog(props?: {
  batchId?: string | null;
  onClose?: () => void;
  onReSubmitFailed?: (p: CreateBatchPreset) => void;
}) {
  const onClose = props?.onClose ?? vi.fn();
  const onReSubmitFailed = props?.onReSubmitFailed ?? vi.fn();
  render(
    <FailureDetailDialog
      batchId={props?.batchId === undefined ? '1780000000000000001' : props.batchId}
      onClose={onClose}
      onReSubmitFailed={onReSubmitFailed}
    />,
  );
  return { onClose, onReSubmitFailed };
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
  useBatchFailuresMock.mockReset();
  useBatchFailuresMock.mockReturnValue(okResult());
});

afterEach(() => {
  Element.prototype.hasPointerCapture = origHasPointerCapture;
  Element.prototype.releasePointerCapture = origReleasePointerCapture;
  Element.prototype.scrollIntoView = origScrollIntoView;
});

describe('FailureDetailDialog（specs §4.3）', () => {
  it('TestFailureDialogList：mock 2 条失败按返回序渲染姓名与摘要（BR6）', async () => {
    renderDialog();

    expect(await screen.findByText('失败 2 人 / 共 5 人')).toBeInTheDocument();
    const rows = screen.getAllByRole('row');
    // 表头 1 行 + 2 数据行，顺序按响应 list
    const cells = rows.map((r) => r.textContent ?? '');
    const z3 = cells.findIndex((c) => c.includes('张三'));
    const l4 = cells.findIndex((c) => c.includes('李四'));
    expect(z3).toBeGreaterThan(0);
    expect(l4).toBeGreaterThan(z3);
    expect(cells[z3]).toContain('LLM 超时，超过 180s');
    expect(cells[l4]).toContain('评分结果解析失败');
  });

  it('TestFailureDialogReSubmit：点按失败对象重新发起回传 staffs 与 period（BR5）', async () => {
    const { onReSubmitFailed } = renderDialog();

    fireEvent.click(await screen.findByRole('button', { name: '按失败对象重新发起' }));

    expect(onReSubmitFailed).toHaveBeenCalledTimes(1);
    expect(onReSubmitFailed).toHaveBeenCalledWith({
      staffs: [
        { staff_id: '', staff_name: '张三' },
        { staff_id: '', staff_name: '李四' },
      ],
      period: { start: '2026-08-31', end: '2026-09-06' },
    });
  });

  it('TestFailureDialogClose：点关闭走 onClose（§4.3.3）', async () => {
    const { onClose, onReSubmitFailed } = renderDialog();

    fireEvent.click(await screen.findByRole('button', { name: '关闭' }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onReSubmitFailed).not.toHaveBeenCalled();
  });

  it('TestFailureDialogLoadError：加载失败弹窗内失败态 + 重试重新拉取（BR7）', async () => {
    const refetch = vi.fn();
    useBatchFailuresMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch,
    });
    const { onClose } = renderDialog();

    expect(await screen.findByText('清单加载失败，请重试')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it('TestFailureDialogLoading：加载中显示骨架不显示表（§4.3.5）', () => {
    useBatchFailuresMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
      refetch: vi.fn(),
    });
    renderDialog();

    expect(screen.getByText('加载中…')).toBeInTheDocument();
    expect(screen.queryByRole('row')).not.toBeInTheDocument();
  });

  it('TestFailureDialogNullBatch：batchId=null 不渲染弹窗（关闭态）', () => {
    renderDialog({ batchId: null });
    expect(screen.queryByText(/失败明细/)).not.toBeInTheDocument();
  });

  it('TestFailureDialogTitle：标题携带批次号（§4.3.1）', async () => {
    renderDialog();
    expect(await screen.findByText('失败明细 · B20260913-001')).toBeInTheDocument();
  });

  it('TestFailureDialogTruncate：过长摘要 truncate + title 全文（§4.3.2）', async () => {
    const longReason =
      'LLM 返回结构非法，原因：响应 content 为空导致 JSON 解析失败，已按兜底策略跳过该人并写入失败终态，等待补跑';
    useBatchFailuresMock.mockReturnValue(
      okResult({
        ...FAILURES_2,
        failed_count: 1,
        list: [{ token_name: '张三', error_summary: longReason }],
      }),
    );
    renderDialog();

    const cell = await screen.findByText(longReason);
    expect(cell).toHaveClass('truncate');
    expect(cell).toHaveAttribute('title', longReason);
  });
});
