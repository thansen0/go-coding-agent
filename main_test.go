package main

import (
	"askthomas/tools"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCheckRequiredEnvVars(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")

	if err := checkRequiredEnvVars(); err == nil {
		t.Fatal("checkRequiredEnvVars succeeded with XAI_API_KEY unset")
	}

	t.Setenv("XAI_API_KEY", "present")

	if err := checkRequiredEnvVars(); err != nil {
		t.Fatalf("checkRequiredEnvVars returned error with XAI_API_KEY set: %v", err)
	}
}

func TestParseActionNormalizesToolNameInTypeForInspect(t *testing.T) {
	action, err := parseAction(`{"type":"list_files","args":{"limit":"50"},"summary":"inspect files"}`)
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "inspect" {
		t.Fatalf("action.Type = %q, want %q", action.Type, "inspect")
	}
	if action.Tool != "list_files" {
		t.Fatalf("action.Tool = %q, want %q", action.Tool, "list_files")
	}
	if action.Args["limit"] != "50" {
		t.Fatalf("action.Args[limit] = %q, want %q", action.Args["limit"], "50")
	}
}

func TestParseActionNormalizesToolNameInTypeForEdit(t *testing.T) {
	action, err := parseAction(`{"type":"apply_patch","args":{"path":"main.go","before":"old","after":"new"},"summary":"edit file"}`)
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "edit" {
		t.Fatalf("action.Type = %q, want %q", action.Type, "edit")
	}
	if action.Tool != "apply_patch" {
		t.Fatalf("action.Tool = %q, want %q", action.Tool, "apply_patch")
	}
}

func TestParseActionNormalizesToolNameInTypeForVerify(t *testing.T) {
	action, err := parseAction(`{"type":"run_command","args":{"command":"go test ./..."},"summary":"verify changes"}`)
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "verify" {
		t.Fatalf("action.Type = %q, want %q", action.Type, "verify")
	}
	if action.Tool != "run_command" {
		t.Fatalf("action.Tool = %q, want %q", action.Tool, "run_command")
	}
}

func TestParseActionNormalizesReadFileToolNameInType(t *testing.T) {
	action, err := parseAction(`{"type":"read_file","args":{"path":"main.go"},"summary":"inspect the entrypoint"}`)
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "inspect" {
		t.Fatalf("action.Type = %q, want %q", action.Type, "inspect")
	}
	if action.Tool != "read_file" {
		t.Fatalf("action.Tool = %q, want %q", action.Tool, "read_file")
	}
	if action.Args["path"] != "main.go" {
		t.Fatalf("action.Args[path] = %q, want %q", action.Args["path"], "main.go")
	}
}

func TestParseActionTrimsWhitespaceAroundTypeAndTool(t *testing.T) {
	action, err := parseAction(`{"type":" read_file ","tool":" ","args":{"path":"main.go"},"summary":"inspect the entrypoint"}`)
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "inspect" {
		t.Fatalf("action.Type = %q, want %q", action.Type, "inspect")
	}
	if action.Tool != "read_file" {
		t.Fatalf("action.Tool = %q, want %q", action.Tool, "read_file")
	}
}

func TestParseActionRejectsUnknownType(t *testing.T) {
	_, err := parseAction(`{"type":"unknown_action","args":{}}`)
	if err == nil {
		t.Fatal("parseAction succeeded for unknown type")
	}
	if !strings.Contains(err.Error(), "allowed: inspect, edit, verify, finish") {
		t.Fatalf("parseAction error = %q", err)
	}
}

func TestParseActionExtractsJSONObjectFromFencedReply(t *testing.T) {
	action, err := parseAction("```json\n{\"type\":\"read_file\",\"args\":{\"path\":\"main.go\"},\"summary\":\"inspect\"}\n```")
	if err != nil {
		t.Fatalf("parseAction: %v", err)
	}

	if action.Type != "inspect" || action.Tool != "read_file" {
		t.Fatalf("action = %+v", action)
	}
}

func TestParseActionReturnsUnexpectedEOFForTruncatedJSON(t *testing.T) {
	_, err := parseAction(`{"type":"read_file","args":{"path":"main.go"}`)
	if err == nil {
		t.Fatal("parseAction succeeded for truncated JSON")
	}
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("parseAction error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestSystemPromptTemplateMatchesAgentWorkflow(t *testing.T) {
	requiredSnippets := []string{
		"Search for relevant files and symbols before reading large files.",
		"`go build ./...`",
		"`go test ./...`",
		"`constants/` package",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(systemPromptTemplate, snippet) {
			t.Fatalf("systemPromptTemplate missing %q", snippet)
		}
	}
}

func TestRunAgentUsesInspectionBeforeEditAndFinishes(t *testing.T) {
	root := t.TempDir()
	if err := tools.SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	if _, err := tools.WriteFile("main.go", "package main\n\nfunc target() string { return \"old\" }\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := tools.WriteFile("go.mod", "module example\n\ngo 1.23.0\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldCaller := modelCaller
	defer func() { modelCaller = oldCaller }()

	var prompts []string
	responses := []string{
		`{"type":"inspect","tool":"search_files","args":{"query":"target","glob":"*.go","limit":"10"},"summary":"find the target function"}`,
		`{"type":"edit","tool":"apply_patch","args":{"path":"main.go","before":"return \"old\"","after":"return \"new\""},"summary":"update the implementation"}`,
		`{"type":"finish","message":"Updated target() to return new."}`,
	}
	modelCaller = func(messages []Message) (string, error) {
		prompts = append(prompts, messages[len(messages)-1].Content)
		reply := responses[0]
		responses = responses[1:]
		return reply, nil
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(oldWD)

	final, err := runAgent("change target")
	if err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if final != "Updated target() to return new." {
		t.Fatalf("final = %q", final)
	}

	got, err := tools.ReadFile("main.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, `return "new"`) {
		t.Fatalf("updated file = %q", got)
	}

	if len(prompts) < 2 || !strings.Contains(prompts[1], "search_files") {
		t.Fatalf("agent did not receive inspection result before editing: %+v", prompts)
	}
}

func TestRunAgentRecoversAfterToolFailure(t *testing.T) {
	root := t.TempDir()
	if err := tools.SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	if _, err := tools.WriteFile("main.go", "package main\n\nfunc target() string { return \"old\" }\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldCaller := modelCaller
	defer func() { modelCaller = oldCaller }()

	responses := []string{
		`{"type":"edit","tool":"apply_patch","args":{"path":"main.go","before":"missing","after":"new"},"summary":"this should fail"}`,
		`{"type":"inspect","tool":"read_file","args":{"path":"main.go"},"summary":"inspect after failure"}`,
		`{"type":"finish","message":"Done after recovery."}`,
	}
	modelCaller = func(messages []Message) (string, error) {
		reply := responses[0]
		responses = responses[1:]
		return reply, nil
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(oldWD)

	final, err := runAgent("change target")
	if err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if final != "Done after recovery." {
		t.Fatalf("final = %q", final)
	}
}

func TestToolToActionTypeCoversSupportedTools(t *testing.T) {
	expected := map[string]string{
		"list_files":      "inspect",
		"search_files":    "inspect",
		"read_file":       "inspect",
		"read_file_range": "inspect",
		"apply_patch":     "edit",
		"write_file":      "edit",
		"run_go_action":   "verify",
		"run_command":     "verify",
	}

	if !reflect.DeepEqual(toolToActionType, expected) {
		t.Fatalf("toolToActionType = %#v, want %#v", toolToActionType, expected)
	}
}

func TestRunAgentRecoversAfterInvalidJSONReply(t *testing.T) {
	root := t.TempDir()
	if err := tools.SetWorkspaceRoot(root); err != nil {
		t.Fatalf("SetWorkspaceRoot: %v", err)
	}
	if _, err := tools.WriteFile("main.go", "package main\n\nfunc target() string { return \"old\" }\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldCaller := modelCaller
	defer func() { modelCaller = oldCaller }()

	var prompts []string
	responses := []string{
		`{"type":"read_file","args":{"path":"main.go"}`,
		"```json\n{\"type\":\"inspect\",\"tool\":\"read_file\",\"args\":{\"path\":\"main.go\"},\"summary\":\"inspect after retry\"}\n```",
		`{"type":"finish","message":"Done after invalid JSON recovery."}`,
	}
	modelCaller = func(messages []Message) (string, error) {
		prompts = append(prompts, messages[len(messages)-1].Content)
		reply := responses[0]
		responses = responses[1:]
		return reply, nil
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(oldWD)

	final, err := runAgent("inspect target")
	if err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if final != "Done after invalid JSON recovery." {
		t.Fatalf("final = %q", final)
	}
	if len(prompts) < 2 || !strings.Contains(prompts[1], "invalid JSON") {
		t.Fatalf("agent did not request a corrected action after invalid JSON: %+v", prompts)
	}
}
