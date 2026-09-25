// GenerateForm 题目生成页四态容器测试（specs §4.3.1-§4.3.5 / §4A.3）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';

await i18n.changeLanguage('zh');

const navigateMock = vi.hoisted(() => vi.fn());
const treeQ = vi.hoisted(() => ({
  data: undefined as { modules: unknown[] } | undefined,
  isLoading: false,
}));
const detailStore = vi.hoisted(() => ({ map: new Map<string, { data?: { description?: string } }>() }));
const progressQ = vi.hoisted(() => ({
  data: undefined as Record<string, unknown> | undefined,
  isLoading: false,
}));
const progressIdle = vi.hoisted(() => ({ data: undefined, isLoading: false }));
const createMutateMock = vi.hoisted(() => vi.fn());
const cancelMutateMock = vi.hoisted(() => vi.fn());

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}));
vi.mock('@/features/dimension/hooks', () => ({
  useDimensionTree: () => treeQ,
  useDimensionDetail: (id: string) => detailStore.map.get(id) ?? { data: undefined },
}));
vi.mock('@/features/question-bank/generate-hooks', () => ({
  useCreateGeneration: () => ({ mutate: createMutateMock, isPending: false }),
  // 实 hook 按 enabled: !!id 守卫：表单态（无 generationId）不出数据，mock 对齐
  useGenerationProgress: (id: string | null) => (id ? progressQ : progressIdle),
  useCancelGeneration: () => ({ mutate: cancelMutateMock, isPending: false }),
}));
vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { GenerateForm } from '../generate-form';

function makeTree(enabledDims: Array<{ id: string; name: string; enabled: boolean }>) {
  return {
    modules: [
      {
        module_code: 'AI_MGMT',
        name: 'AI 管理能力',
        data_source: 'CONVERSATION',
        is_reference: false,
        groups: null,
        dimensions: enabledDims.map((d) => ({
          id: d.id,
          code: `MGT-${d.id}`,
          name: d.name,
          module_code: 'AI_MGMT',
          group_code: null,
          data_source: 'CONVERSATION',
          weight: 20,
          include_overview: true,
          enabled: d.enabled,
        })),
      },
    ],
  };
}

const RUNNING_PROGRESS = {
  generation_id: '3001',
  status: 'RUNNING',
  generated_count: 6,
  count: 12,
  current_dimension_id: '100',
  current_dimension_name: '授权与分工',
};

function renderForm(node: ReactElement = <GenerateForm />) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

/** 选中「授权与分工」并发起生成（mock create 成功），进入进行态。 */
function startRunning() {
  fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));
  createMutateMock.mockImplementation(
    (_payload: unknown, cb?: { onSuccess?: (r: { generation_id: string; status: string }) => void }) => {
      cb?.onSuccess?.({ generation_id: '3001', status: 'QUEUED' });
    },
  );
  fireEvent.click(screen.getByRole('button', { name: '开始生成' }));
}

beforeEach(() => {
  navigateMock.mockReset();
  createMutateMock.mockReset();
  cancelMutateMock.mockReset();
  treeQ.data = makeTree([
    { id: '100', name: '授权与分工', enabled: true },
    { id: '110', name: '风险与担责', enabled: true },
    { id: '120', name: '已停用维度', enabled: false },
  ]) as { modules: unknown[] };
  treeQ.isLoading = false;
  detailStore.map.set('100', { data: { description: 'AI 辅助任务分解、人机边界、授权范围' } });
  detailStore.map.set('110', { data: { description: '风险定级、担责主体、合规边界' } });
  progressQ.data = undefined;
});

