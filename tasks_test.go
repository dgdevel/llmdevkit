package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func setupTasksTest(t *testing.T) {
	t.Helper()
	rootDir = t.TempDir()
}

func TestTasksListEmpty(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_list returned error")
	}
	text := textOf(t, result)
	if text != "" {
		t.Errorf("expected empty output, got %q", text)
	}
}

func TestTasksCreateTopLevel(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "First task",
			},
		},
	}
	result, err := tasksCreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_create returned error")
	}
	text := textOf(t, result)
	if !strings.HasPrefix(text, "Created ID: 1\n") {
		t.Errorf("expected 'Created ID: 1' prefix, got %q", text)
	}
}

func TestTasksCreateMultiple(t *testing.T) {
	setupTasksTest(t)
	for _, desc := range []string{"Task A", "Task B", "Task C"} {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "task_create",
				Arguments: map[string]interface{}{
					"description": desc,
				},
			},
		}
		result, err := tasksCreateHandler(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatal("tasks_create returned error")
		}
	}
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	expected := "1. [ ] Task A\n2. [ ] Task B\n3. [ ] Task C\n"
	if text != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, text)
	}
}

func TestTasksCreateWithParent(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Parent task",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Child task",
				"parent":      "1",
			},
		},
	}
	result, err := tasksCreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_create returned error")
	}
	text := textOf(t, result)
	if !strings.HasPrefix(text, "Created ID: 1.1\n") {
		t.Errorf("expected 'Created ID: 1.1' prefix, got %q", text)
	}
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err = tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text = textOf(t, result)
	expected := "1. [ ] Parent task\n1.1 [ ] Child task\n"
	if text != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, text)
	}
}

func TestTasksCreateGrandchild(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Root",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Child",
				"parent":      "1",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Grandchild",
				"parent":      "1.1",
			},
		},
	}
	result, err := tasksCreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_create returned error")
	}
	text := textOf(t, result)
	if !strings.HasPrefix(text, "Created ID: 1.1.1\n") {
		t.Errorf("expected 'Created ID: 1.1.1' prefix, got %q", text)
	}
}

func TestTasksCreateParentNotFound(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Orphan",
				"parent":      "99",
			},
		},
	}
	result, err := tasksCreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-existent parent")
	}
}

func TestTasksSetStatus(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Task to update",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)

	for _, tc := range []struct {
		status string
		marker string
	}{
		{"in_progress", "[_]"},
		{"completed", "[X]"},
		{"created", "[ ]"},
	} {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "task_set_status",
				Arguments: map[string]interface{}{
					"ID":     "1",
					"status": tc.status,
				},
			},
		}
		result, err := tasksSetStatusHandler(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("tasks_set_status returned error for status %s", tc.status)
		}

		req2 := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "tasks_list",
				Arguments: map[string]interface{}{},
			},
		}
		result2, _ := tasksListHandler(context.Background(), req2)
		text := textOf(t, result2)
		if !strings.Contains(text, tc.marker+" Task to update") {
			t.Errorf("expected %s marker after setting status to %s, got: %s", tc.marker, tc.status, text)
		}
	}
}

func TestTasksSetStatusInvalid(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_set_status",
			Arguments: map[string]interface{}{
				"ID":     "1",
				"status": "invalid",
			},
		},
	}
	result, err := tasksSetStatusHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid status")
	}
}

func TestTasksSetStatusNotFound(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_set_status",
			Arguments: map[string]interface{}{
				"ID":     "99",
				"status": "completed",
			},
		},
	}
	result, err := tasksSetStatusHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.HasPrefix(text, "Not found") {
		t.Errorf("expected 'Not found' prefix, got %q", text)
	}
}

func TestTasksDelete(t *testing.T) {
	setupTasksTest(t)
	for _, desc := range []string{"Task A", "Task B", "Task C"} {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "task_create",
				Arguments: map[string]interface{}{
					"description": desc,
				},
			},
		}
		tasksCreateHandler(context.Background(), req)
	}
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_delete",
			Arguments: map[string]interface{}{
				"ID": "2",
			},
		},
	}
	result, err := tasksDeleteHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_delete returned error")
	}
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err = tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	expected := "1. [ ] Task A\n3. [ ] Task C\n"
	if text != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, text)
	}
}

