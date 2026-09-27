// 题库域状态 → Badge variant 映射（specs §4.1.2 B/D）。
// 列表恒排除 PENDING 无需分支；详情可查 PENDING（批次审核视图复用），经参数区分。
type QuestionStatus = 'ACTIVE' | 'DISABLED' | 'REJECTED' | 'PENDING';

/** withPending=false 列表用（无 PENDING）；true 详情用（PENDING 同驳回 destructive）。 */
export function questionStatusVariant(
  status: QuestionStatus,
  withPending: boolean,
): 'default' | 'secondary' | 'destructive' {
  if (status === 'DISABLED') return 'secondary';
  if (status === 'REJECTED' || (withPending && status === 'PENDING')) return 'destructive';
  return 'default';
}
