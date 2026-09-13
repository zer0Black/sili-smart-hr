// CreateBatchDialog 测试（specs §4.2 / BR1-BR4）
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import i18n from '@/i18n/config';

// Radix Dialog/AlertDialog 在 jsdom 缺指针捕获与 scrollIntoView，补桩
const origHasPointerCapture = Element.prototype.hasPointerCapture;
const origReleasePointerCapture = Element.prototype.releasePointerCapture;
const origScrollIntoView = Element.prototype.scrollIntoView;

const createMutateMock = vi.hoisted(() => vi.fn());
const useBatchPlanMock = vi.hoisted(() => vi.fn());
const useStaffsMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/assessment/hooks', () => ({
  useCreateBatch: () => ({
    mutate: (...args: unknown[]) => createMutateMock(...args),
    isPending: false,
  }),
  useBatchPlan: (...args: unknown[]) => useBatchPlanMock(...args),
}));

vi.mock('@/features/system-params/hooks', () => ({
  useStaffs: (...args: unknown[]) => useStaffsMock(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastInfo = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(
    (...args: unknown[]) => toastInfo(...args),
    {
      success: (...args: unknown[]) => toastSuccess(...args),
      error: (...args: unknown[]) => toastError(...args),
    },
  ),
}));

import { CreateBatchDialog } from '@/features/assessment/components/create-batch-dialog';
import type { CreateBatchPreset } from '@/features/assessment/components/create-batch-dialog';
import type { BatchPlan, StaffItem } from '@/lib/contracts';

const STAFF_A: StaffItem = { staff_id: 'u1', staff_name: '张三' };
const STAFF_B: StaffItem = { staff_id: 'u2', staff_name: '李四' };

function makePlan(overrides: Partial<BatchPlan> = {}): BatchPlan {
  return {
    next_trigger_at: '2026-09-14 23:00',
    period: 'weekly',
    target_mode: 'all',
    target_brief: [],
    target_names: [],
    target_count: 0,
    dimension_base_count: 4,
    dimension_upper_count: 3,
    ...overrides,
  };
}

function renderDialog(props?: {
  open?: boolean;
  preset?: CreateBatchPreset | null;
  onClose?: () => void;
  onSubmitted?: () => void;
}) {
  const onClose = props?.onClose ?? vi.fn();
  const onSubmitted = props?.onSubmitted ?? vi.fn();
  render(
    <CreateBatchDialog
      open={props?.open ?? true}
      preset={props?.preset ?? null}
      onClose={onClose}
      onSubmitted={onSubmitted}
    />,
  );
  return { onClose, onSubmitted };
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
  createMutateMock.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
  toastInfo.mockReset();
  useBatchPlanMock.mockReturnValue({ data: makePlan() });
  useStaffsMock.mockReturnValue({
    data: { list: [STAFF_A, STAFF_B], total: 2, page: 1, page_size: 20 },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  });
});

afterEach(() => {
  vi.useRealTimers();
  Element.prototype.hasPointerCapture = origHasPointerCapture;
  Element.prototype.releasePointerCapture = origReleasePointerCapture;
  Element.prototype.scrollIntoView = origScrollIntoView;
});

