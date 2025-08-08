package main

import (
	"bytes"
	"encoding/json"
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
	proxyURL = "https://api.openai.com/v1/chat/completions"

	// System prompts
	systemPromptStep1 = "You are an expert in command line utilities. Your task is to identify the most appropriate command line tool for a user request. Respond with ONLY the command name, no arguments or flags, just the base command. For example, if asked how to list files, respond with 'ls' only, not 'ls -la'. Do not provide explanations."
	systemPromptStep2 = "You are an expert in command line utilities. Based on the command documentation and the user's request, provide the most appropriate command with all necessary options and arguments. Format your response as a single line command without explanations."

	// Prefixes
	prefixStep1 = "Identify the most appropriate command line tool for this task (command name only): "
	prefixStep2 = "Using the following command documentation, provide the best way to use this command for this task: "

	// API settings
	model       = "gpt-4.1"
	temperature = 0.2
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
	if len(os.Args) < 2 {
		fmt.Println("You must provide a command as an argument")
		return
	}

	// Detect OS
	currentOS := detectOS()
	fmt.Printf("Detected OS: %s\n", currentOS)

	// Get user query
	args := strings.Join(os.Args[1:], " ")

	// Step 1: Identify the appropriate command
	fmt.Println("Step 1: Identifying appropriate command...")
	commandName, err := identifyCommand(args)
	if err != nil {
		fmt.Println("Error identifying command:", err)
		return
	}
	fmt.Printf("Identified command: %s\n", commandName)

	// Step 2: Find command in system directories and get documentation
	fmt.Printf("Step 2: Locating command and retrieving documentation...\n")
	cmdInfo, err := findCommandInfo(commandName, currentOS)
	if err != nil {
		fmt.Printf("Warning: Could not retrieve full command info: %s\n", err)
		// Continue with command name only
		cmdInfo = &CommandInfo{Name: commandName}
	} else {
		fmt.Printf("Found command at: %s\n", cmdInfo.Path)
		fmt.Printf("Documentation length: %d characters\n", len(cmdInfo.Documentation))
	}

	// Step 3: Get optimal usage with documentation context
	fmt.Println("Step 3: Determining optimal command usage...")
	command, err := getOptimalCommand(args, cmdInfo)
	if err != nil {
		fmt.Println("Error determining optimal command usage:", err)
		return
	}

	// Display and run command
	fmt.Printf("\nGenerated command:\n%s\n\n", command)

	// Ask user to run the command
	for {
		var answer string
		fmt.Print("Run this command? ([Y]es/[n]o/[r]etry) [Y]: ")
		fmt.Scanln(&answer)

		switch strings.ToUpper(answer) {
		case "Y", "S", "":
			if err := run(command); err != nil {
				fmt.Println("Error:", err)
			}
			return
		case "R":
			fmt.Println("Retrying command generation...")
			command, err = getOptimalCommand(args, cmdInfo)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
			fmt.Printf("New command: %s\n", command)
			continue
		case "N":
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
	// Define paths to search based on OS
	paths := []string{"/usr/bin", "/bin", "/usr/local/bin"}

	// Find command path
	cmdPath := ""
	for _, path := range paths {
		fullPath := filepath.Join(path, commandName)
		if fileExists(fullPath) {
			cmdPath = fullPath
			break
		}
	}

	if cmdPath == "" {
		return nil, fmt.Errorf("command '%s' not found in system paths", commandName)
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
	var cmd *exec.Cmd

	// Use man for documentation on macOS and Linux
	cmd = exec.Command("man", commandName)

	// Capture output
	out, err := cmd.Output()
	if err != nil {
		// Try --help as fallback
		cmd = exec.Command(commandName, "--help")
		out, err = cmd.Output()
		if err != nil {
			// Try -h as another fallback
			cmd = exec.Command(commandName, "-h")
			out, err = cmd.Output()
			if err != nil {
				return "", fmt.Errorf("could not get documentation for '%s'", commandName)
			}
		}
	}

	// Limit documentation size if it's too large
	doc := string(out)
	if len(doc) > 4000 {
		doc = doc[:4000] + "\n[Documentation truncated due to length]\n"
	}

	return doc, nil
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

func run(command string) error {
	fmt.Printf("Executing: %s\n\n", command)
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
