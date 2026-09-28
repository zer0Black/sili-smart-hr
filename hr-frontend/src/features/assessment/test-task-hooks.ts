// 主动测试任务 hooks（specs P2_TST_001 §4.1.3 轮询 / §4.2 / §4.3）。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { Page } from '@/lib/contracts';
import { usePageVisible } from '@/lib/use-page-visible';
import { useAuthStore } from '@/stores/auth';

import {
  cancelTestTask,
  createTestTask,
  fetchScaleStatus,
  fetchTestTaskLink,
  fetchTestTaskPollCounts,
  fetchTestTasks,
  resendTestTaskLink,
  type CreateTestTaskResult,
} from './test-task-api';
import type { CreateTestTaskPayload, TestTaskFilter, TestTaskLinkInfo, TestTaskListItem } from './test-task-types';

const POLLING_INTERVAL_MS = 10_000;

/**
 * useTestTasks：单类型任务列表。polling 为 true 且页面可见时 10s 轮询
 *（specs §4.1.3：开关由页面级 poll-counts 探针驱动，口径与批次列表同构）。
 */
export function useTestTasks(filter: TestTaskFilter, opts?: { polling?: boolean }) {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<Page<TestTaskListItem>>({
    queryKey: ['test-tasks', filter],
    queryFn: () => fetchTestTasks(filter),
    enabled: !!token,
    refetchInterval: opts?.polling && visible ? POLLING_INTERVAL_MS : false,
  });
}

/**
 * useTestTaskPollCounts：轮询探针，自驱动（仿 useBatchStats）：任一类计数 > 0
 * 且页面可见时 10s 续轮询，全终态即停；error 态继续轮询自愈，防一次瞬时失败
 * 冻结探针。页面隐藏时暂停，恢复可见时 TanStack Query 自动续上。
 */
export function useTestTaskPollCounts() {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery({
    queryKey: ['test-tasks', 'poll-counts'],
    queryFn: fetchTestTaskPollCounts,
    enabled: !!token,
    refetchInterval: (query) => {
      if (!visible) return false;
      const d = query.state.data;
      if (d) {
        return d.ai_mgmt_active > 0 || d.enneagram_active > 0
          ? POLLING_INTERVAL_MS
          : false;
      }
      return query.state.status === 'error' ? POLLING_INTERVAL_MS : false;
    },
  });
}

/** useCreateTestTask：发起主动测试。成功后废弃 test-tasks 前缀（列表与计数一并刷新）。 */
export function useCreateTestTask() {
  const qc = useQueryClient();
  return useMutation<CreateTestTaskResult, Error, CreateTestTaskPayload>({
    mutationFn: createTestTask,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['test-tasks'] });
    },
  });
}

/** useScaleStatus：九型量表就绪查询（发起弹窗只读展示，不轮询）。 */
export function useScaleStatus() {
  const token = useAuthStore((s) => s.token);
  return useQuery({
    queryKey: ['test-tasks', 'scale-status'],
    queryFn: fetchScaleStatus,
    enabled: !!token,
  });
}

/** useTestTaskLink：作答链接查询（弹窗打开时刻快照，specs §4.3.5 不轮询）。 */
export function useTestTaskLink(taskId: string | null) {
  const token = useAuthStore((s) => s.token);
  return useQuery<TestTaskLinkInfo>({
    queryKey: ['test-tasks', 'link', taskId],
    queryFn: () => fetchTestTaskLink(taskId as string),
    enabled: !!taskId && !!token,
  });
}

/** useResendTestTaskLink：重发作答链接。成功后废弃 test-tasks 前缀（列表 link_status 刷新）。 */
export function useResendTestTaskLink() {
  const qc = useQueryClient();
  return useMutation<TestTaskLinkInfo, Error, string>({
    mutationFn: resendTestTaskLink,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['test-tasks'] });
    },
  });
}

/** useCancelTestTask：取消任务。成功后废弃 test-tasks 前缀，行状态呈现已取消。 */
export function useCancelTestTask() {
  const qc = useQueryClient();
  return useMutation<{ task_id: string; status: string }, Error, string>({
    mutationFn: cancelTestTask,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['test-tasks'] });
    },
  });
}
