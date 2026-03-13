package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	defaultModel = "claude-opus-4-6"
	hubURL       = "https://genai.melioffice.com/hub/v1/responses"

	// System prompts
	systemPromptStep1 = "You are an expert in command line utilities. Your task is to identify the most appropriate command line tool for a user request from the list of available commands on the user's system. You must only suggest commands that appear in the provided list. Respond with ONLY the command name, no arguments or flags, just the base command. For example, if asked how to list files, respond with 'ls' only, not 'ls -la'. Do not provide explanations."
	systemPromptStep2 = "You are an expert in command line utilities. Based on the command documentation and the user's request, provide the most appropriate command with all necessary options and arguments. Format your response as a single line command without explanations."

	// Prefixes
	prefixStep1 = "Identify the most appropriate command line tool for this task (command name only): "
	prefixStep2 = "Using the following command documentation, provide the best way to use this command for this task: "
)

// Provider abstracts an AI model backend
type Provider interface {
	Name() string
	Model() string
	SendRequest(systemPrompt, userContent string, tools []Tool) (string, error)
}

// Tool represents a tool definition (flat format for /hub/v1/responses)
type Tool struct {
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  ToolParams `json:"parameters"`
}

// ToolParams defines the parameters schema for a tool
type ToolParams struct {
	Type       string                  `json:"type"`
	Properties map[string]ToolProperty `json:"properties"`
	Required   []string                `json:"required"`
}

// ToolProperty defines a single property in a tool's parameters
type ToolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// Hub request/response types shared across all providers

