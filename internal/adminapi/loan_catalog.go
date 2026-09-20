package adminapi

func addLoanCatalogMetadata() {
	for _, item := range []struct{ key, zh, en, descriptionZh, descriptionEn string }{
		{KeyActivityLoanEnabled, "赛博网贷开关", "Cyber loan switch", "控制是否允许新的贷款；关闭后历史贷款仍可查询，余额不变。", "Controls new loans. Closing preserves loan history and current balances."},
		{KeyActivityLoanTiers, "贷款档位", "Loan tiers", "三个严格递增的整数积分档位，在活动设置中统一调整。", "Three strictly increasing whole-credit tiers, edited together in activity settings."},
		{KeyActivityLoanA, "游戏积分到账系数", "Game-credit disbursement factor", "借款档位乘以该系数为实际到账游戏积分；剩余部分为手续费。", "The selected principal multiplied by this factor is paid as game credits; the remainder is the fee."},
		{KeyActivityLoanB, "通用积分本息系数", "General-credit repayment factor", "借款档位乘以该系数为扣除的通用积分本息，可产生负余额。", "The selected principal multiplied by this factor is deducted as general-credit principal and interest and may create a negative balance."},
	} {
		catalogMetadataByKey[item.key] = catalogMetadata{"activities", catalogText(item.zh, item.en), catalogText(item.descriptionZh, item.descriptionEn), unitNone, []string{KeyActivitiesEnabled}}
	}
}
