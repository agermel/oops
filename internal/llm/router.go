package llm

import (
	"strings"
)

// AgentType 表示专业 Agent 的类型。
type AgentType string

const (
	// AgentDiagnose 故障诊断 Agent — 针对用户报告的故障进行系统排查。
	AgentDiagnose AgentType = "diagnose"
	// AgentInspect 日常巡检 Agent — 快速过一遍所有节点和服务状态。
	AgentInspect AgentType = "inspect"
	// AgentDefault 通用 Agent — 处理不属于上述两类的通用运维问题。
	AgentDefault AgentType = "default"
)

// AgentConfig 定义单个 Agent 类型的运行时配置。
type AgentConfig struct {
	Type    AgentType // Agent 类型
	Prompt  string    // 系统提示词内容
	MaxStep int       // 最大步数
}

// Router 根据用户问题选择合适的 Agent 配置。
type Router struct {
	promptStore *PromptStore
}

// NewRouter 创建 Agent 路由器。
func NewRouter(ps *PromptStore) *Router {
	return &Router{promptStore: ps}
}

// Route 根据用户问题返回 Agent 配置。
// 先从 PromptStore 获取场景 prompt，再根据 AgentType 设置 MaxStep。
func (r *Router) Route(question string) AgentConfig {
	promptContent, promptName := "", "system"
	if r.promptStore != nil {
		promptContent, promptName = r.promptStore.Resolve(question)
	}

	// 如果 Resolve 返回空，回退到 system prompt。
	if promptContent == "" && r.promptStore != nil {
		promptContent = r.promptStore.Get("system")
	}

	at := classifyQuestion(question)

	cfg := AgentConfig{
		Type:   at,
		Prompt: promptContent,
	}

	switch at {
	case AgentDiagnose:
		cfg.MaxStep = 10
		if promptName != "diagnose" && r.promptStore != nil {
			if d := r.promptStore.Get("diagnose"); d != "" {
				cfg.Prompt = d
			}
		}
	case AgentInspect:
		cfg.MaxStep = 5
		if promptName != "inspect" && r.promptStore != nil {
			if d := r.promptStore.Get("inspect"); d != "" {
				cfg.Prompt = d
			}
		}
	default:
		cfg.MaxStep = 15
	}

	return cfg
}

// classifyQuestion 根据问题关键词分类。
func classifyQuestion(question string) AgentType {
	q := strings.ToLower(question)

	// 故障诊断关键词。
	diagnoseKW := []string{"故障", "报错", "异常", "挂了", "不行了", "crash", "error", "fail",
		"出问题", "不工作", "起不来", "连不上", "超时", "timeout", "慢", "排查", "诊断", "怎么办", "为什么"}
	for _, kw := range diagnoseKW {
		if strings.Contains(q, kw) {
			return AgentDiagnose
		}
	}

	// 巡检关键词。
	inspectKW := []string{"巡检", "检查一下", "怎么样", "状态", "overview", "概况", "概览", "汇总", "报告", "列出", "有哪些"}
	for _, kw := range inspectKW {
		if strings.Contains(q, kw) {
			return AgentInspect
		}
	}

	return AgentDefault
}

// MaxStep 是 Agent 的默认最大步数（通用 Agent 使用）。
const MaxStep = 15

// MaxStepDiagnose 是诊断 Agent 的最大步数。
const MaxStepDiagnose = 10

// MaxStepInspect 是巡检 Agent 的最大步数。
const MaxStepInspect = 5