func TestTasksDeleteWithChildren(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Parent",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Child 1",
				"parent":      "1",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Child 2",
				"parent":      "1",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_delete",
			Arguments: map[string]interface{}{
				"ID": "1",
			},
		},
	}
	result, err := tasksDeleteHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_delete returned error")
	}
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err = tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	if text != "" {
		t.Errorf("expected empty output after deleting parent with children, got: %q", text)
	}
}

func TestTasksClear(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "Task to clear",
			},
		},
	}
	tasksCreateHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_clear",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := tasksClearHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_clear returned error")
	}
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_list",
			Arguments: map[string]interface{}{},
		},
	}
	result, err = tasksListHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	if text != "" {
		t.Errorf("expected empty output after clear, got %q", text)
	}
}

func TestTasksClearEmpty(t *testing.T) {
	setupTasksTest(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "tasks_clear",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := tasksClearHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tasks_clear returned error on empty")
	}
}

func TestParseTaskLine(t *testing.T) {
	tests := []struct {
		line     string
		wantID   string
		wantSt   string
		wantDesc string
	}{
		{"1. [X] Completed task", "1", "completed", "Completed task"},
		{"2. [_] In progress task", "2", "in_progress", "In progress task"},
		{"3. [ ] Created task", "3", "created", "Created task"},
		{"2.1 [X] Sub task", "2.1", "completed", "Sub task"},
		{"10.1.3 [ ] Deep task", "10.1.3", "created", "Deep task"},
	}
	for _, tt := range tests {
		got, ok := parseTaskLine(tt.line)
		if !ok {
			t.Errorf("parseTaskLine(%q) failed", tt.line)
			continue
		}
		if got.ID != tt.wantID || got.Status != tt.wantSt || got.Description != tt.wantDesc {
			t.Errorf("parseTaskLine(%q) = {%s, %s, %s}, want {%s, %s, %s}",
				tt.line, got.ID, got.Status, got.Description, tt.wantID, tt.wantSt, tt.wantDesc)
		}
	}
}

func TestFormatTaskLine(t *testing.T) {
	tests := []struct {
		task task
		want string
	}{
		{task{"1", "completed", "Test"}, "1. [X] Test"},
		{task{"2.1", "created", "Sub"}, "2.1 [ ] Sub"},
		{task{"3.2.1", "in_progress", "Deep"}, "3.2.1 [_] Deep"},
	}
	for _, tt := range tests {
		got := formatTaskLine(tt.task)
		if got != tt.want {
			t.Errorf("formatTaskLine(%v) = %q, want %q", tt.task, got, tt.want)
		}
	}
}

func TestSortTasks(t *testing.T) {
	tasks := []task{
		{"3", "created", "C"},
		{"2.2", "created", "BB"},
		{"2", "created", "B"},
		{"1", "created", "A"},
		{"2.1", "created", "BA"},
	}
	sortTasks(tasks)
	expected := []string{"1", "2", "2.1", "2.2", "3"}
	for i, tk := range tasks {
		if tk.ID != expected[i] {
			t.Errorf("sortTasks: position %d got %s, want %s", i, tk.ID, expected[i])
		}
	}
}

func TestTasksNextIdAfterDelete(t *testing.T) {
	setupTasksTest(t)
	for _, desc := range []string{"A", "B", "C"} {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "task_create",
				Arguments: map[string]interface{}{
					"description": desc,
				},
			},
		}
		tasksCreateHandler(context.Background(), req)
	}
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_delete",
			Arguments: map[string]interface{}{
				"ID": "2",
			},
		},
	}
	tasksDeleteHandler(context.Background(), req)
	req = mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "task_create",
			Arguments: map[string]interface{}{
				"description": "D",
			},
		},
	}
	result, err := tasksCreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	if !strings.HasPrefix(text, "Created ID: 4\n") {
		t.Errorf("expected 'Created ID: 4' prefix after deleting task 2, got %q", text)
	}
}