type hubRequest struct {
	Model           string        `json:"model"`
	Input           string        `json:"input"`
	Instructions    string        `json:"instructions,omitempty"`
	Stream          bool          `json:"stream"`
	Temperature     float64       `json:"temperature,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Tools           []Tool        `json:"tools,omitempty"`
	ToolChoice      string        `json:"tool_choice,omitempty"`
	Reasoning       *hubReasoning `json:"reasoning,omitempty"`
}

type hubReasoning struct {
	Effort string `json:"effort"`
}

type hubResponse struct {
	Output []hubOutputItem `json:"output"`
}

type hubOutputItem struct {
	Type      string       `json:"type"`
	Content   []hubContent `json:"content,omitempty"`
	Name      string       `json:"name,omitempty"`
	Arguments string       `json:"arguments,omitempty"`
}

type hubContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sendHubRequest marshals req, POSTs to hubURL, and returns the text or tool-call result
func sendHubRequest(req hubRequest) (string, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	body, err := doHTTPRequest(hubURL, jsonData)
	if err != nil {
		return "", err
	}
	var resp hubResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("error unmarshalling response: %w, body: %s", err, string(body))
	}
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			return extractToolCallField(item.Arguments, "command")
		}
		if item.Type == "message" && len(item.Content) > 0 {
			return strings.TrimSpace(item.Content[0].Text), nil
		}
	}
	return "", fmt.Errorf("no usable output in response")
}

// Tool definitions for structured output
var identifyCommandTool = Tool{
	Type:        "function",
	Name:        "identify_command",
	Description: "Return the identified command line tool name for the user's request",
	Parameters: ToolParams{
		Type: "object",
		Properties: map[string]ToolProperty{
			"command": {
				Type:        "string",
				Description: "The command name only, e.g. ls, grep, find, docker",
			},
		},
		Required: []string{"command"},
	},
}

var generateCommandTool = Tool{
	Type:        "function",
	Name:        "generate_command",
	Description: "Return the complete shell command with all necessary arguments, flags and options",
	Parameters: ToolParams{
		Type: "object",
		Properties: map[string]ToolProperty{
			"command": {
				Type:        "string",
				Description: "The complete command with all arguments and flags, ready to execute",
			},
		},
		Required: []string{"command"},
	},
}

// HistoryEntry captures a single run for history logging
type HistoryEntry struct {
	Timestamp             time.Time `json:"timestamp"`
	Model                 string    `json:"model"`
	OS                    OSType    `json:"os"`
	Query                 string    `json:"query"`
	IdentifiedCommand     string    `json:"identified_command,omitempty"`
	CommandPath           string    `json:"command_path,omitempty"`
	DocumentationIncluded bool      `json:"documentation_included"`
	GeneratedCommand      string    `json:"generated_command,omitempty"`
	Action                string    `json:"action"` // run | decline
	ExitCode              int       `json:"exit_code,omitempty"`
	Error                 string    `json:"error,omitempty"`
}

// OSType represents the detected operating system
type OSType string

const (
	OSLinux   OSType = "linux"
	OSMacOS   OSType = "macos"
	OSUnknown OSType = "unknown"
)

// CommandInfo stores information about a command
type CommandInfo struct {
	Name          string
	Path          string
	Documentation string
}

// providerForModel returns the appropriate provider based on model name
func providerForModel(model string) Provider {
	switch {
	case strings.HasPrefix(model, "claude"):
		return NewClaudeProvider(model)
	case strings.HasPrefix(model, "gpt"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"):
		return NewOpenAIProvider(model)
	case strings.HasPrefix(model, "gemini"):
		return NewGoogleProvider(model)
	default:
		return NewClaudeProvider(model)
	}
}

// doHTTPRequest sends a JSON request to the given URL and returns the raw response body
func doHTTPRequest(url string, jsonData []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s, Details: %s", resp.Status, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}

// extractToolCallField extracts a string field from tool call JSON arguments
func extractToolCallField(arguments, field string) (string, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("failed to parse tool call arguments: %w", err)
	}
	val, ok := args[field]
	if !ok {
		return "", fmt.Errorf("field %q not found in tool call arguments", field)
	}
	return strings.TrimSpace(val), nil
}

func main() {
	// Flags
	var verbose bool
	var historyPathFlag string
	var appendShellHist bool
	var modelFlag string
	var setDefault string
	flag.BoolVar(&verbose, "v", false, "enable verbose output")
	flag.StringVar(&historyPathFlag, "history", "", "path to history log file (default: ~/.go-ai-shell/history.jsonl)")
	flag.BoolVar(&appendShellHist, "append-shell-history", true, "append accepted command to shell history (bash/zsh)")
	flag.StringVar(&modelFlag, "model", "", "model to use for this run (overrides saved default)")
	flag.StringVar(&setDefault, "default", "", "set and persist the default model")
	flag.Parse()

	// Handle --default: save and exit
	if setDefault != "" {
		if err := saveDefaultModel(setDefault); err != nil {
			fmt.Printf("Error saving default model: %v\n", err)
			return
		}
		fmt.Printf("Default model set to: %s\n", setDefault)
		return
	}

	if flag.NArg() < 1 {
		fmt.Println("Usage: go-ai-shell [-v] [--model name] [--default name] [--history path] <request>")
		return
	}

	// Resolve model: --model flag > saved default > hardcoded default
	model := modelFlag
	if model == "" {
		model = loadDefaultModel()
	}
	if model == "" {
		model = defaultModel
	}

	// Select provider based on model
	provider := providerForModel(model)

	// Detect OS
	currentOS := detectOS()
	if verbose {
		fmt.Printf("Provider: %s, Model: %s, OS: %s\n", provider.Name(), provider.Model(), currentOS)
	}

	// Get user query
	args := strings.Join(flag.Args(), " ")

	// Prepare history entry and destination
	historyPath := historyPathFlag
	if historyPath == "" {
		historyPath = defaultHistoryPath()
	}
	history := &HistoryEntry{
		Timestamp: time.Now().UTC(),
		Model:     provider.Model(),
		OS:        currentOS,
		Query:     args,
	}
	defer func() {
		if err := appendHistory(historyPath, history); err != nil && verbose {
			fmt.Printf("Warning: failed to write history: %v\n", err)
		}
	}()

	// Discover available CLI tools from PATH
	availableCommands := listAvailableCommands()
	if verbose {
		fmt.Printf("Found %d available commands in PATH\n", len(availableCommands))
	}

	// Step 1: Identify the appropriate command
	if verbose {
		fmt.Println("Step 1: Identifying appropriate command...")
	}
	commandName, err := identifyCommand(provider, args, availableCommands)
	if err != nil {
		history.Error = fmt.Sprintf("identify: %v", err)
		fmt.Println("Error:", err)
		return
	}
	commandName, err = sanitizeCommandName(commandName)
	if err != nil {
		history.Error = fmt.Sprintf("sanitize: %v", err)
		fmt.Println("Error:", err)
		return
	}
	history.IdentifiedCommand = commandName
	if verbose {
		fmt.Printf("Identified command: %s\n", commandName)
	}

	// Step 2: Find command in system directories and get documentation
	if verbose {
		fmt.Printf("Step 2: Locating command and retrieving documentation...\n")
	}
	cmdInfo, err := findCommandInfo(commandName)
	if err != nil {
		if verbose {
			fmt.Printf("Warning: Could not retrieve full command info: %s\n", err)
		}
		cmdInfo = &CommandInfo{Name: commandName}
	} else {
		history.CommandPath = cmdInfo.Path
		if verbose {
			fmt.Printf("Found command at: %s\n", cmdInfo.Path)
			fmt.Printf("Documentation length: %d characters\n", len(cmdInfo.Documentation))
		}
	}
	history.DocumentationIncluded = cmdInfo.Documentation != ""

	// Step 3: Get optimal usage with documentation context
	if verbose {
		fmt.Println("Step 3: Determining optimal command usage...")
	}
	command, err := getOptimalCommand(provider, args, cmdInfo)
	if err != nil {
		history.Error = fmt.Sprintf("optimize: %v", err)
		fmt.Println("Error:", err)
		return
	}
	history.GeneratedCommand = command

	// Display and run command
	fmt.Printf("%s\n", command)
	fmt.Printf("(model: %s)\n\n", provider.Model())

	// Ask user to run the command
	for {
		var answer string
		fmt.Print("Run this? ([Y]es/[n]o/[r]etry) [Y]: ")
		fmt.Scanln(&answer)

		switch strings.ToUpper(answer) {
		case "Y", "S", "":
			exitCode, err := run(command)
			history.Action = "run"
			history.ExitCode = exitCode
			if err != nil {
				history.Error = err.Error()
				fmt.Println("Error:", err)
			}
			if appendShellHist {
				if err := appendToShellHistory(command, currentOS); err != nil && verbose {
					fmt.Printf("Warning: could not append to shell history: %v\n", err)
				}
			}
			return
		case "R":
			if verbose {
				fmt.Println("Retrying command generation...")
			}
			command, err = getOptimalCommand(provider, args, cmdInfo)
			if err != nil {
				history.Error = fmt.Sprintf("retry: %v", err)
				fmt.Println("Error:", err)
				return
			}
			history.GeneratedCommand = command
			if verbose {
				fmt.Printf("New command: %s\n", command)
			} else {
				fmt.Printf("%s\n(model: %s)\n", command, provider.Model())
			}
			continue
		case "N":
			history.Action = "decline"
			return
		default:
			fmt.Println("Invalid option, only [Y]es/[n]o/[r]etry are allowed.")
		}
	}
}

// identifyCommand identifies the appropriate command for the given user query using available system commands
func identifyCommand(p Provider, query string, availableCommands []string) (string, error) {
	content := prefixStep1 + query + "\n\nAvailable commands on this system:\n" + strings.Join(availableCommands, ", ")
	return p.SendRequest(systemPromptStep1, content, []Tool{identifyCommandTool})
}

// getOptimalCommand gets the optimal command usage with documentation context
func getOptimalCommand(p Provider, query string, cmdInfo *CommandInfo) (string, error) {
	prompt := prefixStep2 + "\n\n"
	prompt += fmt.Sprintf("Command: %s\n", cmdInfo.Name)
	if cmdInfo.Path != "" {
		prompt += fmt.Sprintf("Path: %s\n", cmdInfo.Path)
	}
	if cmdInfo.Documentation != "" {
		prompt += fmt.Sprintf("\nDocumentation:\n%s\n\n", cmdInfo.Documentation)
	}
	prompt += fmt.Sprintf("\nUser request: %s\n", query)
	return p.SendRequest(systemPromptStep2, prompt, []Tool{generateCommandTool})
}

// detectOS detects the current operating system
func detectOS() OSType {
	os := runtime.GOOS
	switch os {
	case "darwin":
		return OSMacOS
	case "linux":
		return OSLinux
	default:
		return OSUnknown
	}
}

// findCommandInfo locates the command in system directories and gets its documentation
func findCommandInfo(commandName string) (*CommandInfo, error) {
	cmdPath, err := exec.LookPath(commandName)
	if err != nil || cmdPath == "" {
		return nil, fmt.Errorf("command '%s' not found in PATH", commandName)
	}

	doc, err := getCommandDocumentation(commandName)
	if err != nil {
		return &CommandInfo{Name: commandName, Path: cmdPath}, fmt.Errorf("command found but couldn't get documentation: %w", err)
	}

	return &CommandInfo{
		Name:          commandName,
		Path:          cmdPath,
		Documentation: doc,
	}, nil
}

// getCommandDocumentation gets the documentation for a command
func getCommandDocumentation(commandName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tryCmd := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Env = append(os.Environ(), "MANPAGER=cat", "PAGER=cat")
		return c.Output()
	}

	if out, err := tryCmd(ctx, commandName, "--help"); err == nil && len(out) > 0 {
		return limitDocSize(string(out)), nil
	}
	if out, err := tryCmd(ctx, commandName, "-h"); err == nil && len(out) > 0 {
		return limitDocSize(string(out)), nil
	}
	if out, err := tryCmd(ctx, "man", commandName); err == nil && len(out) > 0 {
		return limitDocSize(string(out)), nil
	}
	return "", fmt.Errorf("could not get documentation for '%s'", commandName)
}

func run(command string) (int, error) {
	fmt.Printf("Executing: %s\n\n", command)
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	exitCode := -1
	if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
	}
	return exitCode, err
}

// sanitizeCommandName ensures we only use a single, safe command token
func sanitizeCommandName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command name from model")
	}
	s = parts[0]
	s = strings.Trim(s, "`\"'.,;:()[]{}")
	for _, r := range s {
		if !(r == '_' || r == '-' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return "", fmt.Errorf("invalid character in command name: %q", r)
		}
	}
	if s == "" {
		return "", fmt.Errorf("invalid empty command name")
	}
	return s, nil
}

