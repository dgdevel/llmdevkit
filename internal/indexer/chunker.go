package indexer

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsc "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tscpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type Chunk struct {
	FilePath  string
	Language  string
	ChunkType string
	Signature string
	LineStart int
	LineEnd   int
	Content   string
}

const maxChunkSize = 4000
const minChunkSize = 100

var extToLang = map[string]string{
	".go":    "go",
	".py":    "python",
	".js":    "javascript",
	".jsx":   "javascript",
	".ts":    "typescript",
	".tsx":   "tsx",
	".rs":    "rust",
	".java":  "java",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".cc":    "cpp",
	".cxx":   "cpp",
	".hpp":   "cpp",
	".cs":    "csharp",
	".rb":    "ruby",
	".php":   "php",
	".swift": "swift",
	".kt":    "kotlin",
	".scala": "scala",
}

type nodeConfig struct {
	chunkType string
	bodyKinds []string
	splitKinds []string
}

var langNodeConfigs = map[string]map[string]nodeConfig{
	"go": {
		"function_declaration": {"function", []string{"block"}, nil},
		"method_declaration":   {"method", []string{"block"}, nil},
		"type_declaration":     {"type", []string{"struct_type", "interface_type"}, []string{"field_declaration_list", "method_spec_list"}},
		"func_literal":         {"closure", []string{"block"}, nil},
	},
	"python": {
		"function_definition": {"function", []string{"block"}, []string{"expression_statement", "if_statement", "for_statement", "while_statement", "with_statement", "try_statement", "return_statement"}},
		"class_definition":    {"class", []string{"block"}, []string{"function_definition", "decorated_definition"}},
	},
	"javascript": {
		"function_declaration": {"function", []string{"statement_block"}, nil},
		"class_declaration":    {"class", []string{"class_body"}, []string{"method_definition", "field_definition", "public_field_definition"}},
		"method_definition":    {"method", []string{"statement_block"}, nil},
		"arrow_function":       {"function", []string{"statement_block", "template_string"}, nil},
		"export_statement":     {"export", nil, nil},
	},
	"typescript": {
		"function_declaration": {"function", []string{"statement_block"}, nil},
		"class_declaration":    {"class", []string{"class_body"}, []string{"method_definition", "field_definition", "public_field_definition"}},
		"method_definition":    {"method", []string{"statement_block"}, nil},
		"arrow_function":       {"function", []string{"statement_block"}, nil},
		"interface_declaration": {"interface", []string{"object_type"}, []string{"property_signature", "method_signature", "call_signature"}},
		"type_alias_declaration": {"type", []string{"object_type", "union_type", "intersection_type"}, nil},
		"enum_declaration":     {"enum", []string{"enum_body"}, nil},
		"export_statement":     {"export", nil, nil},
	},
	"tsx": {
		"function_declaration": {"function", []string{"statement_block"}, nil},
		"class_declaration":    {"class", []string{"class_body"}, []string{"method_definition", "field_definition", "public_field_definition"}},
		"method_definition":    {"method", []string{"statement_block"}, nil},
		"arrow_function":       {"function", []string{"statement_block"}, nil},
		"interface_declaration": {"interface", []string{"object_type"}, []string{"property_signature", "method_signature", "call_signature"}},
		"type_alias_declaration": {"type", []string{"object_type", "union_type", "intersection_type"}, nil},
		"enum_declaration":     {"enum", []string{"enum_body"}, nil},
		"export_statement":     {"export", nil, nil},
	},
	"rust": {
		"function_item":   {"function", []string{"block"}, nil},
		"impl_item":       {"impl", []string{"declaration_list"}, []string{"function_item", "associated_type", "const_item"}},
		"struct_item":     {"struct", []string{"field_declaration_list"}, nil},
		"enum_item":       {"enum", []string{"enum_variant_list"}, nil},
		"trait_item":      {"trait", []string{"declaration_list"}, []string{"function_signature_item", "associated_type", "const_item"}},
		"type_alias_item": {"type", nil, nil},
	},
	"java": {
		"class_declaration":         {"class", []string{"class_body"}, []string{"method_declaration", "field_declaration", "constructor_declaration", "static_initializer"}},
		"interface_declaration":     {"interface", []string{"interface_body"}, []string{"method_declaration", "constant_declaration"}},
		"enum_declaration":          {"enum", []string{"enum_body_declarations"}, []string{"enum_constant", "method_declaration", "field_declaration"}},
		"method_declaration":        {"method", []string{"block"}, nil},
		"constructor_declaration":   {"constructor", []string{"block"}, nil},
	},
	"c": {
		"function_definition":  {"function", []string{"compound_statement"}, nil},
		"struct_specifier":     {"struct", []string{"field_declaration_list"}, nil},
		"enum_specifier":       {"enum", []string{"enumerator_list"}, nil},
	},
	"cpp": {
		"function_definition":    {"function", []string{"compound_statement"}, nil},
		"class_specifier":        {"class", []string{"field_declaration_list"}, []string{"function_definition", "field_declaration", "declaration"}},
		"struct_specifier":       {"struct", []string{"field_declaration_list"}, nil},
		"enum_specifier":         {"enum", []string{"enumerator_list"}, nil},
		"namespace_definition":   {"namespace", []string{"declaration_list"}, []string{"function_definition", "class_specifier", "struct_specifier"}},
		"template_declaration":   {"template", nil, nil},
		"lambda_expression":      {"lambda", []string{"compound_statement"}, nil},
	},
}

