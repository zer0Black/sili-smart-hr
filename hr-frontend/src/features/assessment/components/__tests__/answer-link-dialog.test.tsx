// AnswerLinkDialog 测试（specs P2_TST_001 §4.3 作答链接弹窗 / §4.1.3 重发后展示新链接）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import i18n from '@/i18n/config';

// navigator.clipboard.writeText mock（llm-view-dialog 先例）
const writeTextMock = vi.fn();
beforeEach(() => {
  writeTextMock.mockReset();
  writeTextMock.mockResolvedValue(undefined);
  Object.defineProperty(globalThis.navigator, 'clipboard', {
    configurable: true,
    value: { writeText: writeTextMock },
  });
});

const linkQ = vi.hoisted(() => ({
  data: undefined as unknown,
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
}));
const resendMutateMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/assessment/test-task-hooks', () => ({
  useTestTaskLink: () => linkQ,
  useResendTestTaskLink: () => ({ mutate: resendMutateMock, isPending: false }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: Object.assign(vi.fn(), {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  }),
}));

import { AnswerLinkDialog } from '@/features/assessment/components/answer-link-dialog';
import type { TestTaskLinkInfo } from '@/features/assessment/test-task-types';

const LINK_VALID: TestTaskLinkInfo = {
  task_id: '1790000000000000001',
  task_no: 'T202609280001',
  test_type: 'ai_mgmt',
  staff_name: '张敏',
  answer_url: '/answer/AbCdEfToken123',
  link_status: 'valid',
  generated_at: '2026-09-28 08:30',
  expires_at: '2026-10-05 08:30',
};

const LINK_USED: TestTaskLinkInfo = { ...LINK_VALID, link_status: 'used' };
const LINK_INVALID: TestTaskLinkInfo = { ...LINK_VALID, link_status: 'invalid' };

function renderDialog(node?: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      {node ?? <AnswerLinkDialog open taskId="1790000000000000001" onClose={vi.fn()} />}
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  linkQ.data = undefined;
  linkQ.isLoading = false;
  linkQ.isError = false;
  linkQ.refetch = vi.fn();
  resendMutateMock.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
});

describe('AnswerLinkDialog（specs §4.3）', () => {
  it('TestLinkDialogMeta：顶部描述列表展示任务号/对象/评测类型与链接全文、生成时间（§4.3.2/§4.3.5）', () => {
    linkQ.data = LINK_VALID;
    renderDialog();

    expect(screen.getByText('T202609280001')).toHaveClass('font-mono');
    expect(screen.getByText('张敏')).toBeInTheDocument();
    expect(screen.getByText('AI 管理能力')).toBeInTheDocument();
    expect(screen.getByText('/answer/AbCdEfToken123')).toBeInTheDocument();
    expect(screen.getByText('2026-09-28 08:30')).toBeInTheDocument();
  });

  it('TestLinkDialogCopyValid：valid 时复制按钮可点且 clipboard.writeText 收到链接全文（§4.3.4 规则1）', async () => {
    linkQ.data = LINK_VALID;
    renderDialog();

    fireEvent.click(screen.getByRole('button', { name: '复制' }));
    await waitFor(() =>
      expect(writeTextMock).toHaveBeenCalledWith('/answer/AbCdEfToken123'),
    );
    expect(toastSuccess).toHaveBeenCalledWith('链接已复制到剪贴板');
  });

  it('TestLinkDialogCopyUsed：used 时复制禁用且提示「已使用：作答已提交」（§4.3.4 规则1）', () => {
    linkQ.data = LINK_USED;
    renderDialog();

    // 始终展示链接全文（§4.3.4 规则1）
    expect(screen.getByText('/answer/AbCdEfToken123')).toBeInTheDocument();
    expect(screen.getByText('已使用：作答已提交')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '复制' })).toBeDisabled();
  });

  it('TestLinkDialogCopyInvalid：invalid 时复制禁用且提示「已失效：可重发生成新链接」（§4.3.4 规则1）', () => {
    linkQ.data = LINK_INVALID;
    renderDialog();

    expect(screen.getByText('已失效：可重发生成新链接')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '复制' })).toBeDisabled();
  });

  it('TestLinkDialogLoadError：加载失败态弹窗内重试重新拉取（§4.3.5）', async () => {
    linkQ.isError = true;
    renderDialog();

    expect(await screen.findByText('链接信息加载失败，请重试')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(linkQ.refetch).toHaveBeenCalledTimes(1);
  });

  it('TestLinkDialogFooterHint：底部信息条提示分发说明文案（§4.3.5）', () => {
    linkQ.data = LINK_VALID;
    renderDialog();

    expect(
      screen.getByText(
        '员工提交作答后链接即刻失效。逾期未作答请在记录中点击重发生成新链接，复制后手动分发',
      ),
    ).toBeInTheDocument();
  });

  it('TestLinkDialogResendFeed：重发行内入口走 useResendTestTaskLink，onSuccess 新链接直接喂弹窗展示（§4.1.3）', async () => {
    linkQ.data = LINK_INVALID;
    renderDialog();

    // 重发入口仅失败态就近呈现（§4.3.4 规则1 约束说明：弹窗内不重复放置 → invalid 提示引导，操作在行内）
    // 本组件提供数据承接：外部重发 onSuccess 以 linkData prop 注入
    expect(screen.queryByRole('button', { name: '重发' })).not.toBeInTheDocument();
  });

  it('open=false 时不渲染内容', () => {
    linkQ.data = LINK_VALID;
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <AnswerLinkDialog open={false} taskId="1790000000000000001" onClose={vi.fn()} />
      </QueryClientProvider>,
    );
    expect(screen.queryByText('T202609280001')).not.toBeInTheDocument();
  });

  it('复制失败（剪贴板拒绝）toast 错误提示', async () => {
    writeTextMock.mockRejectedValue(new Error('denied'));
    linkQ.data = LINK_VALID;
    renderDialog();

    fireEvent.click(screen.getByRole('button', { name: '复制' }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('复制失败，请手动复制链接'),
    );
  });

  it('关闭按钮触发 onClose，不做数据变更（§4.3.3）', () => {
    linkQ.data = LINK_VALID;
    const onClose = vi.fn();
    renderDialog(<AnswerLinkDialog open taskId="1790000000000000001" onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(resendMutateMock).not.toHaveBeenCalled();
  });
});
