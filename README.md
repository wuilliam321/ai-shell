## go-ai-shell

Turn natural language into shell commands. The tool identifies the appropriate CLI, gathers docs, and generates the most suitable command. It prints the command with the model used, optionally runs it, and can log to a JSONL history file. Minimal output by default; use `-v` for verbose steps.

Supports multiple AI providers: **Claude** (default), **OpenAI** (gpt-\*, o1-\*, o3-\*), and **Google Gemini**. The provider is auto-selected from the model name.

### Requirements
- Go 1.22+
- macOS or Linux

### Installation

```shell
git clone https://github.com/wuilliam321/ai-shell.git
cd ai-shell
go build -o ai-shell
```

### Configuration

- **Default model**: persisted to `~/.go-ai-shell/config.json` via `--default`.
- **History file** (JSONL): defaults to `~/.go-ai-shell/history.jsonl`.
- **Model resolution order**: `--model` flag > saved default > `claude-opus-4-6`.

Set a persistent default model:
```shell
./ai-shell --default gemini-2.5-pro
```

### Usage

Basic:
```shell
./ai-shell "find all images in png format"
```

Verbose (show detection steps):
```shell
./ai-shell -v "list processes using port 5432"
```

Use a specific model for one run:
```shell
./ai-shell --model gpt-5.2-chat-latest "archive logs older than 7 days"
```

Custom history path:
```shell
./ai-shell --history /tmp/ai-shell-history.jsonl "archive logs older than 7 days"
```

Append to shell history (bash/zsh on macOS/Linux, enabled by default):
```shell
./ai-shell --append-shell-history "list all docker images"
```
Disable shell history append:
```shell
./ai-shell --append-shell-history=false "list all docker images"
```

Example output (non-verbose):
```text
docker images --format '{{.Repository}}:{{.Tag}}'
(model: claude-opus-4-6)

Run this? ([Y]es/[n]o/[r]etry) [Y]:
```

### Flags
- `-v`: verbose output (prints provider, model, OS, lookup steps, doc stats, retries)
- `--model <name>`: model to use for this run (overrides saved default)
- `--default <name>`: set and persist the default model, then exit
- `--history <path>`: custom JSONL history file path (default `~/.go-ai-shell/history.jsonl`)
- `--append-shell-history`: append accepted command to shell history (zsh or bash). Default: `true`.

### Supported Models

| Provider | Model prefixes | Examples |
|----------|---------------|----------|
| Claude | `claude*` | `claude-opus-4-6`, `claude-sonnet-4-5-20250929` |
| OpenAI | `gpt*`, `o1*`, `o3*` | `gpt-5.2-chat-latest`, `o3-mini` |
| Google | `gemini*` | `gemini-3-pro-preview` |

See the [full list of available models](https://furydocs.io/genai-docs/latest/guide/#/models/).

### History Log
Each run appends one JSON line to the history file. Example entry:
```json
{
  "timestamp": "2025-08-08T12:34:56Z",
  "model": "claude-opus-4-6",
  "os": "macos",
  "query": "list all docker images",
  "identified_command": "docker",
  "command_path": "/usr/local/bin/docker",
  "documentation_included": true,
  "generated_command": "docker images --format '{{.Repository}}:{{.Tag}}'",
  "action": "run",
  "exit_code": 0
}
```

### Notes
- The tool prompts to run the generated command. Choose Yes to execute, Retry to regenerate, or No to exit.
- When `--append-shell-history` is used, the command is appended after you accept run. zsh/bash may need a new session or history reload to show it.
- macOS/Linux only. Unsupported shells are ignored for history appends.