var (
	tsLangs     map[string]*ts.Language
	tsLangsInit sync.Once
)

func getTSLang(lang string) *ts.Language {
	tsLangsInit.Do(func() {
		tsLangs = map[string]*ts.Language{
			"go":         ts.NewLanguage(unsafe.Pointer(tsgo.Language())),
			"python":     ts.NewLanguage(unsafe.Pointer(tspython.Language())),
			"javascript": ts.NewLanguage(unsafe.Pointer(tsjs.Language())),
			"typescript": ts.NewLanguage(unsafe.Pointer(tstypescript.LanguageTypescript())),
		"tsx":        ts.NewLanguage(unsafe.Pointer(tstypescript.LanguageTSX())),
			"rust":       ts.NewLanguage(unsafe.Pointer(tsrust.Language())),
			"java":       ts.NewLanguage(unsafe.Pointer(tsjava.Language())),
			"c":          ts.NewLanguage(unsafe.Pointer(tsc.Language())),
			"cpp":        ts.NewLanguage(unsafe.Pointer(tscpp.Language())),
		}
	})
	return tsLangs[lang]
}

func DetectLanguage(path string) string {
	ext := filepath.Ext(path)
	if l, ok := extToLang[ext]; ok {
		return l
	}
	return ""
}

func ChunkFile(filePath string, content []byte, lang string) ([]Chunk, error) {
	tsLang := getTSLang(lang)
	if tsLang == nil {
		return nil, nil
	}

	parser := ts.NewParser()
	defer parser.Close()
	parser.SetLanguage(tsLang)

	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil, nil
	}
	defer tree.Close()

	root := tree.RootNode()
	configs := langNodeConfigs[lang]
	if configs == nil {
		return nil, nil
	}

	var chunks []Chunk
	collectChunks(root, content, filePath, lang, configs, &chunks)

	var result []Chunk
	for i := range chunks {
		if utf8.RuneCountInString(chunks[i].Content) <= maxChunkSize {
			result = append(result, chunks[i])
		} else {
			split := splitLargeChunk(content, filePath, lang, configs, chunks[i])
			result = append(result, split...)
		}
	}

	return result, nil
}

