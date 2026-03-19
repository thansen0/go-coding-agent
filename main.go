package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
    "askthomas/tools"
)

const systemPrompt = `
You are a coding agent.

You can:
- read files
- write files
- run shell commands

When you want to use a tool, respond ONLY with valid JSON in this exact shape:
{"tool":"tool_name","args":{"key":"value"}}

Available tools:
- read_file: {"path":"..."}
- write_file: {"path":"...","content":"..."}
- run_command: {"cmd":"..."}

Rules:
- Do not wrap the JSON in markdown fences.
- If the task is complete, respond normally with a short final answer.
- Prefer small, incremental actions.
`

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type ToolCall struct {
	Tool string            `json:"tool"`
	Args map[string]string `json:"args"`
}

func callXAI(messages []Message) (string, error) {
	apiKey := os.Getenv("XAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XAI_API_KEY is not set")
	}

	reqBody := ChatRequest{
		// Model:       "grok-4.20-beta-latest-non-reasoning",
		Model:       "grok-code-fast-1",
		Messages:    messages,
		Temperature: 0,
		Stream:      false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var parsed ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("xAI API error: %s", parsed.Error.Message)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("xAI returned status %d", resp.StatusCode)
	}

	if len(parsed.Choices) == 0 {
		return "", errors.New("no choices returned")
	}

	return parsed.Choices[0].Message.Content, nil
}

func executeTool(raw string) (string, error) {
	var tc ToolCall
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		return "", err
	}

	switch tc.Tool {
	case "read_file":
		path := tc.Args["path"]
		if path == "" {
			return "", errors.New(`missing args.path for "read_file"`)
		}
		return tools.ReadFile(path)

	case "write_file":
		path := tc.Args["path"]
		content := tc.Args["content"]
		if path == "" {
			return "", errors.New(`missing args.path for "write_file"`)
		}
		return tools.WriteFile(path, content)

	case "run_command":
		cmd := tc.Args["cmd"]
		if cmd == "" {
			return "", errors.New(`missing args.cmd for "run_command"`)
		}
		return tools.RunCommand(cmd)

	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Tool)
	}
}

func looksLikeToolCall(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(s, `"tool"`)
}

func runAgent(userInput string) (string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userInput},
	}

	for i := 0; i < 10; i++ {
		reply, err := callXAI(messages)
		if err != nil {
			return "", err
		}

		fmt.Printf("\n=== assistant turn %d ===\n%s\n", i+1, reply)

		if !looksLikeToolCall(reply) {
			return reply, nil
		}

		result, err := executeTool(reply)
		if err != nil {
			result = "Tool error: " + err.Error()
		}

		messages = append(messages, Message{
			Role:    "assistant",
			Content: reply,
		})
		messages = append(messages, Message{
			Role: "user",
			Content: "Tool result:\n" + result +
				"\n\nContinue. If the task is done, give the final answer. Otherwise call another tool.",
		})
	}

	return "max iterations reached", nil
}

func main() {
	task := "Create a hello.py file that prints hello world, then run it."
	if len(os.Args) > 1 {
		task = strings.Join(os.Args[1:], " ")
	}

	final, err := runAgent(task)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	fmt.Println("\n=== final ===")
	fmt.Println(final)
}
