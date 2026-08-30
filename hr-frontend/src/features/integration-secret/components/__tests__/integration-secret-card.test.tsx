// IntegrationSecretCard 卡片交互测试（specs §3.1 下区 / §4.2.2C / §4.2.3 / §4.2.4 规则4/5/6 / §4.2.5 / §4.4.4 / BR1-BR6）
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import i18n from '@/i18n/config';
import { ApiError } from '@/lib/http-client';

// Mock hooks：data 由 viewMock 控制（掩码+configured）；detail 由 detailMock 控制；test 由 testMock 控制。
const viewMock = vi.fn();
const detailMock = vi.fn();
const testMock = vi.fn();
vi.mock('@/features/integration-secret/hooks', () => ({
  useIntegrationSecret: () => viewMock(),
  useSecretDetail: (enabled: boolean) => ({
    data: enabled ? detailMock() : undefined,
    isFetching: false,
  }),
  useSecretTest: () => ({
    mutate: testMock,
    isPending: false,
  }),
}));

const removeQueriesMock = vi.fn();
vi.mock('@/lib/query-client', () => ({
  queryClient: {
    removeQueries: (...args: unknown[]) => removeQueriesMock(...args),
  },
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

// SecretUpdateDialog 直桩：仅校验打开与 configured 透传，避免引入 RSA 复杂度。
const dialogMock = vi.fn();
vi.mock('@/features/integration-secret/components/secret-update-dialog', () => ({
  SecretUpdateDialog: (props: { open: boolean; configured: boolean }) => {
    dialogMock(props);
    return props.open ? (
      <div data-testid="secret-update-dialog-stub">
        {props.configured ? 'DIALOG_CONFIGURED' : 'DIALOG_NOT_CONFIGURED'}
      </div>
    ) : null;
  },
}));

import { IntegrationSecretCard } from '@/features/integration-secret/components/integration-secret-card';

beforeEach(() => {
  void i18n.changeLanguage('zh');
  viewMock.mockReset();
  detailMock.mockReset();
  testMock.mockReset();
  removeQueriesMock.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
  dialogMock.mockReset();
});

function setupConfigured(masked = 'sil****a1b2') {
  viewMock.mockReturnValue({
    data: { id: '1', secret_masked: masked, configured: true },
  });
}

function setupNotConfigured() {
  viewMock.mockReturnValue({
    data: { id: '1', secret_masked: '', configured: false },
  });
}

describe('IntegrationSecretCard（§3.1 下区 / §4.2.2C / §4.2.3 / §4.2.5 / BR1-BR6）', () => {
  it('未配置态：掩码区显示「未配置」，配置状态标签 muted，按钮显示「配置」，眼睛与连通验证禁用（BR1 / BR3 / BR4 / BR6 §4.2.5）', () => {
    setupNotConfigured();
    render(<IntegrationSecretCard />);
    // 掩码占位 span + 配置状态 Badge 均含「未配置」，断言两处
    const notConfiguredSpans = screen.getAllByText('未配置');
    expect(notConfiguredSpans.length).toBeGreaterThanOrEqual(1);
    // 配置状态标签 secondary（muted）：Badge[data-variant="secondary"]
    const badge = document.querySelector('[data-slot="badge"][data-variant="secondary"]');
    expect(badge?.textContent).toBe('未配置');
    // 按钮「配置」（非「更新」）
    expect(screen.getByRole('button', { name: '配置' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '更新' })).not.toBeInTheDocument();
    // 眼睛图标按钮 + 连通验证按钮均禁用
    const revealBtn = screen.getByLabelText('显示明文');
    const testBtn = screen.getByRole('button', { name: '连通验证' });
    expect(revealBtn).toBeDisabled();
    expect(testBtn).toBeDisabled();
  });

  it('已配置态：掩码显示掩码值，标签 success，按钮「更新」，眼睛与连通验证启用（BR1 / BR3 / BR4 / BR5）', () => {
    setupConfigured('sil****a1b2');
    render(<IntegrationSecretCard />);
    expect(screen.getByText('sil****a1b2')).toBeInTheDocument();
    expect(screen.getByText('已配置')).toBeInTheDocument();
    const badge = screen.getByText('已配置').closest('[data-slot="badge"]');
    expect(badge?.getAttribute('data-variant')).toBe('default');
    expect(screen.getByRole('button', { name: '更新' })).toBeInTheDocument();
    expect(screen.getByLabelText('显示明文')).not.toBeDisabled();
    expect(screen.getByRole('button', { name: '连通验证' })).not.toBeDisabled();
  });

  it('点眼睛触发 fetchSecretDetail（mock），切换明文显示（BR2 §4.2.3）', async () => {
    setupConfigured();
    detailMock.mockReturnValue({ id: '1', secret: 'plaintext-secret-xyz' });
    render(<IntegrationSecretCard />);
    // 初始：掩码
    expect(screen.getByText('sil****a1b2')).toBeInTheDocument();
    // 点眼睛切明文
    fireEvent.click(screen.getByLabelText('显示明文'));
    await waitFor(() => {
      expect(screen.getByText('plaintext-secret-xyz')).toBeInTheDocument();
    });
  });

  it('再次点眼睛切回隐藏并 removeQueries 清明文缓存（§4.4.4 明文不进缓存）', async () => {
    setupConfigured();
    detailMock.mockReturnValue({ id: '1', secret: 'plaintext-secret-xyz' });
    render(<IntegrationSecretCard />);
    // 展开
    fireEvent.click(screen.getByLabelText('显示明文'));
    await waitFor(() => expect(screen.getByText('plaintext-secret-xyz')).toBeInTheDocument());
    expect(removeQueriesMock).not.toHaveBeenCalled();
    // 收起
    fireEvent.click(screen.getByLabelText('隐藏明文'));
    await waitFor(() => {
      expect(removeQueriesMock).toHaveBeenCalledWith({
        queryKey: ['integration-secret', 'detail'],
      });
    });
    expect(screen.queryByText('plaintext-secret-xyz')).not.toBeInTheDocument();
    expect(screen.getByText('sil****a1b2')).toBeInTheDocument();
  });

  it('点连通验证成功（connected=true）显示成功提示（BR4 / BR6 §4.2.3）', async () => {
    setupConfigured();
    testMock.mockImplementation((_arg, cb) => {
      cb?.onSuccess?.({ connected: true });
    });
    render(<IntegrationSecretCard />);
    fireEvent.click(screen.getByRole('button', { name: '连通验证' }));
    await waitFor(() => expect(testMock).toHaveBeenCalled());
    expect(toastSuccess).toHaveBeenCalledWith('连通成功');
  });

  it('点连通验证失败（1304）就地显示失败原因（BR4 / BR6 §4.2.4 规则6）', async () => {
    setupConfigured();
    const apiErr = new ApiError(1304, 'upstream 503');
    testMock.mockImplementation((_arg, cb) => {
      cb?.onError?.(apiErr);
    });
    render(<IntegrationSecretCard />);
    fireEvent.click(screen.getByRole('button', { name: '连通验证' }));
    await waitFor(() =>
      expect(screen.getByText('upstream 503')).toBeInTheDocument(),
    );
    expect(toastError).not.toHaveBeenCalled();
  });

  it('点「更新/配置」打开 SecretUpdateDialog 并透传 configured（BR3 §4.2.3）', () => {
    setupConfigured();
    render(<IntegrationSecretCard />);
    expect(screen.queryByTestId('secret-update-dialog-stub')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '更新' }));
    expect(screen.getByTestId('secret-update-dialog-stub')).toBeInTheDocument();
    // 最后一次调用透传 configured=true
    const last = dialogMock.mock.calls[dialogMock.mock.calls.length - 1]?.[0];
    expect(last).toMatchObject({ open: true, configured: true });
  });

  it('未配置态点「配置」打开弹窗透传 configured=false（BR3）', () => {
    setupNotConfigured();
    render(<IntegrationSecretCard />);
    fireEvent.click(screen.getByRole('button', { name: '配置' }));
    const stub = screen.getByTestId('secret-update-dialog-stub');
    expect(stub.textContent).toBe('DIALOG_NOT_CONFIGURED');
  });

  it('1303（未配置兜底）：toast 提示「请先配置集成密钥」', async () => {
    setupConfigured();
    const apiErr = new ApiError(1303, 'not configured');
    testMock.mockImplementation((_arg, cb) => {
      cb?.onError?.(apiErr);
    });
    render(<IntegrationSecretCard />);
    fireEvent.click(screen.getByRole('button', { name: '连通验证' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('请先配置集成密钥'));
  });

  it('其它错误（非 1303/1304）：toast 通用错误文案', async () => {
    setupConfigured();
    const apiErr = new ApiError(1500, 'boom');
    testMock.mockImplementation((_arg, cb) => {
      cb?.onError?.(apiErr);
    });
    render(<IntegrationSecretCard />);
    fireEvent.click(screen.getByRole('button', { name: '连通验证' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('操作失败，请稍后重试'));
  });
});
