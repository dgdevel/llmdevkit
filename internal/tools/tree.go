package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func TreeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type dirNode struct {
		name     string
		children []*dirNode
		isDir    bool
		size     int64
		absPath  string
	}

	var withFiles bool
	if args, ok := req.Params.Arguments.(map[string]interface{}); ok {
		if wf, ok := args["with_files"].(bool); ok {
			withFiles = wf
		}
	}

	var buildTree func(path string) *dirNode
	buildTree = func(path string) *dirNode {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		node := &dirNode{name: filepath.Base(path)}
		for _, e := range entries {
			entryPath := filepath.Join(path, e.Name())
			if IsIgnored(entryPath) || IsConfigPath(entryPath) {
				continue
			}
			if e.IsDir() {
				child := buildTree(entryPath)
				if child != nil {
					child.isDir = true
					node.children = append(node.children, child)
				}
			} else if withFiles {
				info, _ := e.Info()
				sz := int64(0)
				if info != nil {
					sz = info.Size()
				}
				node.children = append(node.children, &dirNode{name: e.Name(), isDir: false, size: sz, absPath: entryPath})
			}
		}
		return node
	}

	root := buildTree(RootDir)
	if root == nil {
		return mcp.NewToolResultText("/"), nil
	}

	var buf strings.Builder
	buf.WriteString("/")
	buf.WriteByte('\n')

	var render func(children []*dirNode, prefix string)
	render = func(children []*dirNode, prefix string) {
		for i, child := range children {
			isLast := i == len(children)-1
			if child.isDir {
				suffix := child.name + "/"
				if isLast {
					buf.WriteString(prefix + "└── " + suffix + "\n")
				} else {
					buf.WriteString(prefix + "├── " + suffix + "\n")
				}
			} else {
				sizeStr := "?"
				lineStr := "?"
				if child.size > 0 || child.absPath != "" {
					sizeStr = formatEntrySize(child.size)
					if data, err := os.ReadFile(child.absPath); err == nil {
						if isBinary(data) {
							lineStr = "binary"
						} else {
							lineStr = fmt.Sprintf("%d lines", countLines(data))
						}
					}
				}
				label := fmt.Sprintf("%s, %s, %s", sizeStr, lineStr, child.name)
				if isLast {
					buf.WriteString(prefix + "└── " + label + "\n")
				} else {
					buf.WriteString(prefix + "├── " + label + "\n")
				}
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
