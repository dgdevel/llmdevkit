package main

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

var tasksMu sync.Mutex

type task struct {
	ID          string
	Status      string
	Description string
}

func tasksFilePath() string {
	return cfg.DirPath(rootDir) + "/tasks.txt"
}

func statusToMarker(status string) (string, error) {
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

func markerToStatus(marker string) string {
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

func parseTaskLine(line string) (task, bool) {
	bracketIdx := strings.Index(line, " [")
	if bracketIdx < 0 {
		return task{}, false
	}
	idRaw := line[:bracketIdx]
	rest := line[bracketIdx+1:]
	id := strings.TrimSuffix(idRaw, ".")
	if len(rest) < 4 {
		return task{}, false
	}
	marker := rest[:3]
	status := markerToStatus(marker)
	if status == "" {
		return task{}, false
	}
	description := ""
	if len(rest) > 4 {
		description = rest[4:]
	}
	return task{ID: id, Status: status, Description: description}, true
}

func parseTasks(data string) []task {
	var tasks []task
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if t, ok := parseTaskLine(line); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func formatTaskLine(t task) string {
	marker, _ := statusToMarker(t.Status)
	if strings.Contains(t.ID, ".") {
		return fmt.Sprintf("%s %s %s", t.ID, marker, t.Description)
	}
	return fmt.Sprintf("%s. %s %s", t.ID, marker, t.Description)
}

func formatTasks(tasks []task) string {
	var buf strings.Builder
	for _, t := range tasks {
		buf.WriteString(formatTaskLine(t))
		buf.WriteByte('\n')
	}
	return buf.String()
}

func writeTasks(tasks []task) error {
	dir := cfg.DirPath(rootDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(tasksFilePath(), []byte(formatTasks(tasks)), 0644)
}

func readTasks() ([]task, error) {
	data, err := os.ReadFile(tasksFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseTasks(string(data)), nil
}

func sortTasks(tasks []task) {
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

func nextTopLevelID(tasks []task) string {
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

func nextChildID(tasks []task, parentID string) (string, error) {
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

func tasksListHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	data, err := os.ReadFile(tasksFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(""), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func tasksCreateHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks, err := readTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var id string
	if parent != "" {
		id, err = nextChildID(tasks, parent)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	} else {
		id = nextTopLevelID(tasks)
	}
	tasks = append(tasks, task{ID: id, Status: "created", Description: description})
	sortTasks(tasks)
	if err := writeTasks(tasks); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Created ID: %s\nCurrent Tasks:\n%s", id, formatTasks(tasks))), nil
}

func tasksSetStatusHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("ID")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := req.RequireString("status")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, err := statusToMarker(status); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks, err := readTasks()
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
		return mcp.NewToolResultText(fmt.Sprintf("Not found\nCurrent Tasks:\n%s", formatTasks(tasks))), nil
	}
	if err := writeTasks(tasks); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("ID: %s set to %s\nCurrent Tasks:\n%s", id, status, formatTasks(tasks))), nil
}

func tasksDeleteHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("ID")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks, err := readTasks()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var filtered []task
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
		return mcp.NewToolResultText(fmt.Sprintf("Not found\nCurrent Tasks:\n%s", formatTasks(tasks))), nil
	}
	if err := writeTasks(filtered); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Done\nCurrent Tasks:\n%s", formatTasks(filtered))), nil
}

func tasksClearHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	os.Remove(tasksFilePath())
	return mcp.NewToolResultText("true"), nil
}
