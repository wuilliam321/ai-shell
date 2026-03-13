# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

```bash
go build -o ai-shell
```

No external dependencies — standard library only. Requires Go 1.22+.

## Architecture

CLI tool that translates natural language into shell commands using AI. All code is in a single `main` package across four files.

### Provider abstraction

`main.go` defines the `Provider` interface:

```go
type Provider interface {
    Name() string
    Model() string
    SendRequest(systemPrompt, userContent string, tools []Tool) (string, error)
}
```

Each provider file implements this interface with its own request/response types:

- **`claude.go`** — Claude models. Content blocks, `parameters` field, top-level `content[]`/`tool_calls[]` response.
- **`openai.go`** — OpenAI models (gpt-*, o1-*, o3-*). Uses `/openai/v3/chat/completions` endpoint; text-only responses (no tool calling — proxy strips tool call data on this endpoint).
- **`google.go`** — Gemini models. `system_instruction` field, `parameters` with `temperature`/`reasoning_effort`.

Provider is auto-selected from model name prefix in `providerForModel()`. Claude and Google use the universal proxy endpoint (`/genai/v1/chat/completions`); OpenAI uses the v3 endpoint (`/openai/v3/chat/completions`).

### Three-step command generation flow

1. **Identify command** — AI picks a command name from the user's actual PATH executables (sent as context).
2. **Get documentation** — Tries `--help`, `-h`, then `man` with a 5s timeout. Truncated at 4000 chars.
3. **Generate optimal command** — AI produces the full command using docs + user request.

Claude and Google use OpenAI-style tool calling for structured output (`identify_command` and `generate_command` tool definitions). OpenAI uses text-only responses parsed directly (proxy v3 endpoint strips tool call data).

### Shared infrastructure in main.go

- `doHTTPRequest(url, jsonData)` — shared HTTP client, accepts endpoint URL per provider
- `extractToolCallField()` — parses tool call JSON arguments (used by Claude/Google)
- Tool definitions (`identifyCommandTool`, `generateCommandTool`) — used by Claude/Google providers
- Shell history append (zsh/bash), JSONL history logging, command sanitization

### Configuration

- Default model persisted to `~/.go-ai-shell/config.json` via `--default` flag
- History logged to `~/.go-ai-shell/history.jsonl`
- Resolution order: `--model` flag > saved default > hardcoded `claude-opus-4-6`
