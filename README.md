# llmdevkit

A minimal MCP server exposing Unix-inspired file tools. Designed for low token usage and sandboxed file access.

## Usage

```
./llmdevkit-mcp [--stdio|--http] [--address host:port] [--ignore pattern] [--show tools] [--hide tools] [--enable-indexer] [--enable-memory] [rootdirectory]
```

All paths are virtual — `/` maps to the root directory. Path traversal is blocked.

- Default transport is stdio.
- `--http` starts a streamable HTTP server on the given `--address` (default `localhost:8080`).
- `--ignore` accepts a comma-separated list of glob patterns. Each path component (file or directory name) is matched against every pattern. Files and directories matching any pattern are hidden from all tools. Traversal tools (`ls`, `grep`, `sed`) skip entire matched directories. Examples: `--ignore '.*'` hides all dotfiles/dirs at any depth, `--ignore '.*,node_modules'` hides both dotfiles and `node_modules`.
- `--show` accepts a comma-separated list of tool names to expose (whitelist). Only the listed tools are available. Mutually exclusive with `--hide`. Proxied tools (from `mcps.yml`) are always included regardless of this flag.
- `--hide` accepts a comma-separated list of tool names to hide (blacklist). All other tools remain available. Mutually exclusive with `--show`. Proxied tools are always included regardless of this flag.
- If no root directory is given, the current working directory is used.
- `--enable-indexer` starts the code indexer subsystem (see Code Indexer section).
- `--enable-memory` starts the memory subsystem for fact storage and retrieval (see Memory section).

## Tools

### `file_create` — Create a file

| Argument | Description |
|----------|-------------|
| `path` | File path |
| `content` | File content |

Creates a new file. Errors if the file already exists.

### `ls` — List directory content

| Argument | Description |
|----------|-------------|
| `pathspec` | Glob expression for file names |

Recursively walks the root matching the glob pattern. Supports `*` and `**` (globstar) syntax. Directories end with `/`.

### `file_read` — Read file content

| Argument | Description |
|----------|-------------|
| `path` | File to read |
| `line_range` | Line range, 1-indexed. Formats: `from:to`, `from-to`, `[from:to]`, `[from-to]`. Also `:to`, `from:`, or a single number for that line to end |

Reads a file and outputs the raw content in blocks, with no transformation (no line numbers, no tab/trailing-space visualization). Output is split into blocks of 100 lines (configurable via `core.file_read_block_size`). Each block is preceded by a header:

```
----- $path - lines from X to Y -----
```

At the end, a footer is emitted. If the portion emitted reaches the end of the file, an EOF marker is shown:

```
----- $path - EOF -----
```

If the portion does not go to the end of the file, the remaining line range is shown instead:

```
----- $path - remaining lines from X to Y -----
```

### `mv` — Move files

| Argument | Description |
|----------|-------------|
| `source` | File path |
| `dest` | File path |

Moves a file or directory. Fails if destination already exists or source not found.

### `grep` — Print lines matching pattern with context

| Argument | Description |
|----------|-------------|
| `pattern` | Regular expression |
| `pathspec` | Glob expression for file names |

Output uses `file_read`-style block headers (`----- /path - lines from X to Y -----`). Shows 1 context line before and after each match. Non-adjacent match groups are separated by blank lines. Supports `**` globstar. Line numbers in headers are 1-indexed. Output is limited to 500 content lines.

### `sed` — Search and replace in files

| Argument | Description |
|----------|-------------|
| `pattern` | Regular expression |
| `replacement` | Replacement string |
| `pathspec` | Glob expression for file names |

In-place match and replace (no capturing groups). Returns list of changed files. Supports `**` globstar.

### `edit` — Edit a file by replacing a block of text

| Argument | Description |
|----------|-------------|
| `path` | File path |
| `start_line_number` | The line number where `original_window` begins (1-indexed) |
| `original_window` | Block of text to be replaced |
| `modified_window` | Block of text to be inserted |

