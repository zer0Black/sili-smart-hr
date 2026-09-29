// CreateBatchDialog 测试（specs §4.2 / BR1-BR4 / P2_TST_001 §4.2.2-§4.2.5 三态激活）
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react';

import i18n from '@/i18n/config';

// Radix Dialog/AlertDialog 在 jsdom 缺指针捕获与 scrollIntoView，补桩
const origHasPointerCapture = Element.prototype.hasPointerCapture;
const origReleasePointerCapture = Element.prototype.releasePointerCapture;
const origScrollIntoView = Element.prototype.scrollIntoView;

const createMutateMock = vi.hoisted(() => vi.fn());
const useBatchPlanMock = vi.hoisted(() => vi.fn());
const useStaffsMock = vi.hoisted(() => vi.fn());
const useEnabledAiMgmtDimensionsMock = vi.hoisted(() => vi.fn());
const useScaleStatusMock = vi.hoisted(() => vi.fn());
const testTaskMutateMock = vi.hoisted(() => vi.fn());

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

vi.mock('@/features/question-bank/dimension-options', () => ({
  useEnabledAiMgmtDimensions: (...args: unknown[]) => useEnabledAiMgmtDimensionsMock(...args),
}));

vi.mock('@/features/assessment/test-task-hooks', () => ({
  useCreateTestTask: () => ({
    mutate: (...args: unknown[]) => testTaskMutateMock(...args),
    isPending: false,
  }),
  useScaleStatus: () => useScaleStatusMock(),
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

import {
  CreateBatchDialog,
  resetDialogTypeMemory,
} from '@/features/assessment/components/create-batch-dialog';
import type { CreateBatchPreset } from '@/features/assessment/components/create-batch-dialog';
import type { BatchPlan, StaffItem } from '@/lib/contracts';

const STAFF_A: StaffItem = { staff_id: 'u1', staff_name: '张三' };
const STAFF_B: StaffItem = { staff_id: 'u2', staff_name: '李四' };

const DIMS = [
  { id: 'd1', code: 'MGT-1', name: 'AI 战略规划', module_code: 'AI_MGMT', group_code: null, data_source: 'CONVERSATION', weight: 20, include_overview: true, enabled: true },
  { id: 'd2', code: 'MGT-2', name: 'AI 流程设计', module_code: 'AI_MGMT', group_code: null, data_source: 'CONVERSATION', weight: 20, include_overview: true, enabled: true },
];

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
  onSubmittedType?: (type: 'conversation' | 'ai_mgmt' | 'enneagram') => void;
}) {
  const onClose = props?.onClose ?? vi.fn();
  const onSubmitted = props?.onSubmitted ?? vi.fn();
  const onSubmittedType = props?.onSubmittedType ?? vi.fn();
  render(
    <CreateBatchDialog
      open={props?.open ?? true}
      preset={props?.preset ?? null}
      onClose={onClose}
      onSubmitted={onSubmitted}
      onSubmittedType={onSubmittedType}
    />,
  );
  return { onClose, onSubmitted, onSubmittedType };
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  // 会话记忆模块级变量跨用例泄漏：每用例重置回 conversation（刷新即重置语义）
  resetDialogTypeMemory();
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
  createMutateMock.mockReset();
  testTaskMutateMock.mockReset();
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
  useEnabledAiMgmtDimensionsMock.mockReturnValue(DIMS);
  useScaleStatusMock.mockReturnValue({
    data: { ready: true, scale_key: 'RISO_HUDSON', scale_name: 'Riso-Hudson 九型人格量表', active_question_count: 18 },
    isLoading: false,
    isError: false,
  });
});

afterEach(() => {
  vi.useRealTimers();
  Element.prototype.hasPointerCapture = origHasPointerCapture;
  Element.prototype.releasePointerCapture = origReleasePointerCapture;
  Element.prototype.scrollIntoView = origScrollIntoView;
});

