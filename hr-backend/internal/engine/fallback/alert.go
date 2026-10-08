package fallback

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// AlertWriter 告警写入组件（依赖注入仓储）。
type AlertWriter struct {
	repo     repository.AssessmentAlertRepository
	opLogger PendingLogRecorder // 逾期取消/告警节点投递面（specs P4_LOG_001 §5.2），nil 安全
}

// PendingLogRecorder 告警节点窄投递接口（service.PendingLogRecorder 同构，
// 本包定义避免 fallback→service 反向依赖成环：service 经 pipeline 间接引
// fallback）。*service.OperationLogRecorder 鸭子满足，装配在 app 包收敛。
type PendingLogRecorder interface {
	Record(entry PendingLog)
}

// PendingLog 告警节点事件载荷（service.PendingLog 同构，字段语义见其对端）。
type PendingLog struct {
	AccountID          int64
	FallbackName       string
	FallbackUsername   string
	Module, Target     string
	Summary, Result    string
	Detail, RequestPath string
	Changes            []domain.ChangeItem
}

// NewAlertWriter 组装告警写入组件。
func NewAlertWriter(repo repository.AssessmentAlertRepository, opLogger PendingLogRecorder) *AlertWriter {
	return &AlertWriter{repo: repo, opLogger: opLogger}
}

// WriteAlert 批次终态超阈时写告警信号：ratio 经 FailedRatioPercent 单一口径
//（TotalCount<=0 返 0，无 NaN 落库路径）。写入失败仅记 ERROR 日志返回 nil
//（specs §5.3.5 兜底不再上抛）。Upsert 成功后记告警节点（specs P4_LOG_001
// §5.2 失败类节点，result=fail；Record 异步旁路不阻断）。
func (w *AlertWriter) WriteAlert(ctx context.Context, batch *domain.AssessmentBatch) error {
	ratio := FailedRatioPercent(batch.FailedCount, batch.TotalCount)
	alert := &domain.AssessmentAlert{
		BatchID:     batch.ID,
		BatchNo:     batch.BatchNo,
		FailedCount: batch.FailedCount,
		TotalCount:  batch.TotalCount,
		FailedRatio: ratio,
		SignaledAt:  time.Now().UTC(),
	}
	if err := w.repo.UpsertByBatch(ctx, alert); err != nil {
		slog.Error("告警信号写入失败", "batch_no", batch.BatchNo, "error", err)
		return nil
	}
	if w.opLogger != nil {
		w.opLogger.Record(PendingLog{
			FallbackName: domain.OpOperatorSystem,
			Module:       domain.OpModuleSystemJob,
			Target: fmt.Sprintf("批次 %s（%s ~ %s）", batch.BatchNo,
				batch.PeriodStartAt.Local().Format("2006-01-02"),
				batch.PeriodEndAt.Local().Format("2006-01-02")),
			Summary: fmt.Sprintf("失败人数占比 %.2f%% 超阈值 10%%，告警已写入", ratio),
			Result:  domain.OpResultFail,
		})
	}
	return nil
}