func collectChunks(node *ts.Node, source []byte, filePath, lang string, configs map[string]nodeConfig, chunks *[]Chunk) {
	if node == nil {
		return
	}

	kind := node.Kind()
	if cfg, ok := configs[kind]; ok {
		lineStart := int(node.StartPosition().Row) + 1
		lineEnd := int(node.EndPosition().Row) + 1
		text := node.Utf8Text(source)
		sig := extractSignature(node, source, cfg.bodyKinds)

		*chunks = append(*chunks, Chunk{
			FilePath:  filePath,
			Language:  lang,
			ChunkType: cfg.chunkType,
			Signature: strings.TrimSpace(sig),
			LineStart: lineStart,
			LineEnd:   lineEnd,
			Content:   text,
		})

		if cfg.splitKinds != nil {
			return
		}
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		collectChunks(child, source, filePath, lang, configs, chunks)
	}
}

func extractSignature(node *ts.Node, source []byte, bodyKinds []string) string {
	if len(bodyKinds) == 0 {
		text := node.Utf8Text(source)
		lines := strings.SplitN(text, "\n", 3)
		if len(lines) >= 1 {
			return lines[0]
		}
		return text
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		for _, bk := range bodyKinds {
			if child.Kind() == bk {
				sigEnd := int(child.StartByte())
				sig := string(source[node.StartByte():sigEnd])
				sig = strings.TrimSpace(sig)
				sig = strings.TrimRight(sig, "{(")
				return strings.TrimSpace(sig)
			}
		}
	}

	text := node.Utf8Text(source)
	lines := strings.SplitN(text, "\n", 3)
	return lines[0]
}

func splitLargeChunk(source []byte, filePath, lang string, configs map[string]nodeConfig, chunk Chunk) []Chunk {
	tsLang := getTSLang(lang)
	if tsLang == nil {
		return []Chunk{chunk}
	}

	parser := ts.NewParser()
	defer parser.Close()
	parser.SetLanguage(tsLang)

	tree := parser.Parse([]byte(chunk.Content), nil)
	if tree == nil {
		return []Chunk{chunk}
	}
	defer tree.Close()

	root := tree.RootNode()
	cfg, hasCfg := configs[chunk.ChunkType]
	var splitKinds []string
	if hasCfg && cfg.splitKinds != nil {
		splitKinds = cfg.splitKinds
	}

	var parts []Chunk
	currentLineStart := chunk.LineStart

	var namedChildren []*ts.Node
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child != nil {
			namedChildren = append(namedChildren, child)
		}
	}

	if len(namedChildren) == 0 {
		return splitByLines(chunk)
	}

	if len(splitKinds) > 0 {
		for _, child := range namedChildren {
			matched := false
			for _, sk := range splitKinds {
				if child.Kind() == sk {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}

			childStart := int(child.StartPosition().Row)
			childEnd := int(child.EndPosition().Row)

			if currentLineStart < chunk.LineStart+childStart {
				gapContent := extractLineRange(chunk.Content, 0, childStart)
				if strings.TrimSpace(gapContent) != "" && utf8.RuneCountInString(gapContent) >= minChunkSize {
					parts = append(parts, Chunk{
						FilePath:  filePath,
						Language:  lang,
						ChunkType: chunk.ChunkType + "_section",
						Signature: chunk.Signature,
						LineStart: currentLineStart,
						LineEnd:   chunk.LineStart + childStart,
						Content:   gapContent,
					})
				}
			}

			childContent := child.Utf8Text([]byte(chunk.Content))
			childSig := extractSignature(child, []byte(chunk.Content), configs[child.Kind()].bodyKinds)
			if childSig == "" {
				childSig = chunk.Signature
			}

			if utf8.RuneCountInString(childContent) <= maxChunkSize {
				parts = append(parts, Chunk{
					FilePath:  filePath,
					Language:  lang,
					ChunkType: mapChunkType(child.Kind(), lang),
					Signature: strings.TrimSpace(childSig),
					LineStart: chunk.LineStart + childStart,
					LineEnd:   chunk.LineStart + childEnd,
					Content:   childContent,
				})
			} else {
				subChunks := splitLargeChunk(source, filePath, lang, configs, Chunk{
					FilePath:  filePath,
					Language:  lang,
					ChunkType: mapChunkType(child.Kind(), lang),
					Signature: strings.TrimSpace(childSig),
					LineStart: chunk.LineStart + childStart,
					LineEnd:   chunk.LineStart + childEnd,
					Content:   childContent,
				})
				parts = append(parts, subChunks...)
			}

			currentLineStart = chunk.LineStart + childEnd + 1
		}
	} else {
		for _, child := range namedChildren {
			childStart := int(child.StartPosition().Row)
			childEnd := int(child.EndPosition().Row)
			childContent := child.Utf8Text([]byte(chunk.Content))

			sig := extractSignature(child, []byte(chunk.Content), nil)
			if sig == "" {
				sig = chunk.Signature
			}

			if utf8.RuneCountInString(childContent) <= maxChunkSize {
				parts = append(parts, Chunk{
					FilePath:  filePath,
					Language:  lang,
					ChunkType: chunk.ChunkType + "_part",
					Signature: strings.TrimSpace(sig),
					LineStart: chunk.LineStart + childStart,
					LineEnd:   chunk.LineStart + childEnd,
					Content:   chunk.Signature + "\n" + childContent,
				})
			} else {
				parts = append(parts, splitByLines(Chunk{
					FilePath:  filePath,
					Language:  lang,
					ChunkType: chunk.ChunkType + "_part",
					Signature: strings.TrimSpace(sig),
					LineStart: chunk.LineStart + childStart,
					LineEnd:   chunk.LineStart + childEnd,
					Content:   childContent,
				})...)
			}
		}
	}

	if len(parts) == 0 {
		return splitByLines(chunk)
	}

	return parts
}

