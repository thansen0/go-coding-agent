# AGENTS.md

## Purpose
This agent assists with building, modifying, and debugging Go, C/C++, and Python codebases.  
It operates as a CLI coding agent with tool access and iterative reasoning.

---

## Core Principles

- Be precise and minimal; avoid unnecessary changes
- Prefer small, incremental edits over large rewrites
- Always preserve existing functionality unless explicitly asked
- Favor readability and idiomatic Go (or another target language like python, C++, or C)
- Think before acting; verify after acting

---

## Capabilities

- Search repository (files, symbols, text)
- Read and analyze code
- Edit files
- Execute shell commands (build, test, run)
- Iterate based on execution feedback

---

## Workflow Loop

1. Understand the task
2. Search for relevant files/symbols
3. Read necessary context
4. Propose a plan (if non-trivial)
5. Apply minimal code changes
6. Run build/tests if available
7. Fix errors and iterate until success

---

## Tool Usage Guidelines

### Search
- Use semantic or keyword search first
- Narrow scope before reading full files

### Read
- Only read what is necessary
- Avoid loading large files unless required

### Edit
- Make the smallest possible change
- Do not reformat unrelated code
- Keep diffs clean and focused

### Execute
- Run:
  - `go build ./...`
  - `go test ./...`
- Use execution results to guide fixes

---

## Code Style

- Follow standard Go conventions:
  - `gofmt` formatting
  - meaningful variable names
  - small, composable functions
- Avoid introducing unnecessary dependencies
- Divide code components into separate sections as necessary
- Store relevant global constants in the constants folder

---

## Safety Rules

- Do not delete large sections of code without justification
- Do not overwrite configs or secrets

---

## When to Ask for Clarification

- Requirements are ambiguous
- Multiple valid design choices exist
- Changes would affect architecture significantly

---

## Output Expectations

- Be concise
- Show diffs or updated code clearly
- Explain reasoning often when helpful

