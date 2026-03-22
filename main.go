package main

import (
	"askthomas/constants"
	"askthomas/tools"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var systemPromptTemplate = strings.Join([]string{
	"You are a coding agent working inside an existing codebase.",
	"",
	"Current working directory: %s",
	"",
	"Your job is to inspect the repository first, make minimal targeted edits to existing files, verify the change, and then finish with a short answer.",
	"",
	"Follow this workflow:",
	"- Understand the task before acting.",
	"- Search for relevant files and symbols before reading large files.",
	"- Read only the context needed to make a safe change.",
	"- Prefer small incremental edits that preserve existing behavior unless the task explicitly requires otherwise.",
	"- After edits, verify with the smallest useful step first, then prefer `go build ./...` and `go test ./...` when Go verification is needed.",
	"- Keep global constants in the `constants/` package when introducing or relocating shared configuration.",
	"",
	"You can:",
	"- inspect repository structure and search before editing",
	"- read full files or line ranges",
	"- apply minimal snippet patches to existing files",
	"- create new files only when truly needed",
	"- run a limited set of safe verification commands inside the current workspace",
	"",
	"When you act, respond ONLY with valid JSON in one of these shapes:",
	"{\"type\":\"inspect\",\"tool\":\"tool_name\",\"args\":{\"key\":\"value\"},\"summary\":\"why this step helps\"}",
	"{\"type\":\"edit\",\"tool\":\"tool_name\",\"args\":{\"key\":\"value\"},\"summary\":\"what will change\"}",
	"{\"type\":\"verify\",\"tool\":\"tool_name\",\"args\":{\"key\":\"value\"},\"summary\":\"what this validates\"}",
	"{\"type\":\"finish\",\"message\":\"short final answer\"}",
	"",
	"Schema rules:",
	"- \"type\" must be exactly one of: \"inspect\", \"edit\", \"verify\", \"finish\"",
	"- For non-finish actions, \"tool\" must be exactly one of the available tool names below",
	"- Do not put a tool name in \"type\"",
	"",
	"Valid examples:",
	"{\"type\":\"inspect\",\"tool\":\"list_files\",\"args\":{\"limit\":\"50\",\"pattern\":\".go\"},\"summary\":\"find Go files before reading\"}",
	"{\"type\":\"edit\",\"tool\":\"apply_patch\",\"args\":{\"path\":\"main.go\",\"before\":\"old\",\"after\":\"new\"},\"summary\":\"make the targeted change\"}",
	"{\"type\":\"finish\",\"message\":\"Updated the implementation and verified it.\"}",
	"",
	"Invalid example:",
	"{\"type\":\"list_files\",\"args\":{\"limit\":\"50\"}}",
	"The invalid example is wrong because \"list_files\" is a tool name. The correct form is {\"type\":\"inspect\",\"tool\":\"list_files\",...}.",
	"",
	"Available tools:",
	"- list_files: {\"limit\":\"200\",\"pattern\":\"optional-substring-filter\"}",
	"- search_files: {\"query\":\"...\",\"glob\":\"optional-glob\",\"limit\":\"20\"}",
	"- read_file: {\"path\":\"...\"}",
	"- read_file_range: {\"path\":\"...\",\"start_line\":\"1\",\"end_line\":\"80\"}",
	"- apply_patch: {\"path\":\"...\",\"before\":\"exact old snippet\",\"after\":\"replacement snippet\"}",
	"- write_file: {\"path\":\"...\",\"content\":\"...\"}",
	"- run_go_action: {\"action\":\"build|test|format|mod_tidy\"}",
	"- run_command: {\"command\":\"...\",\"dir\":\"optional/subdir\",\"intent\":\"inspection|verification\"}",
	"",
	"Rules:",
	"- Start with inspect actions unless the repository state is already obvious from earlier tool results.",
	"- Prefer search_files, list_files, and read_file_range over reading or rewriting entire files.",
	"- For existing files, prefer apply_patch. Use write_file only for new files or complete rewrites explicitly justified by the task.",
	"- Preserve unrelated code and formatting.",
	"- After edits, run the smallest useful verification step before broader checks. For Go changes, prefer `run_go_action` with `build` or `test` so verification uses `./...` across the workspace.",
	"- If a tool fails, inspect more context and recover. Do not repeat the same failing action blindly.",
	"- Do not wrap JSON in markdown fences.",
}, "\n")

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

type AgentAction struct {
	Type    string            `json:"type"`
	Tool    string            `json:"tool,omitempty"`
	Args    map[string]string `json:"args,omitempty"`
	Summary string            `json:"summary,omitempty"`
	Message string            `json:"message,omitempty"`
}

type AgentState struct {
	FilesRead      []string
	FilesChanged   []string
	CommandsRun    []string
	FailedActions  []string
	ToolSummaries  []string
	LastToolResult string
}

type WorkspaceSummary struct {
	TopLevelFiles []string
	KeyFiles      []string
	DetectedStack []string
}

type ToolResult struct {
	Category string
	Tool     string
	Summary  string
	Body     string
}

var requiredEnvVars = []string{
	"XAI_API_KEY",
}

var modelCaller = callXAI

var toolToActionType = map[string]string{
	"list_files":      "inspect",
	"search_files":    "inspect",
	"read_file":       "inspect",
	"read_file_range": "inspect",
	"apply_patch":     "edit",
	"write_file":      "edit",
	"run_go_action":   "verify",
	"run_command":     "verify",
}

func checkRequiredEnvVars() error {
	var missing []string
	for _, key := range requiredEnvVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func callXAI(messages []Message) (string, error) {
	apiKey := os.Getenv("XAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XAI_API_KEY is not set")
	}

	reqBody := ChatRequest{
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

	client := &http.Client{Timeout: 120 * time.Second}

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

func parseAction(raw string) (AgentAction, error) {
	var action AgentAction
	normalizedRaw, extractErr := extractJSONObject(raw)
	if extractErr != nil {
		return AgentAction{}, extractErr
	}

	if err := json.Unmarshal([]byte(normalizedRaw), &action); err != nil {
		return AgentAction{}, err
	}

	action.Type = strings.TrimSpace(action.Type)
	action.Tool = strings.TrimSpace(action.Tool)
	action.Message = strings.TrimSpace(action.Message)

	if action.Tool != "" {
		if normalizedType, ok := toolToActionType[action.Tool]; ok {
			if action.Type == "" {
				action.Type = normalizedType
			}
		}
	}

	if normalizedType, ok := toolToActionType[action.Type]; ok {
		if action.Tool == "" {
			action.Tool = action.Type
		}
		action.Type = normalizedType
	}

	switch action.Type {
	case "inspect", "edit", "verify":
		if action.Tool == "" {
			return AgentAction{}, errors.New("missing tool for non-finish action")
		}
		if action.Args == nil {
			action.Args = map[string]string{}
		}
	case "finish":
		if strings.TrimSpace(action.Message) == "" {
			return AgentAction{}, errors.New("missing message for finish action")
		}
	default:
		return AgentAction{}, fmt.Errorf("unknown action type %q (allowed: inspect, edit, verify, finish; tool names belong in the tool field)", action.Type)
	}

	return action, nil
}

func extractJSONObject(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", io.ErrUnexpectedEOF
	}

	if json.Valid([]byte(trimmed)) {
		return trimmed, nil
	}

	start := strings.IndexByte(trimmed, '{')
	if start == -1 {
		return "", fmt.Errorf("no JSON object found in reply")
	}

	var (
		depth     int
		inString  bool
		escaped   bool
		candidate strings.Builder
	)

	for _, r := range trimmed[start:] {
		candidate.WriteRune(r)

		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '{':
			depth++
		case !inString && r == '}':
			depth--
			if depth == 0 {
				result := candidate.String()
				if json.Valid([]byte(result)) {
					return result, nil
				}
				break
			}
		}
	}

	if depth > 0 {
		return "", io.ErrUnexpectedEOF
	}

	return "", fmt.Errorf("no valid JSON object found in reply")
}

func parseIntArg(args map[string]string, key string, defaultValue int) (int, error) {
	value := strings.TrimSpace(args[key])
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %w", key, err)
	}
	return parsed, nil
}

