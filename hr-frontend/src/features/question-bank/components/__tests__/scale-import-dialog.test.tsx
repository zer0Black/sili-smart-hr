// ScaleImportDialog 量表引入两步弹窗测试（specs §4.1.2 F / §4.1.3 / §4.1.4 规则8/9 / §4A.4）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import dayjs from 'dayjs';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';

await i18n.changeLanguage('zh');

Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

const scalesQ = vi.hoisted(() => ({
  data: undefined as { list: unknown[] } | undefined,
  isLoading: false,
  refetch: vi.fn(() => Promise.resolve({})),
}));
const importMutateMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/question-bank/scale-hooks', () => ({
  useScales: () => scalesQ,
  useImportScale: () => ({ mutate: importMutateMock, isPending: false }),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { ScaleImportDialog } from '../scale-import-dialog';
import { ApiError } from '@/lib/http-client';
import type { ImportScaleResult, ScaleCandidate } from '@/lib/contracts';

const candidates: ScaleCandidate[] = [
  {
    scale_key: 'RISO_HUDSON',
    name: 'Riso-Hudson 标准量表',
    question_count: 144,
    estimated_minutes: 25,
    description: '主流九型量表，覆盖 9 型别全维度题项',
    imported: true,
  },
  {
    scale_key: 'ESSENCE',
    name: 'Essence 精简量表',
    question_count: 108,
    estimated_minutes: 18,
    description: '标准量表的缩短版本，作答负担更轻',
    imported: false,
  },
];

// 样本期后端按题数等比折算时长，mock 用同构小值覆盖折算口径
const sampleCandidates: ScaleCandidate[] = candidates.map((c) => ({
  ...c,
  question_count: c.scale_key === 'RISO_HUDSON' ? 18 : 9,
  estimated_minutes: c.scale_key === 'RISO_HUDSON' ? 3 : 2,
}));

const importResult: ImportScaleResult = {
  batch_id: '1785000000000000002',
  batch_no: '#S0923',
  question_count: 108,
};

