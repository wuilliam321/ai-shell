package main

// ClaudeProvider implements Provider for Claude models
type ClaudeProvider struct {
	model string
}

// NewClaudeProvider creates a new Claude provider
func NewClaudeProvider(model string) *ClaudeProvider {
	return &ClaudeProvider{model: model}
}

func (p *ClaudeProvider) Name() string  { return "claude" }
func (p *ClaudeProvider) Model() string { return p.model }

func (p *ClaudeProvider) SendRequest(systemPrompt, userContent string, tools []Tool) (string, error) {
	req := hubRequest{
		Model:           p.model,
		Input:           userContent,
		Instructions:    systemPrompt,
		MaxOutputTokens: 1200,
		Stream:          false,
	}
	if len(tools) > 0 {
		req.Tools = tools
		req.ToolChoice = "auto"
	}
	return sendHubRequest(req)
}