func executeAction(action AgentAction) (ToolResult, error) {
	var body string
	var err error

	switch action.Tool {
	case "list_files":
		limit, parseErr := parseIntArg(action.Args, "limit", 200)
		if parseErr != nil {
			return ToolResult{}, parseErr
		}
		body, err = tools.ListFiles(action.Args["pattern"], limit)
	case "search_files":
		query := action.Args["query"]
		if query == "" {
			return ToolResult{}, errors.New(`missing args.query for "search_files"`)
		}
		limit, parseErr := parseIntArg(action.Args, "limit", 20)
		if parseErr != nil {
			return ToolResult{}, parseErr
		}
		body, err = tools.SearchFiles(query, action.Args["glob"], limit)
	case "read_file":
		path := action.Args["path"]
		if path == "" {
			return ToolResult{}, errors.New(`missing args.path for "read_file"`)
		}
		body, err = tools.ReadFile(path)
	case "read_file_range":
		path := action.Args["path"]
		if path == "" {
			return ToolResult{}, errors.New(`missing args.path for "read_file_range"`)
		}
		start, parseErr := parseIntArg(action.Args, "start_line", 1)
		if parseErr != nil {
			return ToolResult{}, parseErr
		}
		end, parseErr := parseIntArg(action.Args, "end_line", start+199)
		if parseErr != nil {
			return ToolResult{}, parseErr
		}
		body, err = tools.ReadFileRange(path, start, end)
	case "apply_patch":
		path := action.Args["path"]
		before := action.Args["before"]
		after := action.Args["after"]
		if path == "" {
			return ToolResult{}, errors.New(`missing args.path for "apply_patch"`)
		}
		if before == "" {
			return ToolResult{}, errors.New(`missing args.before for "apply_patch"`)
		}
		body, err = tools.ApplyPatch(path, before, after)
	case "write_file":
		path := action.Args["path"]
		if path == "" {
			return ToolResult{}, errors.New(`missing args.path for "write_file"`)
		}
		body, err = tools.WriteFile(path, action.Args["content"])
	case "run_go_action":
		actionName := action.Args["action"]
		if actionName == "" {
			return ToolResult{}, errors.New(`missing args.action for "run_go_action"`)
		}
		body, err = tools.RunGoAction(actionName)
	case "run_command":
		command := action.Args["command"]
		if command == "" {
			return ToolResult{}, errors.New(`missing args.command for "run_command"`)
		}
		body, err = tools.RunCommand(command, action.Args["dir"], action.Args["intent"])
	default:
		return ToolResult{}, fmt.Errorf("unknown tool: %s", action.Tool)
	}

	if err != nil {
		return ToolResult{
			Category: action.Type,
			Tool:     action.Tool,
			Summary:  summarizeResult("error", err.Error()),
			Body:     err.Error(),
		}, err
	}

	return ToolResult{
		Category: action.Type,
		Tool:     action.Tool,
		Summary:  summarizeResult("ok", body),
		Body:     body,
	}, nil
}

