package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func TreeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type dirNode struct {
		name     string
		children []*dirNode
	}

	var buildTree func(path string) *dirNode
	buildTree = func(path string) *dirNode {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		node := &dirNode{name: filepath.Base(path)}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			childPath := filepath.Join(path, e.Name())
			if IsIgnored(childPath) || IsConfigPath(childPath) {
				continue
			}
			child := buildTree(childPath)
			if child != nil {
				node.children = append(node.children, child)
			}
		}
		return node
	}

	root := buildTree(RootDir)
	if root == nil {
		return mcp.NewToolResultText(RootDir), nil
	}

	var buf strings.Builder
	buf.WriteString(RootDir)
	buf.WriteByte('\n')

	var render func(children []*dirNode, prefix string)
	render = func(children []*dirNode, prefix string) {
		for i, child := range children {
			isLast := i == len(children)-1
			if isLast {
				buf.WriteString(prefix + "└── " + child.name + "\n")
			} else {
				buf.WriteString(prefix + "├── " + child.name + "\n")
			}
			nextPrefix := prefix + "│   "
			if isLast {
				nextPrefix = prefix + "    "
			}
			render(child.children, nextPrefix)
		}
	}

	render(root.children, "")

	return mcp.NewToolResultText(strings.TrimRight(buf.String(), "\n")), nil
}
