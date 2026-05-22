package main

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"llmdevkit/internal/tools"
)

func addCoreTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("ls",
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), tools.LsHandler)

	s.AddTool(mcp.NewTool("tree",
		mcp.WithDescription("Project directories"),
		mcp.WithBoolean("with_files",
			mcp.Description("Include files (default=false)"),
		),
	), tools.TreeHandler)

	s.AddTool(mcp.NewTool("file_read",
		mcp.WithDescription("Read file"),
		mcp.WithString("path",
			mcp.Required(),
		),
		mcp.WithString("line_range",
			mcp.Required(),
			mcp.Description("Line range, 1-indexed. Formats: from:to, from-to, [from:to], [from-to]"),
		),
	), tools.FileReadHandler)

	s.AddTool(mcp.NewTool("file_create",
		mcp.WithString("path",
			mcp.Required(),
		),
		mcp.WithString("content",
			mcp.Required(),
		),
		mcp.WithBoolean("overwrite_existing"),
	), tools.CreateHandler)

	s.AddTool(mcp.NewTool("mv",
		mcp.WithDescription("Move files"),
		mcp.WithString("source",
			mcp.Required(),
		),
		mcp.WithString("dest",
			mcp.Required(),
		),
	), tools.MvHandler)

	s.AddTool(mcp.NewTool("grep",
		mcp.WithDescription("Print lines matching pattern with context (`grep -A1 -B1`)"),
		mcp.WithString("pattern",
			mcp.Required(),
			mcp.Description("Regexp"),
		),
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), tools.GrepHandler)

	s.AddTool(mcp.NewTool("sed",
		mcp.WithDescription("Search and replace in files (`sed -i`)"),
		mcp.WithString("pattern",
			mcp.Required(),
			mcp.Description("Regexp"),
		),
		mcp.WithString("replacement",
			mcp.Required(),
		),
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), tools.SedHandler)

	s.AddTool(mcp.NewTool("edit",
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("File path"),
		),
		mcp.WithNumber("start_line_number",
			mcp.Required(),
			mcp.Description("Line number where original_window begins (1-indexed)"),
		),
		mcp.WithString("original_window",
			mcp.Required(),
			mcp.Description("Text to be replaced"),
		),
		mcp.WithString("modified_window",
			mcp.Required(),
			mcp.Description("Text to be inserted"),
		),
	), tools.EditHandler)

	s.AddTool(mcp.NewTool("rm",
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("File path"),
		),
	), tools.RmHandler)

	s.AddTool(mcp.NewTool("stat",
		mcp.WithDescription("Infos on files and directories"),
		mcp.WithString("path",
			mcp.Required(),
		),
	), tools.StatHandler)

	// Task management tools
	s.AddTool(mcp.NewTool("tasks_list",
		mcp.WithDescription("List of tasks ([ ] created, [_] in progress, [X] completed)"),
	), tools.TasksListHandler)

	s.AddTool(mcp.NewTool("task_create",
		mcp.WithString("description",
			mcp.Required(),
		),
		mcp.WithString("parent",
			mcp.Description("ID of parent task, optional"),
		),
		mcp.WithString("status",
			mcp.Description("One of: created, in_progress, completed. Optional"),
		),
	), tools.TasksCreateHandler)

	s.AddTool(mcp.NewTool("task_set_status",
		mcp.WithDescription("Change status of task"),
		mcp.WithString("ID",
			mcp.Required(),
			mcp.Description("Task ID"),
		),
		mcp.WithString("status",
			mcp.Required(),
			mcp.Description("One of: created, in_progress, completed"),
		),
	), tools.TasksSetStatusHandler)

	s.AddTool(mcp.NewTool("task_delete",
		mcp.WithString("ID",
			mcp.Required(),
		),
	), tools.TasksDeleteHandler)

	s.AddTool(mcp.NewTool("tasks_clear",
		mcp.WithDescription("Clear all tasks"),
	), tools.TasksClearHandler)

	// Web / search tools
	s.AddTool(mcp.NewTool("w3m-dump",
		mcp.WithDescription("Fetch a webpage text (like `w3m-dump`)"),
		mcp.WithString("url",
			mcp.Required(),
		),
	), tools.W3mdumpHandler)

	s.AddTool(mcp.NewTool("online_search",
		mcp.WithDescription("Search online"),
		mcp.WithString("search_query",
			mcp.Required(),
		),
	), tools.OnlineSearchHandler)

	// Command execution tools
	s.AddTool(mcp.NewTool("available_commands",
		mcp.WithDescription("List available commands"),
	), tools.AvailableCommandsHandler)

	s.AddTool(mcp.NewTool("run_command",
		mcp.WithDescription("Run the command from available_commands"),
		mcp.WithString("name",
			mcp.Required(),
		),
		mcp.WithArray("arguments",
			mcp.Description("Array of strings to pass to the command line"),
			mcp.WithStringItems(),
		),
		mcp.WithNumber("timeout",
			mcp.Required(),
			mcp.Description("Timeout in seconds"),
		),
	), tools.RunCommandHandler)
}