// listAvailableCommands returns the names of all executables found in PATH
func listAvailableCommands() []string {
	seen := make(map[string]bool)
	var commands []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if seen[name] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0111 != 0 {
				seen[name] = true
				commands = append(commands, name)
			}
		}
	}
	sort.Strings(commands)
	return commands
}

type appConfig struct {
	Model string `json:"model"`
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.json"
	}
	return filepath.Join(home, ".go-ai-shell", "config.json")
}

func saveDefaultModel(model string) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(appConfig{Model: model})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadDefaultModel() string {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return ""
	}
	var cfg appConfig
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.Model
}

func defaultHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "history.jsonl"
	}
	return filepath.Join(home, ".go-ai-shell", "history.jsonl")
}

func appendHistory(path string, entry *HistoryEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func limitDocSize(doc string) string {
	if len(doc) > 4000 {
		return doc[:4000] + "\n[Documentation truncated due to length]\n"
	}
	return doc
}

// appendToShellHistory appends a command to the user's shell history if possible (macOS/Linux; bash/zsh)
func appendToShellHistory(command string, osType OSType) error {
	shell := os.Getenv("SHELL")
	var shellName string
	if shell != "" {
		shellName = filepath.Base(shell)
	}
	if shellName == "" {
		if osType == OSMacOS {
			shellName = "zsh"
		} else if osType == OSLinux {
			shellName = "bash"
		}
	}

	switch shellName {
	case "zsh":
		return appendToZshHistory(command)
	case "bash":
		return appendToBashHistory(command)
	default:
		return nil
	}
}

func appendToZshHistory(command string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return err
	}
	histFile := filepath.Join(home, ".zsh_history")
	if hf := os.Getenv("HISTFILE"); hf != "" {
		histFile = hf
	}
	line := fmt.Sprintf(": %d:0;%s\n", time.Now().Unix(), command)
	f, err := os.OpenFile(histFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return nil
}

func appendToBashHistory(command string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return err
	}
	histFile := filepath.Join(home, ".bash_history")
	if hf := os.Getenv("HISTFILE"); hf != "" {
		histFile = hf
	}
	f, err := os.OpenFile(histFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(command + "\n"); err != nil {
		return err
	}
	return nil
}
