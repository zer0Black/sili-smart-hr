package fallback

import (
	"context"
	"log/slog"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// AlertWriter 告警写入组件（依赖注入仓储）。
type AlertWriter struct {
	repo repository.AssessmentAlertRepository
}

// NewAlertWriter 组装告警写入组件。
func NewAlertWriter(repo repository.AssessmentAlertRepository) *AlertWriter {
	return &AlertWriter{repo: repo}
}

// WriteAlert 批次终态超阈时写告警信号：ratio 经 FailedRatioPercent 单一口径
//（TotalCount<=0 返 0，无 NaN 落库路径）。写入失败仅记 ERROR 日志返回 nil
//（specs §5.3.5 兜底不再上抛）。
func (w *AlertWriter) WriteAlert(ctx context.Context, batch *domain.AssessmentBatch) error {
	alert := &domain.AssessmentAlert{
		BatchID:     batch.ID,
		BatchNo:     batch.BatchNo,
		FailedCount: batch.FailedCount,
		TotalCount:  batch.TotalCount,
		FailedRatio: FailedRatioPercent(batch.FailedCount, batch.TotalCount),
		SignaledAt:  time.Now().UTC(),
	}
	if err := w.repo.UpsertByBatch(ctx, alert); err != nil {
		slog.Error("告警信号写入失败", "batch_no", batch.BatchNo, "error", err)
	}
	return nil
}