Searches for `original_window` in the file at `path`. Applies the edit only if `start_line_number` is exact or off by at most 5 lines (and there's a single match within that range). Possible responses:

- `ok`
- `ok, start_line_number was wrong, it was X instead`
- `ko: no match`
- `ko: X matches found, ensure start_line_number is right`

### `rm` — Delete a file or a directory

| Argument | Description |
|----------|-------------|
| `path` | Path to delete |

Recursive delete (`rm -rf`). Returns ok for nonexistent paths.

### `stat` — Various info on files and directories

| Argument | Description |
|----------|-------------|
| `path` | File or directory path |

Returns:

```
Type: [file|directory]
Size: [bytes], [human readable]
Permissions: [read|write|execute]
Owner: [username](uid=[uid])
Group: [groupname](gid=[gid])
Access: [ISO8601 timestamp]
Modify: [ISO8601 timestamp]
Change: [ISO8601 timestamp]
Birth: [ISO8601 timestamp]
```

Birth time uses `statx` when available, falls back to change time otherwise. Permissions are relative to the current user.

### `w3m-dump` — Fetch a webpage text

| Argument | Description |
|----------|-------------|
| `url` | URL to fetch |

Fetches a webpage, extracts the main readable content using Mozilla Readability, and converts it to Markdown. Only `http` and `https` URLs are supported. Connect timeout is 5 seconds, read timeout is 20 seconds. Response body is limited to 5 MB. If the resulting Markdown exceeds 200 KB, it is truncated and prefixed with `# PAGE TOO LONG - PARTIAL OUTPUT`.

### `online_search` — Search topic online

| Argument | Description |
|----------|-------------|
| `search_query` | Search query string |

Searches DuckDuckGo and returns results with title, URL, and description. Returns `No results found` if nothing matches. Example output:

```
Title: This is a page
Url: http://....
Description:
The page is about being
a page. The description text can be
multiline.

Title: This is another page
Url: http://....
Description: A short description
```

### `available_commands` — List available commands

No arguments.

Lists all user-defined commands from the configuration, including their descriptions and expected arguments. Example output:

```
Command: build
Arguments: target

Command: test
Arguments: no arguments are taken, invoke without arguments
Description: Run tests

Command: run
Description: Run the main executable; target_folder is the directory to work with, config_file is the reference configuration to use.
```

### `run_command` — Run the command

| Argument | Required | Description |
|----------|----------|-------------|
| `name` | Yes | Name of the command to run |
| `arguments` | No | Array of strings to pass to the command line |
| `timeout` | Yes | Timeout in seconds |

Validates the command name and argument count against the configuration, sanitizes input, and executes the command. Arguments are only accepted when the command defines an `arguments` list; passing arguments to a command that takes none is an error. Stdout and stderr are merged and returned untouched. On timeout, the process is sent SIGTERM, then SIGKILL after 5 seconds. If a timeout occurs, the output is prefixed with `Command timed out. Partial output.`.

For example, with `build_cmdline=make` and `build_arguments=target`, calling `run_command` with `name="build"` and `arguments=["clean"]` executes `make clean`.

### `examples` — Show usage examples for a tool

| Argument | Description |
|----------|-------------|
| `tool_name` | Name of the tool to get examples for |

Returns at least 3 examples of request/response pairs for the given tool. If the tool name is unknown, returns an error listing all available tool names.

## Task Management

Tasks are stored in `[root]/.llmdevkit/tasks.txt`. Each task has a system-assigned hierarchical ID, a status, and a description.

Status markers:

| Status | Marker |
|--------|--------|
| `created` | `[ ]` |
| `in_progress` | `[_]` |
| `completed` | `[X]` |

Example file content:

```
1. [X] Design the API
2. [_] Implement features
2.1 [X] Add config loading
2.2 [ ] Add error handling
3. [ ] Write documentation
```

### `tasks_list` — List all tasks

No arguments.

Returns the content of the tasks file.

### `task_create` — Append a task to the task list

| Argument | Required | Description |
|----------|----------|-------------|
| `description` | Yes | Task description |
| `parent` | No | ID of the parent task |

Returns the assigned ID and the current task list:

```
Created ID: 3
Current Tasks:
1. [X] Design the API
2. [_] Implement features
3. [ ] New task
```

When `parent` is provided, the new task becomes a child (e.g. `parent="2"` → new ID `2.1`).

### `task_set_status` — Change status of a task

| Argument | Description |
|----------|-------------|
| `ID` | Task ID |
| `status` | One of: `created`, `in_progress`, `completed` |

Returns the updated status and the current task list:

```
ID: 2 set to completed
Current Tasks:
1. [X] Design the API
2. [X] Implement features
```

If the ID is not found, returns `Not found` followed by the current task list.

### `task_delete` — Delete a task

| Argument | Description |
|----------|-------------|
| `ID` | Task ID |

Deletes the task and all its children. Returns `Done` or `Not found` followed by the current task list:

```
Done
Current Tasks:
1. [X] Design the API
3. [ ] Write documentation
```

### `tasks_clear` — Clear all tasks

No arguments.

## Configuration

`llmdevkit` reads configuration from two locations, merged with local overriding global:

1. **Global**: `$XDG_CONFIG_HOME/llmdevkit/config.ini` (or `$HOME/.config/llmdevkit/config.ini` if `XDG_CONFIG_HOME` is unset)
2. **Local**: `[root]/.llmdevkit/config.ini`

Both the global and local `.llmdevkit` directories are invisible to all MCP tools — they cannot be listed, read, created, edited, or deleted through the server.

The configuration is re-read on every request, so changes take effect without restarting the server.

### `llmdevkit-config` — Manage the configuration file

```
./llmdevkit-config [--global] <get|set> <namespace.key> [value]
./llmdevkit-config <root> <get|set> <namespace.key> [value]
```

With `--global`, operations target the global configuration file instead of the local one. The `--global` flag cannot be combined with a root directory argument.

Examples:

```
./llmdevkit-config set core.readonly true
./llmdevkit-config --global set core.readonly yes
./llmdevkit-config get core.readonly
./llmdevkit-config /path/to/project set core.readonly yes
```

### `core.readonly`

When set to `true` (or `1` / `yes`), the write tools are hidden from the server:

- `file_create`
- `sed`
- `edit`
- `rm`
- `mv`

### `core.file_read_block_size`

Block size (number of lines) for the `file_read` tool. Default is `100`.

### `commands` — User-defined commands

The `commands` section lets you define named commands that can be listed and executed through the `available_commands` and `run_command` tools. Each command requires a `cmdline` and can optionally have a `description` and an `arguments` list.

| Key | Required | Description |
|-----|----------|-------------|
| `commands.list` | Yes | Comma-separated list of command names |
| `commands.<name>_cmdline` | Yes | The command line to execute |
| `commands.<name>_description` | No | Human-readable description of the command |
| `commands.<name>_arguments` | No | Comma-separated list of argument names the command accepts |

Example configuration:

```
./llmdevkit-config set commands.list build,test,run
./llmdevkit-config set commands.build_cmdline "make"
./llmdevkit-config set commands.build_arguments "target"
./llmdevkit-config set commands.test_cmdline "make test"
./llmdevkit-config set commands.test_description "Run tests"
```

### `mcps` — Upstream MCP server proxying

`llmdevkit` can proxy tools from upstream MCP servers, making them available as if they were built-in. Configuration is loaded from both global (`$XDG_CONFIG_HOME/llmdevkit/mcps.yml`) and local (`[root]/.llmdevkit/mcps.yml`), merged with local overriding global.

```yaml
mcps:
  myserver:
    url: http://localhost:9001/mcp
    headers:
      Authorization: Bearer token123
    tools:
      search:
        rename: my_search
        description: Search my database
        arguments:
          query:
            description: The search query
      get_item:
        keep_as_is: true
```

| Field | Required | Description |
|-------|----------|-------------|
| `mcps.<name>.url` | Yes | URL of the upstream MCP server (streamable HTTP transport) |
| `mcps.<name>.headers` | No | HTTP headers to send with each request |
| `mcps.<name>.tools` | Yes | Map of upstream tool names to their configuration |

For each tool entry:

| Field | Required | Description |
|-------|----------|-------------|
| `rename` | No | New name for the proxied tool |
| `description` | No | Override the tool description |
| `arguments` | No | Map of argument names to `{rename, description}` overrides |
| `keep_as_is` | No | If `true`, pass the tool through unchanged (no rename/description overrides) |

Only tools explicitly listed in the `tools` map are proxied. Other tools from the upstream server are ignored.

Proxied tools are excluded from `--show`/`--hide` filtering — they are always visible.

## Code Indexer

llmdevkit includes an optional code indexer that provides semantic code search using local embedding and reranking models powered by llama.cpp. It runs as a child process of the main llmdevkit server.

### Setup

```
./llmdevkit-setup [--global] [rootdirectory]
```

With `--global`, llama.cpp binaries and models are stored in the global config directory (`$XDG_CONFIG_HOME/llmdevkit/`), and the `[llama]` configuration is written there. This is recommended so that all projects share the same binaries and models. A root directory cannot be specified when using `--global`.

Downloads llama.cpp (CPU-only x86_64), an embedding model and a reranking model, then writes the configuration to the config file.

The index storage (vector database) is always local to each project at `[root]/.llmdevkit/index/`, since it is project-specific.

### First-time indexing

The initial index can take several minutes depending on project size. It is recommended to warm up the index before launching the MCP server:

```
echo "reindex" | ./llmdevkit-indexer [rootdirectory]
```

Wait for the `ok` response, then start the MCP server with `--enable-indexer`. Subsequent startups will only index changed files (incremental via content hash tracking).

### `relevant_code` — Semantic code search

| Argument | Description |
|----------|-------------|
| `prompt` | Description of the code you are looking for |

Requires `--enable-indexer`. Returns results as blocks separated by an empty line:

```
Signature: func myFunction(x int) error
File: pkg/handler.go
Line Range: 42-58
Language: go
Type: function

Signature: type MyStruct struct
File: pkg/types.go
Line Range: 10-15
Language: go
Type: type
```

Use `file_read` with the reported line range to read the actual code. Returns an empty string if the indexer is not ready or no results are found.

### `search_symbol_in_code` — Search symbol in indexed signatures

| Argument | Description |
|----------|-------------|
| `symbol_name` | Symbol name to search for (substring match) |

Requires `--enable-indexer`. Searches all indexed code signatures for the given symbol name (substring match). Returns results in the same block format as `relevant_code`. Returns an empty string if the indexer is not ready or no results are found.

### Configuration

The indexer reads the `[llama]` section from the merged global+local configuration:

| Key | Description |
|-----|-------------|
| `llama.path` | Path to `llama-server` binary (may include extra flags) |
| `llama.embedder` | HuggingFace repo ID for the embedding model |
| `llama.reranker` | HuggingFace repo ID for the reranking model (not required when `llama.reranker_enabled` is `false`) |
| `llama.search_count` | Number of documents retrieved from the vector database (default: `50`) |
| `llama.result_count` | Number of final results returned after reranking (default: `10`) |
| `llama.reranker_enabled` | Set to `false`, `0`, `no`, `disabled`, or `off` to skip the reranker entirely. When disabled, results are scored by vector similarity only and the reranker server is not started (default: `true`) |

## Memory

llmdevkit includes an optional memory subsystem that provides persistent fact storage and semantic retrieval using the same llama.cpp embedding infrastructure as the code indexer. Facts are embedded, stored in a vector database, and can be recalled by semantic similarity.

### Setup

The memory subsystem requires the `[llama]` configuration section (shared with the code indexer). Run the indexer setup first:

```
./llmdevkit-setup [--global] [rootdirectory]
```

Then start the MCP server with `--enable-memory`:

```
./llmdevkit-mcp --enable-memory [rootdirectory]
```

The memory store is persisted at `[root]/.llmdevkit/memory/` and survives server restarts.

### `memory_put` — Store a fact

| Argument | Description |
|----------|-------------|
| `fact` | Fact phrase to memorize |

Requires `--enable-memory`. Embeds the fact and stores it in the vector database. Duplicate facts (identical text) are deduplicated — re-adding the same fact overwrites the previous entry. Each fact tracks:

- `created_at` — timestamp of first insertion
- `last_used` — timestamp of last retrieval
- `recall_count` — number of times the fact was retrieved

Returns the total number of stored facts.

### `relevant_memory` — Search relevant facts

| Argument | Description |
|----------|-------------|
| `prompt` | Query to find relevant memories |

Requires `--enable-memory`. Embeds the prompt, searches the vector database for semantically similar facts, and returns up to 10 results ranked by similarity. Each retrieval updates `last_used` and increments `recall_count` for the matched facts.

Each result is a JSON object:

```json
{
  "id": "sha256 hex prefix",
  "fact": "the original fact text",
  "created_at": "2025-01-15T10:30:00Z",
  "last_used": "2025-01-16T14:20:00Z",
  "recall_count": 3,
  "score": 0.92
}
```

Returns `No relevant memories found.` if the memory store is empty or no matches exist.

### `memory_extract` — Extract facts from text

| Argument | Description |
|----------|-------------|
| `text` | Text to extract facts from (conversation, notes, document) |

Requires `--enable-memory` and a configured extractor model. Sends the text to a small local LLM (default: Qwen3.5-0.8B Q4) which extracts discrete factual statements. For each extracted fact:

1. The vector database is checked for similar existing facts (cosine similarity > 0.85)
2. Near-duplicates (similarity > 0.93) are skipped
3. Rephrases of existing facts (verified by the LLM) are skipped
4. New or refined facts are stored

Each result is a JSON object with an `action` field:

```json
{"fact": "extracted fact text", "action": "stored"}
{"fact": "extracted fact text", "action": "stored_refined", "similar": "existing similar fact"}
{"fact": "already known fact", "action": "skipped_duplicate", "similar": "existing fact"}
{"fact": "rephrased fact", "action": "skipped_rephrase", "similar": "existing fact"}
```

Returns `No extractable facts found in the text.` if nothing could be extracted.

### Configuration

The memory subsystem reads the `[llama]` section from the merged configuration:

| Key | Description |
|-----|-------------|
| `llama.path` | Path to `llama-server` binary (may include extra flags) |
| `llama.embedder` | HuggingFace repo ID for the embedding model |
| `llama.embedder_flags` | Extra flags for the embedder llama-server instance (e.g. `--ctx-size 4096`) |
| `llama.extractor` | HuggingFace repo ID for the chat model used for fact extraction (default: `unsloth/Qwen3.5-0.8B-GGUF`) |
| `llama.extractor_flags` | Extra flags for the extractor llama-server instance (e.g. `--temp 0`) |
