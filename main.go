package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	serverURL     = "http://localhost:11434/api/generate"
	system_prompt = "You are a Mac OS iterm shell"
	prefix        = "do not explain, do not use markdown, only use stdlib tools, " +
		"unless otherwise is requested. Just return one single command for this request: "
	model       = "llama3.1"
	temperature = 0.2
)

type Request struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	System  string                 `json:"system"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options"`
}

type Response struct {
	Response string `json:"response"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("you must provide a command as an argument")
		return
	}

	args := strings.Join(os.Args[1:], " ")

	for {
		command, err := generate(args)
		if err != nil {
			fmt.Println("Error:", err)
			return
		}

		fmt.Println(command)
		var answer string
		fmt.Print("run it? ([Y]es/[n]o/[r]etry) [Y]: ")
		fmt.Scanln(&answer)

		switch strings.ToUpper(answer) {
		case "Y", "S", "":
			if err := run(command); err != nil {
				fmt.Println(err)
			}
			return
		case "R":
			continue
		case "N":
			return
		default:
			fmt.Println("Invalid option, only [Y]es/[n]o/[r]etry are allowed.")
		}
	}
}

func generate(prompt string) (string, error) {
	reqBody := Request{
		Model:  model,
		Prompt: prefix + prompt,
		System: system_prompt,
		Stream: false,
		Options: map[string]interface{}{
			"temperature": temperature,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(serverURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server error: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var responseData Response
	if err := json.Unmarshal(body, &responseData); err != nil {
		return "", err
	}

	return responseData.Response, nil
}

func run(command string) error {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}
