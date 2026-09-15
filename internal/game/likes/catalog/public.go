package catalog

import "slices"

// Snapshot is public, versioned content for explaining one mode's persisted
// rules. It contains neither operating prices nor unused legacy settings.
type Snapshot struct {
	RulesVersion  int    `json:"rules_version"`
	DesignVersion string `json:"design_version"`
	SchemaVersion int    `json:"schema_version"`
	ContentHash   string `json:"content_hash"`
	Config        Config `json:"config"`
}

func Public(mode string) (Snapshot, error) {
	c, hash, err := Load(mode)
	if err != nil {
		return Snapshot{}, err
	}
	delete(c.Parameters, "POINT_TICKET")
	delete(c.Parameters, "FOLLOWUP_CAP")
	c.ParamMeta = slices.DeleteFunc(c.ParamMeta, func(p ParamMeta) bool {
		return p.ID == "POINT_TICKET" || p.ID == "FOLLOWUP_CAP"
	})
	for i := range c.Buffs {
		if c.Buffs[i].Kind == "OVERLOAD" {
			c.Buffs[i].Description = "过载状态。不能净化或驱散，恢复期间自动跳过全部购物与技能，订阅时钟照常推进。仅双方过载参与行动限制终局判定。"
			c.Buffs[i].Target = "自身；共享电能不足时仅本轮报价大于零的席位过载"
		}
	}
	return Snapshot{RulesVersion: RulesVersion, DesignVersion: DesignVersion, SchemaVersion: SchemaVersion, ContentHash: hash, Config: c}, nil
}
