// QuestionViewDialog 查看弹窗测试（specs §4.1.2 D / §4.1.3 查看题目）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

const fetchDetailMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/question-bank/api', () => ({
  fetchQuestions: vi.fn(),
  fetchQuestionDetail: fetchDetailMock,
  updateQuestion: vi.fn(),
  toggleQuestionStatus: vi.fn(),
  deleteQuestion: vi.fn(),
}));

import { QuestionViewDialog } from '../question-view-dialog';
import type { QuestionDetail } from '@/lib/contracts';

function makeDetail(overrides: Partial<QuestionDetail> = {}): QuestionDetail {
  return {
    id: '1780000000000000101',
    question_no: 'Q-AG-0002',
    source: 'AI',
    dimension_id: '202',
    dimension_name: '风险与担责',
    answer_mode: 'CHAT',
    status: 'REJECTED',
    scenario: 'AI 助手依据历史报价自动向客户承诺了一项超出授权范围的折扣……',
    requirement: '请选择最符合你处理方式的选项……A. …… B. ……',
    focus_point: '风险定级准确、担责边界清晰。',
    reject_reason: 'AI 建议存在明显性别与生育偏见',
    batch_id: '3001',
    batch_no: '#G0921',
    reference_count: 3,
    version: 2,
    created_at: '2026-09-21 10:31:24',
    updated_at: '2026-09-21 10:31:24',
    ...overrides,
  };
}

function renderDialog(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

beforeEach(() => {
  fetchDetailMock.mockReset();
  useAuthStore.setState({ token: 'test-token' });
});

describe('QuestionViewDialog 查看弹窗（specs §4.1.2 D / §4A.4）', () => {
  it('TestRejectedShowsReason：REJECTED 题显示驳回原因区块与全文字段', async () => {
    fetchDetailMock.mockResolvedValue(makeDetail());

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('Q-AG-0002');
    expect(screen.getByText('驳回原因')).toBeInTheDocument();
    expect(screen.getByText('AI 建议存在明显性别与生育偏见')).toBeInTheDocument();
    expect(screen.getByText(/AI 助手依据历史报价/)).toBeInTheDocument();
    expect(screen.getByText(/风险定级准确/)).toBeInTheDocument();
    expect(screen.getByText('已驳回')).toBeInTheDocument();
  });

  it('TestRejectedNoEditButton：REJECTED 题底部无「编辑」按钮（§4.1.3 查看题目）', async () => {
    fetchDetailMock.mockResolvedValue(makeDetail());

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('Q-AG-0002');
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭' })).toBeInTheDocument();
  });

  it('TestAiActiveEditButton：source=AI 且 ACTIVE 显示「编辑」并回调携带详情', async () => {
    fetchDetailMock.mockResolvedValue(makeDetail({ status: 'ACTIVE', reject_reason: '' }));
    const onEdit = vi.fn();

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={onEdit} />);

    const editBtn = await screen.findByRole('button', { name: '编辑' });
    editBtn.click();
    await waitFor(() =>
      expect(onEdit).toHaveBeenCalledWith(
        expect.objectContaining({ id: '1780000000000000101', status: 'ACTIVE' }),
      ),
    );
  });

  it('TestAiDisabledEditButton：source=AI 且 DISABLED 也显示「编辑」', async () => {
    fetchDetailMock.mockResolvedValue(makeDetail({ status: 'DISABLED', reject_reason: '' }));

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('Q-AG-0002');
    expect(screen.getByRole('button', { name: '编辑' })).toBeInTheDocument();
  });

  it('TestScaleNoEdit：SCALE 题无「编辑」按钮（§4.1.4 规则5）', async () => {
    fetchDetailMock.mockResolvedValue(
      makeDetail({
        source: 'SCALE',
        question_no: 'Q-Scale-0001',
        status: 'ACTIVE',
        reject_reason: '',
        answer_mode: 'LIKERT5',
      }),
    );

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('Q-Scale-0001');
    expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument();
    expect(screen.getByText('Likert 5 级')).toBeInTheDocument();
  });

  it('TestNoRejectBlock：非驳回题不渲染驳回原因区块', async () => {
    fetchDetailMock.mockResolvedValue(makeDetail({ status: 'ACTIVE', reject_reason: '' }));

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('Q-AG-0002');
    expect(screen.queryByText('驳回原因')).not.toBeInTheDocument();
  });

  it('TestIdNullNoFetch：questionId 为 null 时不发详情请求', () => {
    renderDialog(<QuestionViewDialog open questionId={null} onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    expect(fetchDetailMock).not.toHaveBeenCalled();
  });

  it('TestLoadingHint：加载中显示加载文案', async () => {
    fetchDetailMock.mockImplementation(() => new Promise(() => {}));

    renderDialog(<QuestionViewDialog open questionId="1780000000000000101" onOpenChange={vi.fn()} onEdit={vi.fn()} />);

    await screen.findByText('加载中…');
  });
});
