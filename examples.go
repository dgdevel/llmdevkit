package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func examplesHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	toolName, err := req.RequireString("tool_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	examples, ok := toolExamples[toolName]
	if !ok {
		available := make([]string, 0, len(toolExamples))
		for k := range toolExamples {
			available = append(available, k)
		}
		sort.Strings(available)
		return mcp.NewToolResultError(fmt.Sprintf("unknown tool %q. Available: %s", toolName, strings.Join(available, ", "))), nil
	}
	return mcp.NewToolResultText(examples), nil
}

var toolExamples = map[string]string{
	"ls": `Example 1: List all .go files recursively

Request:
  tool: ls
  arguments: {"pathspec": "**/*.go"}

Response:
  main.go
  server/handler.go
  server/routes.go

Example 2: List files in root only

Request:
  tool: ls
  arguments: {"pathspec": "*.txt"}

Response:
  file1.txt
  notes.txt

Example 3: List directories

Request:
  tool: ls
  arguments: {"pathspec": "src*"}

Response:
  src/`,

	"file_read": `Example 1: Read full file

Request:
  tool: file_read
  arguments: {"path": "/main.go", "line_range": ":"}

Response:
  ----- /main.go - lines from 1 to 4 -----
  package main

  func main() {
  	fmt.Println("hello")
  }
  ----- /main.go - EOF -----

Example 2: Read a specific line range (lines 3-5, 1-indexed)

Request:
  tool: file_read
  arguments: {"path": "/main.go", "line_range": "2:5"}

Response:
  ----- /main.go - lines from 3 to 5 -----
  func main() {
  	fmt.Println("hello")
  }
  ----- /main.go - EOF -----

Example 3: Large files are split into blocks (default 30 lines)

Request:
  tool: file_read
  arguments: {"path": "/bigfile.go", "line_range": ":"}

Response:
  ----- /bigfile.go - lines from 1 to 30 -----
  (30 lines of raw content)
  ----- /bigfile.go - lines from 31 to 60 -----
  (next 30 lines)
  ----- /bigfile.go - lines from 61 to 75 -----
  (final lines)
  ----- /bigfile.go - EOF -----`,

	"file_create": `Example 1: Create a new file

Request:
  tool: file_create
  arguments: {"path": "/hello.txt", "content": "Hello, world!"}

Response:
  ok

Example 2: File already exists returns error

Request:
  tool: file_create
  arguments: {"path": "/hello.txt", "content": "different content"}

Response (error):
  file already exists

Example 3: Create a file in a nested directory (directories are created automatically)

Request:
  tool: file_create
  arguments: {"path": "/src/pkg/util.go", "content": "package pkg"}

Response:
  ok`,

	"mv": `Example 1: Move a file

Request:
  tool: mv
  arguments: {"source": "/old.txt", "dest": "/new.txt"}

Response:
  ok

Example 2: Source not found returns error

Request:
  tool: mv
  arguments: {"source": "/missing.txt", "dest": "/somewhere.txt"}

Response (error):
  source not found

Example 3: Destination already exists returns error

Request:
  tool: mv
  arguments: {"source": "/a.txt", "dest": "/b.txt"}

Response (error):
  destination already exists`,

	"grep": `Example 1: Simple pattern match with context

Request:
  tool: grep
  arguments: {"pattern": "TODO", "pathspec": "*.go"}

Response:
  main.go:12:import os
  main.go:13:
  main.go:14:// TODO: handle error
  main.go:15:
  main.go:16:func main() {

Example 2: Regex pattern

Request:
  tool: grep
  arguments: {"pattern": "^func ", "pathspec": "**/*.go"}

Response:
  main.go:4:import (
  main.go:5:
  main.go:6:func main() {
  main.go:7:	fmt.Println("hello")
  main.go:8:}
  --
  handler.go:10:import (
  handler.go:11:
  handler.go:12:func handle() error {
  handler.go:13:	return nil
  handler.go:14:}

Example 3: No match returns empty string

Request:
  tool: grep
  arguments: {"pattern": "NONEXISTENT_PATTERN", "pathspec": "*.go"}

Response:
  (empty string)`,

	"sed": `Example 1: Replace text in matching files

Request:
  tool: sed
  arguments: {"pattern": "old_name", "replacement": "new_name", "pathspec": "**/*.go"}

Response:
  main.go
  handler.go

Example 2: No matches returns empty string

Request:
  tool: sed
  arguments: {"pattern": "nothing_matches_this", "replacement": "x", "pathspec": "*.txt"}

Response:
  (empty string)

Example 3: Replace in a specific file

Request:
  tool: sed
  arguments: {"pattern": "v1.0", "replacement": "v2.0", "pathspec": "config.yaml"}

Response:
  config.yaml`,

	"edit": `Example 1: Replace a single line

Request:
  tool: edit
  arguments: {"path": "/main.go", "start_line_number": 5, "original_window": "fmt.Println(\"hello\")", "modified_window": "fmt.Println(\"world\")"}

Response:
  ok

Example 2: Replace a block of lines

Request:
  tool: edit
  arguments: {"path": "/config.yaml", "start_line_number": 3, "original_window": "host: localhost\nport: 8080", "modified_window": "host: 0.0.0.0\nport: 9090"}

Response:
  ok

Example 3: start_line_number is slightly wrong but edit still applies

Request:
  tool: edit
  arguments: {"path": "/main.go", "start_line_number": 7, "original_window": "fmt.Println(\"hello\")", "modified_window": "fmt.Println(\"world\")"}

Response:
  ok, start_line_number was wrong, it was 5 instead`,

	"rm": `Example 1: Delete a file

Request:
  tool: rm
  arguments: {"path": "/old_file.txt"}

Response:
  ok

Example 2: Delete a directory recursively

Request:
  tool: rm
  arguments: {"path": "/old_directory"}

Response:
  ok

Example 3: Nonexistent path also returns ok

Request:
  tool: rm
  arguments: {"path": "/already_gone.txt"}

Response:
  ok`,

	"stat": `Example 1: Stat a file

Request:
  tool: stat
  arguments: {"path": "/main.go"}

Response:
  Type: file
  Size: 234, 234 B
  Permissions: read,write
  Owner: alice(uid=1000)
  Group: alice(gid=1000)
  Access: 2025-04-25T10:30:00
  Modify: 2025-04-25T09:15:00
  Change: 2025-04-25T09:15:00
  Birth: 2025-04-20T08:00:00

Example 2: Stat a directory

Request:
  tool: stat
  arguments: {"path": "/src"}

Response:
  Type: directory
  Size: 4096, 4.0 KB
  Permissions: read,write,execute
  Owner: alice(uid=1000)
  Group: alice(gid=1000)
  Access: 2025-04-25T10:30:00
  Modify: 2025-04-25T09:15:00
  Change: 2025-04-25T09:15:00
  Birth: 2025-04-20T08:00:00

Example 3: Nonexistent path returns error

Request:
  tool: stat
  arguments: {"path": "/no_such_file"}

Response (error):
  stat /no_such_file: no such file or directory`,

	"tasks_list": `Example 1: List existing tasks

Request:
  tool: tasks_list
  arguments: {}

Response:
  1. [X] Design the API
  2. [_] Implement features
  2.1 [X] Add config loading
  2.2 [ ] Add error handling
  3. [ ] Write documentation

Example 2: No tasks returns empty string

Request:
  tool: tasks_list
  arguments: {}

Response:
  (empty string)

Example 3: Tasks with mixed statuses

Request:
  tool: tasks_list
  arguments: {}

Response:
  1. [ ] Set up CI pipeline
  2. [_] Write unit tests
  3. [ ] Add integration tests`,

	"task_create": `Example 1: Create a top-level task

Request:
  tool: task_create
  arguments: {"description": "Set up the database schema"}

Response:
  Created ID: 1

Example 2: Create a sub-task under task 2

Request:
  tool: task_create
  arguments: {"description": "Write migration script", "parent": "2"}

Response:
  Created ID: 2.1

Example 3: Multiple sub-tasks get auto-incremented IDs

Request:
  tool: task_create
  arguments: {"description": "Add seed data", "parent": "2"}

Response:
  Created ID: 2.2`,

	"task_set_status": `Example 1: Mark a task as in progress

Request:
  tool: task_set_status
  arguments: {"ID": "1", "status": "in_progress"}

Response:
  true

Example 2: Mark a task as completed

Request:
  tool: task_set_status
  arguments: {"ID": "2.1", "status": "completed"}

Response:
  true

Example 3: Invalid status returns error

Request:
  tool: task_set_status
  arguments: {"ID": "1", "status": "done"}

Response (error):
  invalid status: done`,

	"task_delete": `Example 1: Delete a task

Request:
  tool: task_delete
  arguments: {"ID": "3"}

Response:
  true

Example 2: Deleting a parent also removes its children

Request:
  tool: task_delete
  arguments: {"ID": "2"}

Response:
  true
  (task 2, 2.1, 2.2 are all removed)

Example 3: Deleting a non-existent task still succeeds

Request:
  tool: task_delete
  arguments: {"ID": "99"}

Response:
  true`,

	"tasks_clear": `Example 1: Clear all tasks

Request:
  tool: tasks_clear
  arguments: {}

Response:
  true

Example 2: Clear when already empty

Request:
  tool: tasks_clear
  arguments: {}

Response:
  true

Example 3: After clearing, tasks_list returns empty

Request:
  tool: tasks_clear
  arguments: {}

Response:
  true
  (subsequent call to tasks_list will return an empty string)`,

	"w3m-dump": `Example 1: Fetch a webpage

Request:
  tool: w3m-dump
  arguments: {"url": "https://example.com/docs/api"}

Response:
  # API Documentation

  ## Getting Started
  Welcome to the API. This endpoint allows you to...

Example 2: Unsupported URL scheme returns error

Request:
  tool: w3m-dump
  arguments: {"url": "ftp://files.example.com/data"}

Response (error):
  only http and https URLs are supported

Example 3: Very long pages get truncated

Request:
  tool: w3m-dump
  arguments: {"url": "https://example.com/very-long-article"}

Response:
  # PAGE TOO LONG - PARTIAL OUTPUT

  # Article Title
  First part of the article content...`,

	"online_search": `Example 1: Search with results

Request:
  tool: online_search
  arguments: {"search_query": "golang mcp server"}

Response:
  Title: mark3labs/mcp-go
  Url: https://github.com/mark3labs/mcp-go
  Description:
  An MCP server implementation in Go.

  Title: Building MCP Servers
  Url: https://modelcontextprotocol.io
  Description:
  Learn how to build Model Context Protocol servers.

Example 2: No results found

Request:
  tool: online_search
  arguments: {"search_query": "xyzzyqwerty12345nonexistent"}

Response:
  No results found

Example 3: Multi-word query

Request:
  tool: online_search
  arguments: {"search_query": "how to parse json in go"}

Response:
  Title: encoding/json - Go Documentation
  Url: https://pkg.go.dev/encoding/json
  Description:
  Package json implements encoding and decoding of JSON.`,

	"available_commands": `Example 1: List configured commands

Request:
  tool: available_commands
  arguments: {}

Response:
  Command: build
  Arguments: target
  Description: Build the project

  Command: test
  Arguments: no arguments are taken, invoke without arguments
  Description: Run tests

Example 2: No commands configured returns empty string

Request:
  tool: available_commands
  arguments: {}

Response:
  (empty string)

Example 3: Single command with multiple arguments

Request:
  tool: available_commands
  arguments: {}

Response:
  Command: deploy
  Arguments: environment
  Arguments: version
  Description: Deploy to the specified environment`,

	"run_command": `Example 1: Run a command with arguments

Request:
  tool: run_command
  arguments: {"name": "build", "arguments": ["release"], "timeout": 60}

Response:
  Build complete: release binary at ./bin/app

Example 2: Run a command without arguments

Request:
  tool: run_command
  arguments: {"name": "test", "arguments": [], "timeout": 120}

Response:
  PASS
  ok  github.com/example/project  0.123s

Example 3: Unknown command returns error

Request:
  tool: run_command
  arguments: {"name": "deploy", "arguments": [], "timeout": 30}

Response (error):
  unknown command: deploy`,

	"examples": `Example 1: Get examples for a tool

Request:
  tool: examples
  arguments: {"tool_name": "ls"}

Response:
  Example 1: List root directory
  ...

Example 2: Unknown tool returns error with available names

Request:
  tool: examples
  arguments: {"tool_name": "nonexistent"}

Response (error):
  unknown tool "nonexistent". Available: diff, file_read, ...

Example 3: Get examples for task tools

Request:
  tool: examples
  arguments: {"tool_name": "task_create"}

Response:
  Example 1: Create a top-level task
  ...`,

	"relevant_code": `Example 1: Find authentication-related code

Request:
  tool: relevant_code
  arguments: {"prompt": "user authentication and login logic"}

Response:
  auth/handler.go:45-82:go:function:func (h *Handler) Login(w http.ResponseWriter, r *http.Request)
  auth/middleware.go:12-38:go:function:func Authenticate(next http.Handler) http.Handler

Example 2: No relevant code found returns empty string

Request:
  tool: relevant_code
  arguments: {"prompt": "quantum entanglement simulation"}

Response:
  (empty string)

Example 3: Indexer still indexing returns empty string

Request:
  tool: relevant_code
  arguments: {"prompt": "database connection pool"}

Response:
  (empty string)`,
}