describe('CreateBatchDialog（specs §4.2 对话分析既有链路）', () => {
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

  it('TestCreateDialogSubmit：校验通过调 createBatch，成功回调 onSubmitted 且关弹窗（BR3/BR5）', async () => {
    const { onClose, onSubmitted, onSubmittedType } = renderDialog({
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
    expect(onSubmittedType).toHaveBeenCalledWith('conversation');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestCreateDialogSubmitFail：接口失败 toast 留弹窗（BR5）', async () => {
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

  it('TestCreateDialogCancelConfirm：staffs 非空取消弹二次确认（BR4 口径）', async () => {
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

  it('TestCreateDialogCancelEmpty：staffs 为空取消直接关闭不确认（BR4 口径）', () => {
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

describe('CreateBatchDialog 三态激活（P2_TST_001 specs §4.2.2/§4.2.5）', () => {
  it('TestCreateDialogTypeSwitch：点 AI 管理能力卡片出现子能力复选框组默认全选（BR1/BR3）', async () => {
    renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    // 表单区出现子能力复选框组，默认全选（BR3）
    const cb1 = await screen.findByLabelText('AI 战略规划');
    const cb2 = screen.getByLabelText('AI 流程设计');
    expect(cb1).toBeChecked();
    expect(cb2).toBeChecked();
    // 评估时段字段随类型切换消失（表单区整体切换）
    expect(screen.queryByLabelText('开始日期')).not.toBeInTheDocument();
  });

  it('TestCreateDialogEnneagramScale：切九型卡片出现使用量表只读行，显量表名与题数（BR4）', async () => {
    renderDialog();

    fireEvent.click(screen.getByText('九型人格'));
    expect(await screen.findByText('Riso-Hudson 九型人格量表')).toBeInTheDocument();
    expect(screen.getByText('共 18 题')).toBeInTheDocument();
  });

  it('TestCreateDialogEnneagramScaleNotReady：量表未就绪显提示不阻断（BR4）', async () => {
    useScaleStatusMock.mockReturnValue({
      data: { ready: false, scale_key: '', scale_name: '', active_question_count: 0 },
      isLoading: false,
      isError: false,
    });
    renderDialog();

    fireEvent.click(screen.getByText('九型人格'));
    expect(
      await screen.findByText('九型量表未就绪，请先在题库引入量表'),
    ).toBeInTheDocument();
  });

  it('TestCreateDialogSwitchDiscards：三卡片切换后已填对象被清空（BR7）', async () => {
    const { onClose } = renderDialog({
      preset: {
        staffs: [STAFF_A],
        period: { start: '2026-08-01', end: '2026-08-07' },
      },
    });
    expect(screen.getByLabelText('移除 张三')).toBeInTheDocument();

    fireEvent.click(screen.getByText('AI 管理能力'));
    // conversation 表单内容随切换丢弃：回到 conversation 不再预填张三
    fireEvent.click(screen.getByText('对话分析'));
    expect(await screen.findByLabelText('开始日期')).toBeInTheDocument();
    expect(screen.queryByLabelText('移除 张三')).not.toBeInTheDocument();
    // 对象非空口径按当前分支判定：conversation 回到空名单，取消直接关不确认
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestCreateDialogAiMgmtSubmit：ai_mgmt 提交走 createTestTask，成功回调 onSubmittedType(ai_mgmt)（BR5）', async () => {
    const { onClose, onSubmittedType } = renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    fireEvent.click(screen.getByText('点击选择测评对象'));
    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));

    testTaskMutateMock.mockImplementation((_p: unknown, opts: { onSuccess: () => void }) => {
      opts.onSuccess();
    });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    await waitFor(() => expect(testTaskMutateMock).toHaveBeenCalledTimes(1));
    expect(testTaskMutateMock).toHaveBeenCalledWith(
      {
        test_type: 'ai_mgmt',
        staff_id: 'u1',
        staff_name: '张三',
        dimension_ids: ['d1', 'd2'],
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
    expect(onSubmittedType).toHaveBeenCalledWith('ai_mgmt');
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(createMutateMock).not.toHaveBeenCalled();
  });

  it('TestCreateDialogAiMgmtEmptyDims：ai_mgmt 取消全选维度提交被 zod 拦截字段下标红（BR3）', async () => {
    renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    // 默认全选取消两项
    fireEvent.click(await screen.findByLabelText('AI 战略规划'));
    fireEvent.click(screen.getByLabelText('AI 流程设计'));

    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    expect(await screen.findByText('请至少选择一项子能力')).toBeInTheDocument();
    expect(testTaskMutateMock).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('TestCreateDialogAiMgmtEmptyStaff：ai_mgmt 空对象提交字段下标红不调接口（BR2）', async () => {
    renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    expect(await screen.findByText('请选择测评对象')).toBeInTheDocument();
    expect(testTaskMutateMock).not.toHaveBeenCalled();
  });

  it('TestCreateDialogEnneagramSubmit：enneagram 提交走 createTestTask 不带 dimension_ids（BR5）', async () => {
    const { onClose, onSubmittedType } = renderDialog();

    fireEvent.click(screen.getByText('九型人格'));
    fireEvent.click(screen.getByText('点击选择测评对象'));
    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));

    testTaskMutateMock.mockImplementation((_p: unknown, opts: { onSuccess: () => void }) => {
      opts.onSuccess();
    });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));

    await waitFor(() => expect(testTaskMutateMock).toHaveBeenCalledTimes(1));
    expect(testTaskMutateMock).toHaveBeenCalledWith(
      {
        test_type: 'enneagram',
        staff_id: 'u1',
        staff_name: '张三',
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
    expect(onSubmittedType).toHaveBeenCalledWith('enneagram');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('TestCreateDialogErrCodes：ai_mgmt 提交错误码分桶 toast（1803/1804/1805/1305，specs §4.2.4/§5.1.5）', async () => {
    const { onClose } = renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    fireEvent.click(screen.getByText('点击选择测评对象'));
    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));

    const { ApiError } = await import('@/lib/http-client');
    // 1804/1805/1305 为固定原文，前端 i18n 分桶；1803 走英文模板剥离 + i18n
    // 插值（维度名取自 message，03 B1 错误码表契约）。
    const cases: Array<{ err: Error; message: string }> = [
      { err: new ApiError(1803, 'dimension AI 战略规划 has no active questions'), message: '子能力 AI 战略规划 无可用题目，请先在题库补充' },
      { err: new ApiError(1803, 'unrecognized message shape'), message: '子能力无可用题目，请先在题库补充' },
      { err: new ApiError(1804, 'backend'), message: '九型量表未就绪，请先在题库引入量表' },
      { err: new ApiError(1805, 'backend'), message: '人员信息获取失败，请稍后重试' },
      { err: new ApiError(1305, 'backend'), message: '人员或会话上游暂不可用，请稍后重试' },
    ];
    for (const c of cases) {
      testTaskMutateMock.mockImplementation((_p: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError(c.err);
      });
      fireEvent.click(screen.getByRole('button', { name: '提交' }));
      await waitFor(() => expect(toastError).toHaveBeenLastCalledWith(c.message));
    }
    // 失败留弹窗（BR5）
    expect(onClose).not.toHaveBeenCalled();
  });

  it('TestCreateDialogErrGeneric：未识别错误码走通用文案 toast（BR5）', async () => {
    renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    fireEvent.click(screen.getByText('点击选择测评对象'));
    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));

    const { ApiError } = await import('@/lib/http-client');
    testTaskMutateMock.mockImplementation((_p: unknown, opts: { onError: (e: unknown) => void }) => {
      opts.onError(new ApiError(1500, 'backend'));
    });
    fireEvent.click(screen.getByRole('button', { name: '提交' }));
    await waitFor(() => expect(toastError).toHaveBeenLastCalledWith('发起失败，请稍后重试'));
  });

  it('TestCreateDialogSessionMemory：类型选择会话级记忆，重开弹窗保持上次选择（BR1）', async () => {
    const first = renderDialog();
    fireEvent.click(screen.getByText('AI 管理能力'));
    await screen.findByLabelText('AI 战略规划');
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    // 对象为空直接关闭
    await waitFor(() => expect(first.onClose).toHaveBeenCalledTimes(1));
    cleanup();

    // 同标签页内重开：保持 ai_mgmt（模块级内存变量）
    renderDialog();
    expect(await screen.findByLabelText('AI 战略规划')).toBeInTheDocument();
  });

  it('TestCreateDialogTestCancelConfirm：主动测试分支对象非空取消弹二次确认（BR4 同口径）', async () => {
    const { onClose } = renderDialog();

    fireEvent.click(screen.getByText('AI 管理能力'));
    fireEvent.click(screen.getByText('点击选择测评对象'));
    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));

    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(await screen.findByText('已选择测评对象，确定放弃并关闭？')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '放弃并关闭' }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});
