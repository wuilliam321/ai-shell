package main

// OpenAIProvider implements Provider for OpenAI models (gpt-*, o1-*, o3-*)
type OpenAIProvider struct {
	model string
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(model string) *OpenAIProvider {
	return &OpenAIProvider{model: model}
}

func (p *OpenAIProvider) Name() string  { return "openai" }
func (p *OpenAIProvider) Model() string { return p.model }

func (p *OpenAIProvider) SendRequest(systemPrompt, userContent string, tools []Tool) (string, error) {
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
