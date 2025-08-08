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
	"strings"
	"time"
)

const (
	proxyURL = ""

	// System prompts
	systemPromptStep1 = "You are an expert in command line utilities. Your task is to identify the most appropriate command line tool for a user request. Respond with ONLY the command name, no arguments or flags, just the base command. For example, if asked how to list files, respond with 'ls' only, not 'ls -la'. Do not provide explanations."
	systemPromptStep2 = "You are an expert in command line utilities. Based on the command documentation and the user's request, provide the most appropriate command with all necessary options and arguments. Format your response as a single line command without explanations."

	// Prefixes
	prefixStep1 = "Identify the most appropriate command line tool for this task (command name only): "
	prefixStep2 = "Using the following command documentation, provide the best way to use this command for this task: "

	// API settings
	model       = "gpt-5"
	temperature = 1
)

// OpenAI API request structure
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAI API response structure
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
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

func main() {
	// Flags
	var verbose bool
	var historyPathFlag string
	var appendShellHist bool
	flag.BoolVar(&verbose, "v", false, "enable verbose output")
	flag.StringVar(&historyPathFlag, "history", "", "path to history log file (default: ~/.go-ai-shell/history.jsonl)")
	flag.BoolVar(&appendShellHist, "append-shell-history", true, "append accepted command to shell history (bash/zsh)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Println("Usage: go-ai-shell [-v] [--history path] <request>")
		return
	}

	// Detect OS
	currentOS := detectOS()
	if verbose {
		fmt.Printf("OS: %s\n", currentOS)
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
		Model:     model,
		OS:        currentOS,
		Query:     args,
	}
	defer func() {
		if err := appendHistory(historyPath, history); err != nil && verbose {
			fmt.Printf("Warning: failed to write history: %v\n", err)
		}
	}()

	// Step 1: Identify the appropriate command
	if verbose {
		fmt.Println("Step 1: Identifying appropriate command...")
	}
	commandName, err := identifyCommand(args)
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
	cmdInfo, err := findCommandInfo(commandName, currentOS)
	if err != nil {
		if verbose {
			fmt.Printf("Warning: Could not retrieve full command info: %s\n", err)
		}
		// Continue with command name only
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
	command, err := getOptimalCommand(args, cmdInfo)
	if err != nil {
		history.Error = fmt.Sprintf("optimize: %v", err)
		fmt.Println("Error:", err)
		return
	}
	history.GeneratedCommand = command

	// Display and run command
	fmt.Printf("%s\n", command)
	fmt.Printf("(model: %s)\n\n", model)

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
			command, err = getOptimalCommand(args, cmdInfo)
			if err != nil {
				history.Error = fmt.Sprintf("retry: %v", err)
				fmt.Println("Error:", err)
				return
			}
			history.GeneratedCommand = command
			if verbose {
				fmt.Printf("New command: %s\n", command)
			} else {
				fmt.Printf("%s\n(model: %s)\n", command, model)
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

// sendRequest sends a request to the OpenAI API with the given system prompt and user content
func sendRequest(systemPrompt, userContent string) (string, error) {
	// Check for API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	// Create request body with system prompt and user query
	reqBody := Request{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
		Temperature: temperature,
		Stream:      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	// Create request with appropriate headers
	req, err := http.NewRequest("POST", proxyURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	// Set required headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Send request with a timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Handle non-200 responses
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error: %s, Details: %s", resp.Status, string(bodyBytes))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var responseData Response
	if err := json.Unmarshal(body, &responseData); err != nil {
		return "", fmt.Errorf("error unmarshalling response: %w, Response body: %s", err, string(body))
	}

	// Check if we have valid choices
	if len(responseData.Choices) == 0 {
		return "", fmt.Errorf("no response choices received")
	}

	// Extract the content from the response
	return strings.TrimSpace(responseData.Choices[0].Message.Content), nil
}

// identifyCommand identifies the appropriate command for the given user query
func identifyCommand(query string) (string, error) {
	return sendRequest(systemPromptStep1, prefixStep1+query)
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
func findCommandInfo(commandName string, osType OSType) (*CommandInfo, error) {
	// Find command path via PATH
	cmdPath, err := exec.LookPath(commandName)
	if err != nil || cmdPath == "" {
		return nil, fmt.Errorf("command '%s' not found in PATH", commandName)
	}

	// Get command documentation
	doc, err := getCommandDocumentation(commandName, osType)
	if err != nil {
		return &CommandInfo{Name: commandName, Path: cmdPath}, fmt.Errorf("command found but couldn't get documentation: %w", err)
	}

	return &CommandInfo{
		Name:          commandName,
		Path:          cmdPath,
		Documentation: doc,
	}, nil
}

// fileExists checks if a file exists at the given path
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getCommandDocumentation gets the documentation for a command
func getCommandDocumentation(commandName string, osType OSType) (string, error) {
	// Prefer --help/-h for speed; then fall back to man. Apply timeouts and disable pagers.
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

// getOptimalCommand gets the optimal command usage with documentation context
func getOptimalCommand(query string, cmdInfo *CommandInfo) (string, error) {
	// Prepare prompt for second query
	prompt := prefixStep2 + "\n\n"

	// Add command info to the prompt
	prompt += fmt.Sprintf("Command: %s\n", cmdInfo.Name)
	if cmdInfo.Path != "" {
		prompt += fmt.Sprintf("Path: %s\n", cmdInfo.Path)
	}

	// Add documentation if available
	if cmdInfo.Documentation != "" {
		prompt += fmt.Sprintf("\nDocumentation:\n%s\n\n", cmdInfo.Documentation)
	}

	// Add original query
	prompt += fmt.Sprintf("\nUser request: %s\n", query)

	return sendRequest(systemPromptStep2, prompt)
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
	// Determine shell from environment; default to zsh on macOS, bash on Linux
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
		// Unsupported shell; no-op
		return nil
	}
}

func appendToZshHistory(command string) error {
	// zsh history file with EXTENDED_HISTORY: ': <epoch>:0;<command>'
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return err
	}
	histFile := filepath.Join(home, ".zsh_history")
	// Prefer HISTFILE if set
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
	// bash history is plain lines in ~/.bash_history or $HISTFILE
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
