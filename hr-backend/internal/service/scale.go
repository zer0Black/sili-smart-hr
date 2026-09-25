// scale 量表引入业务层：候选列表组装（scaledata 模板 + 实时 imported 标记）与
// 引入编排的 scale_key 校验、哨兵映射（specs P2_QBN_001 §4.1.2 F/§4.1.4 规则 8/9，03 §3.11/§3.12）。
// 引入事务主体（行锁 + 维度 ensure + 建批）收口在 repository.ImportScale。
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/questionbank/scaledata"
	"sili-smart-hr/backend/internal/repository"
)

// ScaleCandidateDTO 量表候选卡（03 §3.11）。Imported 由 questions 表实时计算，
// 前端据此置灰并标注「已引入」。
type ScaleCandidateDTO struct {
	ScaleKey         string `json:"scale_key"`
	Name             string `json:"name"`
	QuestionCount    int    `json:"question_count"`
	EstimatedMinutes int    `json:"estimated_minutes"`
	Description      string `json:"description"`
	Imported         bool   `json:"imported"`
}

// ImportScaleResult 引入成功响应（03 §3.12）。
type ImportScaleResult struct {
	BatchID       string `json:"batch_id"`
	BatchNo       string `json:"batch_no"`
	QuestionCount int    `json:"question_count"`
}

// ScaleService 量表引入域业务接口。
type ScaleService interface {
	ListScales(ctx context.Context) ([]ScaleCandidateDTO, error)
	ImportScale(ctx context.Context, scaleKey string) (*ImportScaleResult, error)
}

type scaleService struct {
	repo repository.ScaleRepository
}

func NewScaleService(repo repository.ScaleRepository) ScaleService {
	return &scaleService{repo: repo}
}

// ListScales 候选列表（03 §3.11）：模板静态字段 + CountImported 实时填 Imported
//（引入/作废/删除后状态即时变化，specs 规则 8）。
func (s *scaleService) ListScales(ctx context.Context) ([]ScaleCandidateDTO, error) {
	tpls := scaledata.Templates()
	keys := make([]string, len(tpls))
	for i := range tpls {
		keys[i] = tpls[i].ScaleKey
	}
	counts, err := s.repo.CountImported(ctx, nil, keys)
	if err != nil {
		return nil, fmt.Errorf("count imported scales: %w", err)
	}
	list := make([]ScaleCandidateDTO, 0, len(tpls))
	for i := range tpls {
		tpl := &tpls[i]
		list = append(list, ScaleCandidateDTO{
			ScaleKey:         tpl.ScaleKey,
			Name:             tpl.Name,
			QuestionCount:    tpl.QuestionCount,
			EstimatedMinutes: tpl.EstimatedMinutes,
			Description:      tpl.Description,
			Imported:         counts[tpl.ScaleKey] > 0,
		})
	}
	return list, nil
}

// ImportScale 引入编排（03 §3.12）：scale_key 未命中内置模板返 1400，事务与并发
// 拦截收口在 repo；ErrScaleImported 映射 1704（specs §4.1.4 规则 9 单独提示）。
func (s *scaleService) ImportScale(ctx context.Context, scaleKey string) (*ImportScaleResult, error) {
	tpl, ok := scaledata.FindByKey(scaleKey)
	if !ok {
		return nil, NewError(errcode.BadRequest)
	}
	batch, err := s.repo.ImportScale(ctx, tpl, time.Now())
	if err != nil {
		if errors.Is(err, repository.ErrScaleImported) {
			return nil, NewError(errcode.ScaleAlreadyImported)
		}
		return nil, fmt.Errorf("import scale %s: %w", scaleKey, err)
	}
	return &ImportScaleResult{
		BatchID:       int64ToString(batch.ID),
		BatchNo:       batch.BatchNo,
		QuestionCount: batch.QuestionCount,
	}, nil
}