func summarizeResult(prefix string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return prefix
	}
	body = strings.ReplaceAll(body, "\n", " ")
	if len(body) > 140 {
		body = body[:140] + "..."
	}
	return prefix + ": " + body
}

func updateState(state *AgentState, action AgentAction, result ToolResult, failed bool) {
	switch action.Tool {
	case "read_file", "read_file_range":
		if path := action.Args["path"]; path != "" {
			state.FilesRead = appendUnique(state.FilesRead, path)
		}
	case "apply_patch", "write_file":
		if path := action.Args["path"]; path != "" {
			state.FilesChanged = appendUnique(state.FilesChanged, path)
		}
	case "run_go_action":
		if name := action.Args["action"]; name != "" {
			state.CommandsRun = appendUnique(state.CommandsRun, "go:"+name)
		}
	case "run_command":
		if cmd := action.Args["command"]; cmd != "" {
			state.CommandsRun = appendUnique(state.CommandsRun, cmd)
		}
	}

	if action.Summary != "" {
		state.ToolSummaries = append(state.ToolSummaries, action.Type+" "+action.Tool+": "+action.Summary)
	}
	if failed {
		state.FailedActions = append(state.FailedActions, action.Type+" "+action.Tool+": "+result.Body)
	}
	state.LastToolResult = result.Summary
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func formatState(state AgentState) string {
	return fmt.Sprintf(
		"Agent state:\n- files_read: %s\n- files_changed: %s\n- commands_run: %s\n- recent_summaries: %s\n- failed_actions: %s\n- last_tool_result: %s",
		formatList(state.FilesRead, 8),
		formatList(state.FilesChanged, 8),
		formatList(state.CommandsRun, 8),
		formatList(state.ToolSummaries, 6),
		formatList(state.FailedActions, 4),
		emptyFallback(state.LastToolResult),
	)
}

func formatList(values []string, limit int) string {
	if len(values) == 0 {
		return "(none)"
	}
	start := 0
	if len(values) > limit {
		start = len(values) - limit
	}
	return strings.Join(values[start:], ", ")
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}

func gatherWorkspaceSummary() WorkspaceSummary {
	summary := WorkspaceSummary{}

	listing, err := tools.ListFiles("", 200)
	if err != nil {
		return summary
	}

	lines := strings.Split(strings.TrimSpace(listing), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		summary.TopLevelFiles = append(summary.TopLevelFiles, line)
		switch line {
		case "go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "yarn.lock", "Makefile", "README.md", "README", "main.go":
			summary.KeyFiles = append(summary.KeyFiles, line)
		}
	}

	stackSet := map[string]struct{}{}
	for _, file := range summary.TopLevelFiles {
		switch {
		case file == "go.mod" || strings.HasSuffix(file, ".go"):
			stackSet["Go"] = struct{}{}
		case file == "package.json" || strings.HasSuffix(file, ".ts") || strings.HasSuffix(file, ".tsx") || strings.HasSuffix(file, ".js"):
			stackSet["Node.js"] = struct{}{}
		case file == "Makefile":
			stackSet["Make"] = struct{}{}
		}
	}

	for stack := range stackSet {
		summary.DetectedStack = append(summary.DetectedStack, stack)
	}
	sort.Strings(summary.DetectedStack)
	sort.Strings(summary.KeyFiles)

	if len(summary.TopLevelFiles) > 25 {
		summary.TopLevelFiles = summary.TopLevelFiles[:25]
	}

	return summary
}

func formatWorkspaceSummary(summary WorkspaceSummary) string {
	return fmt.Sprintf(
		"Workspace summary:\n- top_level_files: %s\n- key_files: %s\n- detected_stack: %s",
		formatList(summary.TopLevelFiles, 25),
		formatList(summary.KeyFiles, 10),
		formatList(summary.DetectedStack, 10),
	)
}

func runAgent(userInput string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	workspaceSummary := gatherWorkspaceSummary()
	state := AgentState{}

	messages := []Message{
		{Role: "system", Content: fmt.Sprintf(systemPromptTemplate, cwd)},
		{Role: "user", Content: userInput},
		{Role: "system", Content: formatWorkspaceSummary(workspaceSummary)},
		{Role: "system", Content: formatState(state)},
	}

	for i := 0; i < constants.MaxIterations; i++ {
		reply, err := modelCaller(messages)
		if err != nil {
			return "", err
		}

		fmt.Printf("\n=== agent step %d ===\n%s\n", i+1, reply)

		action, err := parseAction(reply)
		if err != nil {
			messages = append(messages, Message{Role: "assistant", Content: reply})
			messages = append(messages, Message{
				Role: "user",
				Content: fmt.Sprintf(
					"Your last response was invalid JSON or not a valid agent action.\nError: %v\nReturn exactly one valid JSON object matching the schema, with no markdown fences and no extra text.\n%s",
					err,
					formatState(state),
				),
			})
			continue
		}

		if action.Type == "finish" {
			return action.Message, nil
		}

		fmt.Printf("-> %s %s: %s\n", action.Type, action.Tool, emptyFallback(action.Summary))

		result, execErr := executeAction(action)
		if execErr != nil {
			updateState(&state, action, result, true)
			messages = append(messages, Message{Role: "assistant", Content: reply})
			messages = append(messages, Message{
				Role: "user",
				Content: fmt.Sprintf(
					"Tool failure.\nCategory: %s\nTool: %s\nSummary: %s\nBody:\n%s\n\n%s\nRecover by inspecting more context or choosing a different action. Do not repeat the same failure blindly.",
					result.Category,
					result.Tool,
					result.Summary,
					result.Body,
					formatState(state),
				),
			})
			continue
		}

		updateState(&state, action, result, false)
		messages = append(messages, Message{Role: "assistant", Content: reply})
		messages = append(messages, Message{
			Role: "user",
			Content: fmt.Sprintf(
				"Tool result.\nCategory: %s\nTool: %s\nSummary: %s\nBody:\n%s\n\n%s\nContinue with the next best inspect, edit, verify, or finish action.",
				result.Category,
				result.Tool,
				result.Summary,
				result.Body,
				formatState(state),
			),
		})
	}

	return fmt.Sprintf("max iterations (%d) reached", constants.MaxIterations), nil
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
	if err := checkRequiredEnvVars(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

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
