package scaledata

// risoHudsonTemplate Riso-Hudson 标准量表（全集 144 题约 25 分钟，specs 4.1.2 F）。
// 样本期收录 18 题（9 型各 2 题），长短语陈述句为该量表经典风格。
func risoHudsonTemplate() ScaleTemplate {
	items := []ScaleItem{
		{Statement: "我对自己有很高的标准，事情没做对会让我很难安心", DimensionCode: "ENNE_TYPE_1_REFORMER",
			FocusPoint: "计分键：本题计入完美型（1 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我常常在心里批评自己哪里还可以做得更好", DimensionCode: "ENNE_TYPE_1_REFORMER",
			FocusPoint: "计分键：本题计入完美型（1 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我习惯主动去关心别人需要什么，并且乐于伸出援手", DimensionCode: "ENNE_TYPE_2_HELPER",
			FocusPoint: "计分键：本题计入助人型（2 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "让别人满意会让我觉得自己是有价值、被需要的", DimensionCode: "ENNE_TYPE_2_HELPER",
			FocusPoint: "计分键：本题计入助人型（2 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我喜欢设定明确的目标，并为自己取得的成就感到自豪", DimensionCode: "ENNE_TYPE_3_ACHIEVER",
			FocusPoint: "计分键：本题计入成就型（3 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我非常在意自己在别人眼中是不是成功的形象", DimensionCode: "ENNE_TYPE_3_ACHIEVER",
			FocusPoint: "计分键：本题计入成就型（3 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我的感受很深，常常觉得自己和周围的人不太一样", DimensionCode: "ENNE_TYPE_4_INDIVIDUALIST",
			FocusPoint: "计分键：本题计入自我型（4 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我容易被忧伤和美好的事物打动，渴望找到真实的自己", DimensionCode: "ENNE_TYPE_4_INDIVIDUALIST",
			FocusPoint: "计分键：本题计入自我型（4 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "遇到问题时，我更愿意先自己安静地想清楚再行动", DimensionCode: "ENNE_TYPE_5_INVESTIGATOR",
			FocusPoint: "计分键：本题计入智慧型（5 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我喜欢深入了解事物的原理，私人空间和时间对我很重要", DimensionCode: "ENNE_TYPE_5_INVESTIGATOR",
			FocusPoint: "计分键：本题计入智慧型（5 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我做事之前常常先想好可能出问题的地方，心里才踏实", DimensionCode: "ENNE_TYPE_6_LOYALIST",
			FocusPoint: "计分键：本题计入忠诚型（6 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我对信任的人非常忠诚，但也容易怀疑和担心", DimensionCode: "ENNE_TYPE_6_LOYALIST",
			FocusPoint: "计分键：本题计入忠诚型（6 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我精力充沛，总有很多新的想法和计划想去尝试", DimensionCode: "ENNE_TYPE_7_ENTHUSIAST",
			FocusPoint: "计分键：本题计入活跃型（7 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我不喜欢被限制，快乐和多样化的体验对我很重要", DimensionCode: "ENNE_TYPE_7_ENTHUSIAST",
			FocusPoint: "计分键：本题计入活跃型（7 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我习惯直接面对冲突，敢于说出自己真实的想法", DimensionCode: "ENNE_TYPE_8_CHALLENGER",
			FocusPoint: "计分键：本题计入领袖型（8 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我喜欢掌控局面，保护自己在意的人不受欺负", DimensionCode: "ENNE_TYPE_8_CHALLENGER",
			FocusPoint: "计分键：本题计入领袖型（8 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "我容易看到各方的道理，希望大家都和睦相处", DimensionCode: "ENNE_TYPE_9_PEACEMAKER",
			FocusPoint: "计分键：本题计入调停型（9 型）倾向聚合，认同度越高该型别倾向得分越高。"},
		{Statement: "为了维持平静，我常常把自己的想法放在一边", DimensionCode: "ENNE_TYPE_9_PEACEMAKER",
			FocusPoint: "计分键：本题计入调停型（9 型）倾向聚合，认同度越高该型别倾向得分越高。"},
	}
	for i := range items {
		items[i].Requirement = likertRequirement
	}
	return ScaleTemplate{
		ScaleKey:         "RISO_HUDSON",
		Name:             "Riso-Hudson 标准量表",
		QuestionCount:    len(items),
		EstimatedMinutes: 25,
		Description:      "经典九型人格标准量表，题项覆盖全面，测型结果稳定，适合追求细致区分的场景。",
		Dimensions:       enneDimensions,
		Items:            items,
	}
}
