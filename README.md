## go-ai-shell

Turn natural language into shell commands. The tool identifies the appropriate CLI, gathers docs, and generates the most suitable command. It prints the command with the model used, optionally runs it, and can log to a JSONL history file. Minimal output by default; use `-v` for verbose steps.

### Requirements
- Go 1.20+
- Environment variable `OPENAI_API_KEY` set (OpenAI-compatible endpoint)

### Installation

```shell
git clone https://github.com/wuilliam321/ai-shell.git
cd ai-shell
go build -o ai
```

### Configuration
- `OPENAI_API_KEY`: required.
- History file (JSONL): defaults to `~/.go-ai-shell/history.jsonl`.

### Usage

Basic:
```shell
./ai "find all images in png format"
```

Verbose (show detection steps):
```shell
./ai -v "list processes using port 5432"
```

Custom history path:
```shell
./ai --history /tmp/ai-shell-history.jsonl "archive logs older than 7 days"
```

Append to shell history (bash/zsh on macOS/Linux, enabled by default):
```shell
./ai --append-shell-history "list all docker images"
```
Disable shell history append:
```shell
./ai --append-shell-history=false "list all docker images"
```

Example output (non-verbose):
```text
docker images --format '{{.Repository}}:{{.Tag}}'
(model: gpt-5)

Run this? ([Y]es/[n]o/[r]etry) [Y]:
```

### Flags
- `-v`: verbose output (prints OS, lookup steps, doc stats, retries)
- `--history <path>`: custom JSONL history file path (default `~/.go-ai-shell/history.jsonl`)
- `--append-shell-history`: append accepted command to shell history (zsh or bash). Default: `true`.

### History Log
Each run appends one JSON line to the history file. Example entry:
```json
{
  "timestamp": "2025-08-08T12:34:56Z",
  "model": "gpt-5",
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
- On macOS/Linux only. Unsupported shells are ignored for history appends.
