// Package activity 是使用活跃度统计子域：纯规则统计，不调 LLM。
package activity

import (
	"encoding/json"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
)

// ProfileDigest 档案消费视图（specs §2.2）：本组件只读四块内容与 status，
// 不感知抽取内部结构。ProfileJSON 仅内存临时持有，禁止落日志。
type ProfileDigest struct {
	SessionKey  string
	Status      string // success / failed / skipped
	Client      string // 主客户端标识（T4 分层探测落库列，人群签名维度）
	Stats       extractor.ProfileStats
	ProfileJSON string    // 原始档案 JSON，仅内存临时持有
	LastTurn    time.Time // 末轮时间（归一过滤判据，from domain 行 LastTurnAt），T4 评分侧消费
}

// digestJSON 是 ProfileJSON 的解析形态：顶层键与 extractor 落库序列化对齐
// （FeatureProfile 无 json tag，键名为字段名原样）。
type digestJSON struct {
	Stats extractor.ProfileStats
}

// ParseDigests 把 domain.SessionFeature 行转 ProfileDigest 列表：
// json.Unmarshal ProfileJSON 取 Stats（失败或 skipped 行零值）。
// 块可用性由消费方按 Status==success 判（success 行三块必在）。
// 坏 JSON 行 Stats 落零值不报错（签名判定只依赖 status/client 分布，单行统计缺失可容忍）。
func ParseDigests(rows []domain.SessionFeature) []ProfileDigest {
	ds := make([]ProfileDigest, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		d := ProfileDigest{
			SessionKey:  r.SessionKey,
			Status:      r.Status,
			Client:      r.Client,
			ProfileJSON: r.ProfileJSON,
			LastTurn:    r.LastTurnAt,
		}
		if r.ProfileJSON != "" {
			var pj digestJSON
			if json.Unmarshal([]byte(r.ProfileJSON), &pj) == nil {
				d.Stats = pj.Stats
			}
		}
		ds = append(ds, d)
	}
	return ds
}