describe('CreateBatchDialog（specs §4.2）', () => {
  it('TestCreateDialogTypeCards：另两类型卡片置灰，点击 toast 后续版本开放（BR1）', () => {
    renderDialog();

    // 对话分析为选中态，另两类型置灰
    const aiMgmt = screen.getByText('AI 管理能力').closest('button');
    const enneagram = screen.getByText('九型人格').closest('button');
    const conversation = screen.getByText('对话分析').closest('button');
    expect(conversation).toHaveAttribute('aria-pressed', 'true');
    expect(aiMgmt).toBeDisabled();
    expect(enneagram).toBeDisabled();
    expect(aiMgmt).toHaveAttribute('title', '该评测类型将在后续版本开放');
    expect(enneagram).toHaveAttribute('title', '该评测类型将在后续版本开放');

    // 置灰卡片点击：toast 提示后续版本开放（disabled 按钮不响应 click，组件需另行承载点击提示）
    const aiMgmtClickable = screen.getByTestId('type-card-ai-mgmt');
    fireEvent.click(aiMgmtClickable);
    expect(toastInfo).toHaveBeenCalledWith('该评测类型将在后续版本开放');
  });

  it('TestCreateDialogDefaultPeriod：常规打开时段默认上一完整周窗口（BR2）', () => {
    // 固定 now 为 2026-09-13（周日）；weekly 上一完整周窗口为 2026-08-31 ~ 2026-09-06
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2026-09-13T10:00:00'));
    renderDialog();

    const start = screen.getByLabelText('开始日期') as HTMLInputElement;
    const end = screen.getByLabelText('结束日期') as HTMLInputElement;
    expect(start.value).toBe('2026-08-31');
    expect(end.value).toBe('2026-09-06');
  });

  it('TestCreateDialogValidation：空人员提交出字段级错误且未调 createBatch（BR3）', async () => {
    renderDialog();

    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    expect(await screen.findByText('请至少选择一名评估对象')).toBeInTheDocument();
    expect(createMutateMock).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('TestCreateDialogValidation：start 晚于 end 出字段级错误（BR2）', async () => {
    renderDialog();

    const start = screen.getByLabelText('开始日期');
    const end = screen.getByLabelText('结束日期');
    fireEvent.change(start, { target: { value: '2026-09-01' } });
    fireEvent.change(end, { target: { value: '2026-08-01' } });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    expect(await screen.findByText('开始日期不能晚于结束日期')).toBeInTheDocument();
    expect(createMutateMock).not.toHaveBeenCalled();
  });

  it('TestCreateDialogValidation：结束日期为今天出字段级错误（BR2）', async () => {
    // 不冻时钟：以真实时间作为基准，让 RHF/zod 的 dayjs() 取到与组件相同的「今天」
    const realNow = new Date();
    const y = new Date(realNow.getTime() - 24 * 3600 * 1000);
    const fmt = (d: Date) =>
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    const yesterdayStr = fmt(y);
    const todayStr = fmt(realNow);

    renderDialog();

    const start = screen.getByLabelText('开始日期');
    const end = screen.getByLabelText('结束日期');

    // 用原生 setter 触发 input 事件：fireEvent.change 对 type=date 在 jsdom 下不进 RHF 的注册回调
    const setNative = (el: HTMLInputElement, value: string) => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        'value',
      )!.set!;
      setter.call(el, value);
      el.dispatchEvent(new Event('input', { bubbles: true }));
    };
    setNative(start as HTMLInputElement, yesterdayStr);
    setNative(end as HTMLInputElement, todayStr);
    fireEvent.change(start, { target: { value: yesterdayStr } });
    fireEvent.change(end, { target: { value: todayStr } });

    // fake timer 下 submit 事件丢失，用 fireEvent.submit 直发表单提交
    const formEl = (end as HTMLInputElement).closest('form')!;
    fireEvent.submit(formEl);

    await waitFor(() =>
      expect(screen.getByText('结束日期不能是今天或以后')).toBeInTheDocument(),
    );
    expect(createMutateMock).not.toHaveBeenCalled();
    expect((end as HTMLInputElement).max).toBe(yesterdayStr);
  });

  it('TestCreateDialogPreset：preset 带入人员与时段为预填值（BR3/BR5）', () => {
    renderDialog({
      preset: {
        staffs: [STAFF_A, STAFF_B],
        period: { start: '2026-08-01', end: '2026-08-07' },
      },
    });

    expect(screen.getByLabelText('移除 张三')).toBeInTheDocument();
    expect(screen.getByLabelText('移除 李四')).toBeInTheDocument();
    const start = screen.getByLabelText('开始日期') as HTMLInputElement;
    const end = screen.getByLabelText('结束日期') as HTMLInputElement;
    expect(start.value).toBe('2026-08-01');
    expect(end.value).toBe('2026-08-07');
  });

  it('TestCreateDialogSubmit：校验通过调 createBatch，成功回调 onSubmitted 且关弹窗（BR3）', async () => {
    const { onClose, onSubmitted } = renderDialog({
      preset: {
        staffs: [STAFF_A],
        period: { start: '2026-08-01', end: '2026-08-07' },
      },
    });

    createMutateMock.mockImplementation((_payload: unknown, opts: { onSuccess: () => void }) => {
      opts.onSuccess();
    });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    await waitFor(() => expect(createMutateMock).toHaveBeenCalledTimes(1));
    expect(createMutateMock).toHaveBeenCalledWith(
      {
        target_mode: 'specified',
        staffs: [{ staff_id: 'u1', staff_name: '张三' }],
        period_start: '2026-08-01',
        period_end: '2026-08-07',
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
    expect(onSubmitted).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestCreateDialogSubmitFail：接口失败 toast 留弹窗（BR3）', async () => {
    const { onClose } = renderDialog({
      preset: {
        staffs: [STAFF_A],
        period: { start: '2026-08-01', end: '2026-08-07' },
      },
    });

    createMutateMock.mockImplementation((_p: unknown, opts: { onError: (e: unknown) => void }) => {
      opts.onError(new Error('boom'));
    });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith('发起失败，请稍后重试'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('TestCreateDialogCancelConfirm：staffs 非空取消弹二次确认（BR4）', async () => {
    const { onClose } = renderDialog({
      preset: {
        staffs: [STAFF_A],
        period: { start: '2026-08-01', end: '2026-08-07' },
      },
    });

    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(await screen.findByText('已选择评估对象，确定放弃并关闭？')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '放弃并关闭' }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it('TestCreateDialogCancelEmpty：staffs 为空取消直接关闭不确认（BR4）', () => {
    const { onClose } = renderDialog();
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestCreateDialogFocus：弹窗打开焦点落评估对象触发框（§4.2.5）', async () => {
    renderDialog();
    // Radix Dialog 打开即把焦点让渡给内部第一个可聚焦元素，本组件用 setTimeout 0 把焦点再移回评估对象触发框
    await vi.waitFor(() => {
      expect(screen.getByText('点击选择评估对象').closest('button')).toHaveFocus();
    });
  });
});
