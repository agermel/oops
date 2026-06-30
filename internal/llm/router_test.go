package llm

import (
	"testing"
)

func TestClassifyQuestion_Diagnose(t *testing.T) {
	tests := []string{
		"MySQL 报错了",
		"Redis 故障排查",
		"容器异常怎么办",
		"Pod crash了",
		"连接超时怎么处理",
		"为什么日志报 error",
		"连不上数据库了",
		"帮忙诊断一下",
		"服务不工作了",
		"容器起不来",
	}
	for _, q := range tests {
		if got := classifyQuestion(q); got != AgentDiagnose {
			t.Errorf("classifyQuestion(%q) = %s, want %s", q, got, AgentDiagnose)
		}
	}
}

func TestClassifyQuestion_Inspect(t *testing.T) {
	tests := []string{
		"巡检一下",
		"检查一下所有容器状态",
		"MySQL 怎么样",
		"概况",
		"概览",
		"汇总报告",
		"列出所有容器",
		"有哪些机器",
		"overview",
	}
	for _, q := range tests {
		if got := classifyQuestion(q); got != AgentInspect {
			t.Errorf("classifyQuestion(%q) = %s, want %s", q, got, AgentInspect)
		}
	}
}

func TestClassifyQuestion_Default(t *testing.T) {
	tests := []string{
		"你好",
		"帮我看看日志",
		"现在几点",
	}
	for _, q := range tests {
		if got := classifyQuestion(q); got != AgentDefault {
			t.Errorf("classifyQuestion(%q) = %s, want %s", q, got, AgentDefault)
		}
	}
}

func TestClassifyQuestion_DiagnosePriority(t *testing.T) {
	// 同时包含诊断和巡检关键词时，诊断优先。
	q := "巡检一下，看看有没有异常"
	if got := classifyQuestion(q); got != AgentDiagnose {
		t.Errorf("classifyQuestion(%q) = %s, want %s (diagnose should take priority)", q, got, AgentDiagnose)
	}
}

func TestRouter_Route_NoPromptStore(t *testing.T) {
	r := NewRouter(nil)
	cfg := r.Route("MySQL 报错了")
	if cfg.Type != AgentDiagnose {
		t.Errorf("cfg.Type = %s, want %s", cfg.Type, AgentDiagnose)
	}
	if cfg.MaxStep != 10 {
		t.Errorf("cfg.MaxStep = %d, want 10", cfg.MaxStep)
	}
	// Prompt 应回退到空。
	if cfg.Prompt != "" {
		t.Logf("cfg.Prompt is non-empty without prompt store: %q", cfg.Prompt)
	}
}

func TestRouter_Route_DefaultMaxStep(t *testing.T) {
	r := NewRouter(nil)
	cfg := r.Route("你好")
	if cfg.MaxStep != 15 {
		t.Errorf("cfg.MaxStep = %d, want 15", cfg.MaxStep)
	}
}

func TestRouter_Route_InspectMaxStep(t *testing.T) {
	r := NewRouter(nil)
	cfg := r.Route("巡检一下")
	if cfg.MaxStep != 5 {
		t.Errorf("cfg.MaxStep = %d, want 5", cfg.MaxStep)
	}
}