describe('GenerateForm 题目生成页四态（specs §4.3 / §4A.3）', () => {
  it('TestStartDisabledNoSelection：表单态未选维度时「开始生成」禁用，停用维度不出卡（BR1）', () => {
    renderForm();

    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: /已停用维度/ })).not.toBeInTheDocument();
    // 选中后解禁，再取消选中回到禁用
    fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));
    expect(screen.getByRole('button', { name: '开始生成' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));
    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
  });

  it('TestStartPayload：开始生成携所选维度 ID 与题数（默认 10，BR1）', () => {
    renderForm();
    fireEvent.change(screen.getByRole('spinbutton', { name: '题数' }), { target: { value: '12' } });
    fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));
    fireEvent.click(screen.getByRole('button', { name: /风险与担责/ }));

    fireEvent.click(screen.getByRole('button', { name: '开始生成' }));

    expect(createMutateMock).toHaveBeenCalledWith(
      { dimension_ids: ['100', '110'], count: 12 },
      expect.anything(),
    );
  });

  it('TestCountBounds：题数 4 与 31 均禁用开始生成，建议文案在场（BR1 / §4.3.5）', () => {
    renderForm();
    const countInput = screen.getByRole('spinbutton', { name: '题数' });
    fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));

    expect(screen.getByText(/建议每批 10 到 20 题，便于一次审完/)).toBeInTheDocument();

    fireEvent.change(countInput, { target: { value: '4' } });
    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
    fireEvent.change(countInput, { target: { value: '31' } });
    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
    fireEvent.change(countInput, { target: { value: '10' } });
    expect(screen.getByRole('button', { name: '开始生成' })).toBeEnabled();
  });

  it('TestResetRestoresDefault：重置清空选择并恢复题数 10（§4.3.3 重置）', () => {
    renderForm();
    fireEvent.click(screen.getByRole('button', { name: /授权与分工/ }));
    fireEvent.change(screen.getByRole('spinbutton', { name: '题数' }), { target: { value: '20' } });

    fireEvent.click(screen.getByRole('button', { name: '重置' }));

    const card = screen.getByRole('button', { name: /授权与分工/ });
    expect(card).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('spinbutton', { name: '题数' })).toHaveValue(10);
    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
  });

  it('TestRunningProgress：进行态渲染已生成 6 / 12、当前维度名与调用中文案（验收锚点 / §4.3.5）', async () => {
    progressQ.data = { ...RUNNING_PROGRESS };
    renderForm();

    startRunning();

    expect(await screen.findByText(/6 \/ 12/)).toBeInTheDocument();
    expect(screen.getByText('当前：授权与分工')).toBeInTheDocument();
    expect(screen.getByText('调用 LLM 出题中…')).toBeInTheDocument();
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '50');
    // 表单按钮区被进度区替换（§4.3.3 开始生成加载状态）
    expect(screen.queryByRole('button', { name: '开始生成' })).not.toBeInTheDocument();
  });

  it('TestRunningBackConfirm：进行态「返回题库」出二次确认，确认后取消并回题库（BR2 / §4.3.4 规则1）', async () => {
    progressQ.data = { ...RUNNING_PROGRESS };
    renderForm();
    startRunning();
    await screen.findByText(/6 \/ 12/);

    fireEvent.click(screen.getByRole('button', { name: '返回题库' }));
    expect(await screen.findByText('放弃本批确认')).toBeInTheDocument();

    cancelMutateMock.mockImplementation(
      (_id: string, cb?: { onSuccess?: () => void }) => cb?.onSuccess?.(),
    );
    fireEvent.click(screen.getByRole('button', { name: '放弃并返回' }));

    await waitFor(() => expect(cancelMutateMock).toHaveBeenCalledWith('3001', expect.anything()));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/question-bank' });
  });

  it('TestDoneGoReview：完成态展示批次号，「前往审核」navigate search 携批次 ID（BR4 / 验收锚点）', async () => {
    progressQ.data = {
      generation_id: '3001',
      status: 'COMPLETED',
      generated_count: 12,
      count: 12,
      current_dimension_id: '0',
      current_dimension_name: '',
      batch_id: '9001',
      batch_no: '#G0925',
    };
    renderForm();
    startRunning();

    expect(await screen.findByText(/#G0925/)).toBeInTheDocument();
    expect(screen.getByText(/本批 12 题/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '再生成一批' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '前往审核' }));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/question-bank', search: { review: '9001' } });
  });

  it('TestDoneRegenerateKeepsSelection：完成态「再生成一批」回表单态保留选择（BR4）', async () => {
    progressQ.data = {
      generation_id: '3001',
      status: 'COMPLETED',
      generated_count: 12,
      count: 12,
      current_dimension_id: '0',
      current_dimension_name: '',
      batch_id: '9001',
      batch_no: '#G0925',
    };
    renderForm();
    fireEvent.change(screen.getByRole('spinbutton', { name: '题数' }), { target: { value: '15' } });
    startRunning();
    await screen.findByText(/#G0925/);

    fireEvent.click(screen.getByRole('button', { name: '再生成一批' }));

    expect(await screen.findByRole('button', { name: '开始生成' })).toBeEnabled();
    expect(screen.getByRole('button', { name: /授权与分工/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('spinbutton', { name: '题数' })).toHaveValue(15);
  });

  it('TestFailedLLMTimeout：失败态渲染超时固定文案与作废说明（BR3 / §4.3.4 规则3）', async () => {
    progressQ.data = {
      generation_id: '3001',
      status: 'FAILED',
      generated_count: 6,
      count: 12,
      current_dimension_id: '0',
      current_dimension_name: '',
      error_code: 'LLM_TIMEOUT',
    };
    renderForm();
    startRunning();

    expect(await screen.findByText('生成失败')).toBeInTheDocument();
    expect(
      screen.getByText('失败原因：LLM 调用超时。本批已作废，未形成审核批次。'),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重新生成' })).toBeInTheDocument();
    // 失败态「返回题库」与左上角返回同名，两处均在
    expect(screen.getAllByRole('button', { name: '返回题库' }).length).toBeGreaterThanOrEqual(2);
  });

  it('TestFailedCANCELED：取消终态映射「已取消，本批未生成」（BR3 / 03 §3.14 枚举）', async () => {
    progressQ.data = {
      generation_id: '3001',
      status: 'CANCELED',
      generated_count: 3,
      count: 12,
      current_dimension_id: '0',
      current_dimension_name: '',
      error_code: 'CANCELED',
    };
    renderForm();
    startRunning();

    expect(await screen.findByText(/已取消，本批未生成/)).toBeInTheDocument();
  });

  it('TestFailedRetryKeepsSelection：失败态「重新生成」回表单态保留选择（BR3 / §4.3.3）', async () => {
    progressQ.data = {
      generation_id: '3001',
      status: 'FAILED',
      generated_count: 6,
      count: 12,
      current_dimension_id: '0',
      current_dimension_name: '',
      error_code: 'LLM_FAILED',
    };
    renderForm();
    startRunning();
    await screen.findByText('生成失败');

    fireEvent.click(screen.getByRole('button', { name: '重新生成' }));

    expect(await screen.findByRole('button', { name: '开始生成' })).toBeEnabled();
    expect(screen.getByRole('button', { name: /授权与分工/ })).toHaveAttribute('aria-pressed', 'true');
  });

  it('TestEmptyDimensions：无启用子能力时空态引导前往维度配置且开始生成置灰（BR5 / §4.3.5）', () => {
    treeQ.data = makeTree([{ id: '120', name: '已停用维度', enabled: false }]) as { modules: unknown[] };

    renderForm();

    expect(screen.getByText('暂无启用的 AI 管理子能力')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '前往维度配置' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '开始生成' })).toBeDisabled();
  });

  it('TestGoDimension：空态「前往维度配置」跳维度配置页（BR5）', () => {
    treeQ.data = makeTree([{ id: '120', name: '已停用维度', enabled: false }]) as { modules: unknown[] };
    renderForm();

    fireEvent.click(screen.getByRole('button', { name: '前往维度配置' }));

    expect(navigateMock).toHaveBeenCalledWith({ to: '/system/dimension' });
  });

  it('TestFormBackNoConfirm：表单态「返回题库」直接返回不出确认弹窗（§4.3.3）', () => {
    renderForm();

    fireEvent.click(screen.getByRole('button', { name: '返回题库' }));

    expect(navigateMock).toHaveBeenCalledWith({ to: '/question-bank' });
    expect(screen.queryByText('放弃本批确认')).not.toBeInTheDocument();
  });
});
