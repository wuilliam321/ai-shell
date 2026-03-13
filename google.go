package main

// GoogleProvider implements Provider for Google Gemini models
type GoogleProvider struct {
	model string
}

// NewGoogleProvider creates a new Google provider
func NewGoogleProvider(model string) *GoogleProvider {
	return &GoogleProvider{model: model}
}

func (p *GoogleProvider) Name() string  { return "google" }
func (p *GoogleProvider) Model() string { return p.model }

func (p *GoogleProvider) SendRequest(systemPrompt, userContent string, tools []Tool) (string, error) {
	req := hubRequest{
		Model:           p.model,
		Input:           userContent,
		Instructions:    systemPrompt,
		Temperature:     0.1,
		MaxOutputTokens: 1200,
		Stream:          false,
		Reasoning:       &hubReasoning{Effort: "high"},
	}
	if len(tools) > 0 {
		req.Tools = tools
		req.ToolChoice = "auto"
	}
	return sendHubRequest(req)
}
