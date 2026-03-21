package main

import (
	"askthomas/tools"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const systemPromptTemplate = `
You are a coding agent working inside an existing codebase.

Current working directory: %s

You can:
- read files
- write files
- run a limited set of Go project commands
- run a limited set of safe shell commands inside the current workspace

When you want to use a tool, respond ONLY with valid JSON in this exact shape:
{"tool":"tool_name","args":{"key":"value"}}

Available tools:
- read_file: {"path":"..."}
- write_file: {"path":"...","content":"..."}
- run_go_action: {"action":"build|test|format|mod_tidy"}
- run_shell: {"command":"...","dir":"optional/subdir"}

Rules:
- Do not wrap the JSON in markdown fences.
- If the task is complete, respond normally with a short final answer.
- Prefer small, incremental actions.
- Start by inspecting the relevant files in the repository before making changes.
- Implement the user's requested changes in this codebase instead of creating unrelated demo files unless the user explicitly asks for one.
- Use run_go_action to verify changes when useful.
- run_shell is restricted to allowlisted programs, blocks obvious network tools, blocks sudo, and cannot run outside the current workspace.
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

	case "run_go_action":
		action := tc.Args["action"]
		if action == "" {
			return "", errors.New(`missing args.action for "run_go_action"`)
		}
		return tools.RunGoAction(action)

	case "run_shell":
		command := tc.Args["command"]
		if command == "" {
			return "", errors.New(`missing args.command for "run_shell"`)
		}
		return tools.RunShell(command, tc.Args["dir"])

	default:
		return "", fmt.Errorf("unknown tool: %s", tc.Tool)
	}
}

func looksLikeToolCall(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(s, `"tool"`)
}

func runAgent(userInput string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	messages := []Message{
		{Role: "system", Content: fmt.Sprintf(systemPromptTemplate, cwd)},
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

func readTask(args []string) (string, error) {
	if len(args) > 1 {
		return strings.Join(args[1:], " "), nil
	}

	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}

	if (info.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		task := strings.TrimSpace(string(data))
		if task != "" {
			return task, nil
		}
	}

	fmt.Print("Enter a task for the agent: ")
	reader := bufio.NewReader(os.Stdin)
	task, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	task = strings.TrimSpace(task)
	if task == "" {
		return "", errors.New("no task provided")
	}

	return task, nil
}

func main() {
	task, err := readTask(os.Args)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	if err := tools.SetWorkspaceRoot(cwd); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	final, err := runAgent(task)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	fmt.Println("\n=== final ===")
	fmt.Println(final)
}
