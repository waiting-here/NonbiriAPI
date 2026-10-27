package managementlog

import "strings"

var domainLabels = map[string]string{
	"site-config": "站点配置", "config": "站点配置", "users": "用户", "donations": "捐赠",
	"donation-keys": "捐赠密钥", "donation-sources": "捐赠发现来源", "blacklist": "黑名单",
	"mainstream-channels": "主流渠道", "charity-models": "公益模型", "charity-binding-presets": "绑定预设",
	"charity-price-templates": "价格模板", "gateway-model-capabilities": "协议模型", "reports": "凭据举报",
	"announcements": "公告", "maintenance": "维护状态", "alerts": "告警", "abuse-audit": "风险审计",
	"activities": "活动", "activity": "活动", "pools": "奖池", "limited-activities": "限时活动",
	"inactivity-policy": "低活跃策略", "rewards": "奖励活动", "gacha": "抽卡与商店", "games": "游戏",
	"legal-holds": "法律保全", "ai": "AI 工作区", "management-operations": "管理操作记录",
	"model-bindings": "公益模型绑定", "donation-key-failure-policy": "捐赠密钥失败策略", "branding": "站点外观", "egress": "出站配置", "endpoint-backoff-policy": "端点退避策略",
	"endpoint-backoff-policies": "端点退避策略",
}
var operationLabels = map[string]string{
	"create": "新增", "update": "更新", "delete": "删除", "review": "审核", "approve": "批准", "reject": "驳回",
	"publish": "发布", "withdraw": "撤回", "reset": "重置", "failure-streak-reset": "重置失败计数",
	"cancel": "取消", "resume": "恢复", "clear": "清空", "cleanup": "清理", "resolve": "处理",
	"refresh": "刷新", "retry": "重试", "reopen": "重新开放", "close": "关闭", "import": "导入",
	"decision": "决定计划", "ban": "封禁", "unban": "解封", "revoke": "撤销", "release": "解除",
	"enable": "启用", "disable": "停用", "processed": "标记已处理", "apply-preset": "应用预设",
	"bindings": "配置绑定", "start": "开始", "submit": "提交", "abandon": "终止",
}

func routeAction(method string, parts []string) (string, string) {
	verb := "update"
	if method == "POST" {
		verb = "create"
	}
	if method == "DELETE" {
		verb = "delete"
	}
	components := []string{}
	for _, part := range parts[1:] {
		if part != "" && !strings.HasPrefix(part, "{") {
			components = append(components, part)
		}
	}
	if len(components) > 0 && method == "POST" {
		last := components[len(components)-1]
		if _, ok := operationLabels[last]; ok {
			verb = last
			components = components[:len(components)-1]
		}
	}
	components = append(components, verb)
	domain := domainLabels[parts[0]]
	if len(parts) > 1 {
		switch parts[0] + "/" + parts[1] {
		case "limited-activities/fat-fish":
			domain = "大肥鱼"
		case "limited-activities/picture-book":
			domain = "绘本"
		case "games/duel":
			domain = "游戏 AI"
		}
	}
	if domain == "" {
		domain = parts[0]
	}
	return strings.Join(components, "."), operationLabels[verb] + domain
}
