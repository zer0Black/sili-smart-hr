// StaffSingleSelect 测试（specs P2_TST_001 §4.2.2 A 测评对象：单选下拉可搜索，必填限一人，无全员项）
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import i18n from '@/i18n/config';

const useStaffsMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/system-params/hooks', () => ({
  useStaffs: (...args: unknown[]) => useStaffsMock(...args),
}));

import { StaffSingleSelect } from '@/features/assessment/components/staff-single-select';
import type { StaffItem } from '@/lib/contracts';

const STAFF_A: StaffItem = { staff_id: 'u1', staff_name: '张三' };
const STAFF_B: StaffItem = { staff_id: 'u2', staff_name: '李四' };

function okResult(list: StaffItem[], total?: number) {
  return {
    data: { list, total: total ?? list.length, page: 1, page_size: 20 },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  };
}

function renderSelect(
  value: { staff_id: string; staff_name: string } | null = null,
  onChange = vi.fn(),
) {
  render(<StaffSingleSelect value={value} onChange={onChange} />);
  return onChange;
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  useStaffsMock.mockReset();
  useStaffsMock.mockReturnValue(okResult([STAFF_A, STAFF_B]));
});

describe('StaffSingleSelect（specs §4.2.2 A 测评对象单选）', () => {
  it('TestStaffSingleQuery：键入关键字触发 useStaffs(keyword,1,20) 查询', async () => {
    renderSelect();

    fireEvent.click(screen.getByText('点击选择测评对象'));
    await waitFor(() => expect(useStaffsMock).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('输入人名搜索'), {
      target: { value: '张' },
    });
    await waitFor(() =>
      expect(useStaffsMock).toHaveBeenLastCalledWith('张', 1, 20),
    );
  });

  it('TestStaffSinglePick：点击选项 onChange 携 {staff_id,staff_name} 且选中即收起', async () => {
    const onChange = renderSelect();
    fireEvent.click(screen.getByText('点击选择测评对象'));

    fireEvent.click(await screen.findByRole('button', { name: /张三/ }));
    expect(onChange).toHaveBeenLastCalledWith({ staff_id: 'u1', staff_name: '张三' });

    // 选中即收起：下拉选项面板卸载
    await waitFor(() =>
      expect(screen.queryByPlaceholderText('输入人名搜索')).not.toBeInTheDocument(),
    );
  });

  it('TestStaffSingleNoAll：下拉选项无「全员」项（决策 12 限一人）', async () => {
    renderSelect();
    fireEvent.click(screen.getByText('点击选择测评对象'));

    await screen.findByRole('button', { name: /张三/ });
    expect(screen.queryByText('全员')).not.toBeInTheDocument();
  });

  it('TestStaffSingleErrorRetry：接口 error 时下拉内显错误与重试按钮', async () => {
    const refetch = vi.fn();
    useStaffsMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch,
    });
    const onChange = renderSelect();

    fireEvent.click(screen.getByText('点击选择测评对象'));
    expect(await screen.findByText('人员加载失败')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(refetch).toHaveBeenCalledTimes(1);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('已有选中值时触发框显示姓名，可清除回 null', async () => {
    const onChange = renderSelect(STAFF_A);

    expect(screen.getByText('张三')).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('清除 张三'));
    expect(onChange).toHaveBeenLastCalledWith(null);
  });

  it('再次点击其他选项替换原选中（单选互斥）', async () => {
    const onChange = renderSelect(STAFF_A);
    fireEvent.click(screen.getByText('点击选择测评对象'));

    fireEvent.click(await screen.findByRole('button', { name: /李四/ }));
    expect(onChange).toHaveBeenLastCalledWith({ staff_id: 'u2', staff_name: '李四' });
  });

  it('展开即以空 keyword 第 1 页加载首屏', async () => {
    renderSelect();
    fireEvent.click(screen.getByText('点击选择测评对象'));
    await waitFor(() => expect(useStaffsMock).toHaveBeenCalledWith('', 1, 20));
  });
});
