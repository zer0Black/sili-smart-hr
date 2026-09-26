package scaledata

import "sili-smart-hr/backend/internal/domain"

// essenceTemplate Essence 精简量表（全集 108 题约 18 分钟，specs 4.1.2 F）。
// 样本期收录 9 题（9 型各 1 题），简短直接陈述为该量表经典风格。
// EstimatedMinutes 按题数等比折算（18×9/108≈2），全集替换时改回全集口径。
func essenceTemplate() ScaleTemplate {
	items := []ScaleItem{
		{Statement: "我力求把每件事都做对", DimensionCode: "ENNE_TYPE_1_REFORMER",
			FocusPoint: "计分键：本题计入完美型（1 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我乐于帮助身边的人", DimensionCode: "ENNE_TYPE_2_HELPER",
			FocusPoint: "计分键：本题计入助人型（2 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我把达成目标看得很重", DimensionCode: "ENNE_TYPE_3_ACHIEVER",
			FocusPoint: "计分键：本题计入成就型（3 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我觉得自己是敏感而独特的人", DimensionCode: "ENNE_TYPE_4_INDIVIDUALIST",
			FocusPoint: "计分键：本题计入自我型（4 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我喜欢独自思考和研究问题", DimensionCode: "ENNE_TYPE_5_INVESTIGATOR",
			FocusPoint: "计分键：本题计入智慧型（5 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我做事前会先考虑风险", DimensionCode: "ENNE_TYPE_6_LOYALIST",
			FocusPoint: "计分键：本题计入忠诚型（6 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我总是在寻找新的乐趣", DimensionCode: "ENNE_TYPE_7_ENTHUSIAST",
			FocusPoint: "计分键：本题计入活跃型（7 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我敢于直接面对挑战", DimensionCode: "ENNE_TYPE_8_CHALLENGER",
			FocusPoint: "计分键：本题计入领袖型（8 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我随和，容易与人相处", DimensionCode: "ENNE_TYPE_9_PEACEMAKER",
			FocusPoint: "计分键：本题计入调停型（9 型）倾向聚合，认同度越高该型别倾向得分越高。"},
	}
	for i := range items {
		items[i].Requirement = likertRequirement
	}
	return ScaleTemplate{
		ScaleKey:         domain.ScaleKeyEssence,
		Name:             "Essence 精简量表",
		QuestionCount:    len(items),
		EstimatedMinutes: 2,
		Description:      "精简版九型人格量表，作答负担更轻，适合快速摸底与大面积铺开的场景。",
		Dimensions:       enneDimensions,
		Items:            items,
	}
}