func splitByLines(chunk Chunk) []Chunk {
	lines := strings.Split(chunk.Content, "\n")
	if len(lines) <= 1 {
		return []Chunk{chunk}
	}

	linesPerChunk := maxChunkSize / 40
	if linesPerChunk < 10 {
		linesPerChunk = 10
	}

	var parts []Chunk
	for i := 0; i < len(lines); i += linesPerChunk {
		end := i + linesPerChunk
		if end > len(lines) {
			end = len(lines)
		}

		part := strings.Join(lines[i:end], "\n")
		if len(strings.TrimSpace(part)) < minChunkSize && i+linesPerChunk < len(lines) {
			continue
		}

		parts = append(parts, Chunk{
			FilePath:  chunk.FilePath,
			Language:  chunk.Language,
			ChunkType: chunk.ChunkType + "_segment",
			Signature: chunk.Signature,
			LineStart: chunk.LineStart + i,
			LineEnd:   chunk.LineStart + end - 1,
			Content:   part,
		})
	}

	if len(parts) == 0 {
		return []Chunk{chunk}
	}

	return parts
}

func extractLineRange(text string, startLine, endLine int) string {
	lines := strings.Split(text, "\n")
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine >= endLine {
		return ""
	}
	return strings.Join(lines[startLine:endLine], "\n")
}

func mapChunkType(kind, lang string) string {
	if configs, ok := langNodeConfigs[lang]; ok {
		if cfg, ok := configs[kind]; ok {
			return cfg.chunkType
		}
	}
	return kind
}

func ShouldIndex(path string) bool {
	lang := DetectLanguage(path)
	return lang != ""
}

func SkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "__pycache__", ".llmdevkit",
		"dist", "build", "target", ".cache", ".tox", ".venv", "venv",
		"env", ".env", ".idea", ".vscode", "coverage", ".next", ".nuxt",
		"out", "bin", "obj", "Debug", "Release", "cmake-build-debug",
		"cmake-build-release", ".gradle", ".dart_tool", "bower_components",
		".cargo", ".rustup", "pkg", "site-packages":
		return true
	}
	return false
}

func FmtChunkID(filePath string, lineStart, lineEnd int) string {
	return fmt.Sprintf("%s:%d:%d", filePath, lineStart, lineEnd)
}
