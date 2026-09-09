// 维度域表单字段长度上限单一来源，与后端 hr-backend/internal/service/dimension.go 的
// dimNameMin/Max、dimAnchorMax、dimPromptMax、dimDescriptionMax、dimWeightMax 对齐，改限制时两端同步。
// 长度计数按 Unicode 码点（countRunes），对齐后端 utf8.RuneCountInString 口径。
import { countRunes } from '@/lib/validation';

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

/** 码点口径下限校验，配 zod .refine 使用。 */
export const runeLengthAtLeast = (min: number) => (v: string) => countRunes(v) >= min;

/** 码点口径上限校验，配 zod .refine 使用。 */
export const runeLengthAtMost = (max: number) => (v: string) => countRunes(v) <= max;