function renderDialog(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

/** 渲染并进入第二步（选中未引入的 ESSENCE 后点下一步）。 */
async function renderAtStep2() {
  const utils = renderDialog(
    <ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
  fireEvent.click(screen.getByRole('button', { name: '下一步' }));
  await screen.findByText('确认引入');
  return utils;
}

beforeEach(() => {
  scalesQ.data = { list: candidates };
  scalesQ.isLoading = false;
  scalesQ.refetch.mockClear();
  importMutateMock.mockReset();
});

describe('ScaleImportDialog 量表引入两步弹窗（specs §4.1.2 F / §4A.4）', () => {
  it('TestImportedCardDisabled：已引入量表卡 disabled 且带「已引入」标注，点击不选中（规则8 / BR1）', () => {
    renderDialog(<ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />);

    const importedCard = screen.getByRole('button', { name: /Riso-Hudson 标准量表/ });
    expect(importedCard).toBeDisabled();
    expect(screen.getByText('已引入')).toBeInTheDocument();
    // 置灰卡点击后未选中，「下一步」仍禁用
    fireEvent.click(importedCard);
    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled();
  });

  it('TestNextDisabledWithoutSelection：未选卡时「下一步」禁用（§4.1.2 F 必选一项 / BR1）', () => {
    renderDialog(<ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />);

    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled();
  });

  it('TestStep2Summary：第二步展示题数与时长（来自 mock 数据，样本期等比折算口径）及计分方式/审核批次说明/批次号预览', async () => {
    scalesQ.data = { list: sampleCandidates };
    await renderAtStep2();

    expect(screen.getByText('9 题')).toBeInTheDocument();
    expect(screen.getByText('约 2 分钟')).toBeInTheDocument();
    expect(screen.getByText('Likert 5 级，9 型倾向聚合')).toBeInTheDocument();
    expect(screen.getByText('独立成批，不与 AI 管理题混审')).toBeInTheDocument();
    // 批次号预览 #S+当日 MMdd（specs §4.1.2 F），嵌在整句中用部分匹配
    expect(screen.getByText(new RegExp(`#S${dayjs().format('MMDD')}`))).toBeInTheDocument();
  });

  it('TestPrevBackToStep1：第二步「上一步」回到第一步且选中态保留', async () => {
    await renderAtStep2();

    fireEvent.click(screen.getByRole('button', { name: '上一步' }));
    expect(screen.getByRole('button', { name: '下一步' })).toBeInTheDocument();
    // 选中态保留：直接再点下一步可回到第二步
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    expect(await screen.findByText('确认引入')).toBeInTheDocument();
  });

  it('TestConfirmSuccess：确认引入携 scale_key，成功后出现「进入审核」并回调 batchId（§4.1.3 / BR2）', async () => {
    importMutateMock.mockImplementation((_key: string, cb?: { onSuccess?: (r: ImportScaleResult) => void }) => {
      cb?.onSuccess?.(importResult);
    });
    const onEnterReview = vi.fn();
    const onOpenChange = vi.fn();
    renderDialog(
      <ScaleImportDialog open onOpenChange={onOpenChange} onEnterReview={onEnterReview} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认引入' }));

    await waitFor(() => expect(importMutateMock).toHaveBeenCalledWith('ESSENCE', expect.anything()));
    // 成功后按钮区切「稍后审核」「进入审核」，并展示实际批次号（嵌在整句中部分匹配）
    expect(await screen.findByRole('button', { name: '稍后审核' })).toBeInTheDocument();
    expect(screen.getByText(/#S0923/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '进入审核' }));
    expect(onEnterReview).toHaveBeenCalledTimes(1);
    expect(onEnterReview).toHaveBeenCalledWith('1785000000000000002');
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it('TestLaterReviewStaysList：成功后「稍后审核」关闭弹窗留在列表态（§4.1.3 / BR2）', async () => {
    importMutateMock.mockImplementation((_key: string, cb?: { onSuccess?: (r: ImportScaleResult) => void }) => {
      cb?.onSuccess?.(importResult);
    });
    const onOpenChange = vi.fn();
    const onEnterReview = vi.fn();
    renderDialog(
      <ScaleImportDialog open onOpenChange={onOpenChange} onEnterReview={onEnterReview} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认引入' }));
    fireEvent.click(await screen.findByRole('button', { name: '稍后审核' }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onEnterReview).not.toHaveBeenCalled();
  });

  it('TestErr1704Toast：收 1704 时 toast「该量表已引入」并 refetch 刷新置灰（规则9 / BR3）', async () => {
    const { toast } = await import('sonner');
    importMutateMock.mockImplementation((_key: string, cb?: { onError?: (e: unknown) => void }) => {
      cb?.onError?.(new ApiError(1704, 'imported'));
    });
    const onOpenChange = vi.fn();

    renderDialog(<ScaleImportDialog open onOpenChange={onOpenChange} onEnterReview={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认引入' }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('该量表已引入'));
    await waitFor(() => expect(scalesQ.refetch).toHaveBeenCalled());
    // 弹窗保留在第二步，不关闭
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it('TestGenericError：非 1704 失败走通用文案（规则9），弹窗保留可重试', async () => {
    const { toast } = await import('sonner');
    importMutateMock.mockImplementation((_key: string, cb?: { onError?: (e: unknown) => void }) => {
      cb?.onError?.(new Error('network'));
    });
    const onOpenChange = vi.fn();

    renderDialog(<ScaleImportDialog open onOpenChange={onOpenChange} onEnterReview={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认引入' }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('操作失败，请稍后重试'));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
    expect(screen.getByRole('button', { name: '确认引入' })).toBeInTheDocument();
  });

  it('TestCancelCloses：第一步「取消」关闭弹窗', () => {
    const onOpenChange = vi.fn();
    renderDialog(<ScaleImportDialog open onOpenChange={onOpenChange} onEnterReview={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('TestOpenResets：重新打开弹窗回到第一步并清空选中与成功态', async () => {
    importMutateMock.mockImplementation((_key: string, cb?: { onSuccess?: (r: ImportScaleResult) => void }) => {
      cb?.onSuccess?.(importResult);
    });
    const utils = renderDialog(
      <ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />,
    );

    // 走完整流程到成功态
    fireEvent.click(screen.getByRole('button', { name: /Essence 精简量表/ }));
    fireEvent.click(screen.getByRole('button', { name: '下一步' }));
    fireEvent.click(await screen.findByRole('button', { name: '确认引入' }));
    await screen.findByRole('button', { name: '进入审核' });

    // 关闭再打开：回到第一步、选中与成功态清空
    utils.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <ScaleImportDialog open={false} onOpenChange={vi.fn()} onEnterReview={vi.fn()} />
      </QueryClientProvider>,
    );
    utils.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole('button', { name: '下一步' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: '进入审核' })).not.toBeInTheDocument();
  });

  it('TestLoadingState：候选列表加载中显示加载占位', () => {
    scalesQ.data = undefined;
    scalesQ.isLoading = true;
    renderDialog(<ScaleImportDialog open onOpenChange={vi.fn()} onEnterReview={vi.fn()} />);

    expect(screen.getByText('加载中…')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Riso-Hudson/ })).not.toBeInTheDocument();
  });
});
