// scale_test 量表引入 service 层测试：fake repo 注入，不连真库。
// 覆盖 03 §3.11/§3.12 业务规则（候选列表 imported 标记、引入校验与 1704 映射）。
package service_test

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/questionbank/scaledata"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// scaleFakeRepo 是 ScaleRepository 的测试假实现：返回值挂载 + 调用探针。
type scaleFakeRepo struct {
	countRes map[string]int64
	countErr error

	importTpl *scaledata.ScaleTemplate
	importRes *domain.QuestionBatch
	importErr error
	importHit bool
}

func (r *scaleFakeRepo) CountImported(_ context.Context, _ *gorm.DB, keys []string) (map[string]int64, error) {
	if r.countErr != nil {
		return nil, r.countErr
	}
	// 补 0 语义与真实 repo 一致，fake 只需回填挂载值。
	out := make(map[string]int64, len(keys))
	for _, k := range keys {
		if v, ok := r.countRes[k]; ok {
			out[k] = v
		} else {
			out[k] = 0
		}
	}
	return out, nil
}

func (r *scaleFakeRepo) LockEnneagramDimensions(_ context.Context, _ *gorm.DB, _ []string) ([]domain.Dimension, error) {
	return nil, nil
}

func (r *scaleFakeRepo) EnsureEnneagramDimensions(_ context.Context, _ *gorm.DB, _ []scaledata.ScaleDimension) (map[string]int64, error) {
	return nil, nil
}

func (r *scaleFakeRepo) ImportScale(_ context.Context, tpl *scaledata.ScaleTemplate, _ time.Time) (*domain.QuestionBatch, error) {
	r.importHit = true
	r.importTpl = tpl
	return r.importRes, r.importErr
}

var _ repository.ScaleRepository = (*scaleFakeRepo)(nil)

func newScaleSvc(repo repository.ScaleRepository) service.ScaleService {
	return service.NewScaleService(repo)
}

// ---------- ListScales ----------

// TestListScalesImportedFlag 核心断言：库中存在 RISO_HUDSON 未删题（计数>0）时
// 该候选项 Imported=true、另一套 false（03 §3.11、BR1）。
func TestListScalesImportedFlag(t *testing.T) {
	repo := &scaleFakeRepo{countRes: map[string]int64{"RISO_HUDSON": 9}}
	svc := newScaleSvc(repo)

	list, err := svc.ListScales(context.Background())
	if err != nil {
		t.Fatalf("ListScales: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(list))
	}
	for _, c := range list {
		if c.ScaleKey == "RISO_HUDSON" && !c.Imported {
			t.Fatalf("RISO_HUDSON imported want true, got %+v", c)
		}
		if c.ScaleKey == "ESSENCE" && c.Imported {
			t.Fatalf("ESSENCE imported want false, got %+v", c)
		}
	}
}

// TestListScalesTemplateFieldsAssembled 候选字段来自 scaledata.Templates()（scale_key/
// name/question_count/estimated_minutes/description）与模板一致。
func TestListScalesTemplateFieldsAssembled(t *testing.T) {
	svc := newScaleSvc(&scaleFakeRepo{})

	list, err := svc.ListScales(context.Background())
	if err != nil {
		t.Fatalf("ListScales: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(list))
	}
	c0 := list[0]
	if c0.ScaleKey != "RISO_HUDSON" || c0.Name == "" || c0.Description == "" ||
		c0.QuestionCount <= 0 || c0.EstimatedMinutes <= 0 {
		t.Fatalf("candidate0 fields not assembled from template: %+v", c0)
	}
	c1 := list[1]
	if c1.ScaleKey != "ESSENCE" || c1.Name == "" || c1.QuestionCount <= 0 {
		t.Fatalf("candidate1 fields not assembled from template: %+v", c1)
	}
}

// TestListScalesAllImported 两套均已在库：全部 Imported=true（两套并存各自判定）。
func TestListScalesAllImported(t *testing.T) {
	repo := &scaleFakeRepo{countRes: map[string]int64{"RISO_HUDSON": 144, "ESSENCE": 108}}
	svc := newScaleSvc(repo)
	list, err := svc.ListScales(context.Background())
	if err != nil {
		t.Fatalf("ListScales: %v", err)
	}
	for _, c := range list {
		if !c.Imported {
			t.Fatalf("want imported=true for %s, got %+v", c.ScaleKey, c)
		}
	}
}

// TestListScalesRepoError CountImported 仓储错误透传非业务错误。
func TestListScalesRepoError(t *testing.T) {
	svc := newScaleSvc(&scaleFakeRepo{countErr: gorm.ErrInvalidDB})
	if _, err := svc.ListScales(context.Background()); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- ImportScale ----------

// TestImportScaleSuccess 引入成功：批次信息组装（batch_id string 化、batch_no、题数取模板），
// 透传 repo 的模板即 FindByKey 命中的模板。
func TestImportScaleSuccess(t *testing.T) {
	repo := &scaleFakeRepo{importRes: &domain.QuestionBatch{
		ID: 901, BatchNo: "#S0925", QuestionCount: 9,
	}}
	svc := newScaleSvc(repo)

	res, err := svc.ImportScale(context.Background(), "RISO_HUDSON")
	if err != nil {
		t.Fatalf("ImportScale: %v", err)
	}
	if res.BatchID != "901" || res.BatchNo != "#S0925" || res.QuestionCount != 9 {
		t.Fatalf("result mismatch: %+v", res)
	}
	if !repo.importHit || repo.importTpl == nil || repo.importTpl.ScaleKey != "RISO_HUDSON" {
		t.Fatalf("repo.ImportScale not invoked with matched template: %+v", repo.importTpl)
	}
}

// TestImportUnknownKey 核心断言：scale_key="NOPE" 未命中模板返 1400 且事务未触发。
func TestImportUnknownKey(t *testing.T) {
	repo := &scaleFakeRepo{}
	svc := newScaleSvc(repo)
	_, err := svc.ImportScale(context.Background(), "NOPE")
	wantQCode(t, err, 1400)
	if repo.importHit {
		t.Fatal("repo.ImportScale must not run on unknown scale_key")
	}
}

// TestImportEmptyKey 空串 scale_key 同样未命中模板返 1400。
func TestImportEmptyKey(t *testing.T) {
	svc := newScaleSvc(&scaleFakeRepo{})
	_, err := svc.ImportScale(context.Background(), "")
	wantQCode(t, err, 1400)
}

// TestImportAlreadyImported 核心断言：repo 返回 ErrScaleImported 映射 1704（BR2）。
func TestImportAlreadyImported(t *testing.T) {
	svc := newScaleSvc(&scaleFakeRepo{importErr: repository.ErrScaleImported})
	_, err := svc.ImportScale(context.Background(), "RISO_HUDSON")
	wantQCode(t, err, 1704)
}

// TestImportRepoError 其他仓储错误透传非业务错误。
func TestImportRepoError(t *testing.T) {
	svc := newScaleSvc(&scaleFakeRepo{importErr: gorm.ErrInvalidDB})
	if _, err := svc.ImportScale(context.Background(), "ESSENCE"); err == nil {
		t.Fatal("want error, got nil")
	}
}
