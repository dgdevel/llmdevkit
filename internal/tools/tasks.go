package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"llmdevkit/internal/cfg"

	"github.com/mark3labs/mcp-go/mcp"
)

var TasksMu sync.Mutex

type Task struct {
	ID          string
	Status      string
	Description string
}

func TasksFilePath() string {
	return cfg.DirPath(RootDir) + "/tasks.txt"
}

func StatusToMarker(status string) (string, error) {
	switch status {
	case "created":
		return "[ ]", nil
	case "in_progress":
		return "[_]", nil
	case "completed":
		return "[X]", nil
	default:
		return "", fmt.Errorf("invalid status: %s", status)
	}
}

func MarkerToStatus(marker string) string {
	switch marker {
	case "[ ]":
		return "created"
	case "[_]":
		return "in_progress"
	case "[X]":
		return "completed"
	default:
		return ""
	}
}

func ParseTaskLine(line string) (Task, bool) {
	bracketIdx := strings.Index(line, " [")
	if bracketIdx < 0 {
		return Task{}, false
	}
	idRaw := line[:bracketIdx]
	rest := line[bracketIdx+1:]
	id := strings.TrimSuffix(idRaw, ".")
	if len(rest) < 4 {
		return Task{}, false
	}
	marker := rest[:3]
	status := MarkerToStatus(marker)
	if status == "" {
		return Task{}, false
	}
	description := ""
	if len(rest) > 4 {
		description = rest[4:]
	}
	return Task{ID: id, Status: status, Description: description}, true
}

func ParseTasks(data string) []Task {
	var tasks []Task
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if t, ok := ParseTaskLine(line); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func FormatTaskLine(t Task) string {
	marker, _ := StatusToMarker(t.Status)
	if strings.Contains(t.ID, ".") {
		return fmt.Sprintf("%s %s %s", t.ID, marker, t.Description)
	}
	return fmt.Sprintf("%s. %s %s", t.ID, marker, t.Description)
}

func FormatTasks(tasks []Task) string {
	var buf strings.Builder
	for _, t := range tasks {
		buf.WriteString(FormatTaskLine(t))
		buf.WriteByte('\n')
	}
	return buf.String()
}

func WriteTasks(tasks []Task) error {
	dir := cfg.DirPath(RootDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(TasksFilePath(), []byte(FormatTasks(tasks)), 0644)
}

func ReadTasks() ([]Task, error) {
	data, err := os.ReadFile(TasksFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseTasks(string(data)), nil
}

func SortTasks(tasks []Task) {
	sort.Slice(tasks, func(i, j int) bool {
		pi := idParts(tasks[i].ID)
		pj := idParts(tasks[j].ID)
		for k := 0; k < len(pi) && k < len(pj); k++ {
			if pi[k] != pj[k] {
				return pi[k] < pj[k]
			}
		}
		return len(pi) < len(pj)
	})
}

func idParts(id string) []int {
	parts := strings.Split(id, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i], _ = strconv.Atoi(p)
	}
	return nums
}

func NextTopLevelID(tasks []Task) string {
	maxID := 0
	for _, t := range tasks {
		if !strings.Contains(t.ID, ".") {
			n, _ := strconv.Atoi(t.ID)
			if n > maxID {
				maxID = n
			}
		}
	}
	return strconv.Itoa(maxID + 1)
}

func NextChildID(tasks []Task, parentID string) (string, error) {
	found := false
	maxChild := 0
	prefix := parentID + "."
	for _, t := range tasks {
		if t.ID == parentID {
			found = true
		}
		if strings.HasPrefix(t.ID, prefix) {
			rest := t.ID[len(prefix):]
			if !strings.Contains(rest, ".") {
				n, _ := strconv.Atoi(rest)
				if n > maxChild {
					maxChild = n
				}
			}
		}
	}
	if !found {
		return "", fmt.Errorf("parent task %s not found", parentID)
	}
	return fmt.Sprintf("%s.%d", parentID, maxChild+1), nil
}

func TasksListHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	TasksMu.Lock()
	defer TasksMu.Unlock()
	data, err := os.ReadFile(TasksFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(""), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func TasksCreateHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	description, err := req.RequireString("description")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	parent := ""
	if args, ok := req.Params.Arguments.(map[string]interface{}); ok {
		if s, ok := args["parent"].(string); ok && s != "" {
			parent = s
		}
	}
	TasksMu.Lock()
	defer TasksMu.Unlock()
	tasks, err := ReadTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var id string
	if parent != "" {
		id, err = NextChildID(tasks, parent)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	} else {
		id = NextTopLevelID(tasks)
	}
	tasks = append(tasks, Task{ID: id, Status: "created", Description: description})
	SortTasks(tasks)
	if err := WriteTasks(tasks); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Created ID: %s\nCurrent Tasks:\n%s", id, FormatTasks(tasks))), nil
}

func TasksSetStatusHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("ID")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := req.RequireString("status")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, err := StatusToMarker(status); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	TasksMu.Lock()
	defer TasksMu.Unlock()
	tasks, err := ReadTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	found := false
	for i, t := range tasks {
		if t.ID == id {
			tasks[i].Status = status
			found = true
			break
		}
	}
	if !found {
		return mcp.NewToolResultText(fmt.Sprintf("Not found\nCurrent Tasks:\n%s", FormatTasks(tasks))), nil
	}
	if err := WriteTasks(tasks); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("ID: %s set to %s\nCurrent Tasks:\n%s", id, status, FormatTasks(tasks))), nil
}

func TasksDeleteHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("ID")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	TasksMu.Lock()
	defer TasksMu.Unlock()
	tasks, err := ReadTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var filtered []Task
	prefix := id + "."
	found := false
	for _, t := range tasks {
		if t.ID == id || strings.HasPrefix(t.ID, prefix) {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		return mcp.NewToolResultText(fmt.Sprintf("Not found\nCurrent Tasks:\n%s", FormatTasks(tasks))), nil
	}
	if err := WriteTasks(filtered); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Done\nCurrent Tasks:\n%s", FormatTasks(filtered))), nil
}

func TasksClearHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	TasksMu.Lock()
	defer TasksMu.Unlock()
	os.Remove(TasksFilePath())
	return mcp.NewToolResultText("true"), nil
}
