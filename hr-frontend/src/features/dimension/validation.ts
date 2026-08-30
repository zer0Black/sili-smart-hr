// 维度域表单字段长度上限单一来源，与后端 hr-backend/internal/service/dimension.go 的
// dimNameMin/Max、dimAnchorMax、dimPromptMax、dimDescriptionMax、dimWeightMax 对齐，改限制时两端同步。
export const DIM_FIELD_LIMITS = {
  nameMin: 2,
  nameMax: 30,
  anchorMin: 1,
  anchorMax: 500,
  promptMax: 2000,
  descriptionMax: 300,
  weightMin: 0,
  weightMax: 100,
} as const;
