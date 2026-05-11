# llmdevkit

A toolkit for building and running AI-powered developer agents. Provides an MCP server with Unix-inspired file tools, a code indexer, and an ACP (Agent Client Protocol) server that orchestrates LLMs with tool-calling capabilities.

The project is a merge of my previous two projects [nixdevkit](/dgdevel/nixdevkit) and [lite-dev-agent](/dgdevel/lite-dev-agent).

## Executables

| Binary | Description |
|--------|-------------|
| `llmdevkit-config` | Manage global and local configuration files |
| `llmdevkit-setup` | Download and configure llama.cpp for embedding/reranking |
| `llmdevkit-indexer` | Build and query the code index |
| `llmdevkit-mcp` | MCP server — file tools, task management, command runner, code search, memory |
| `llmdevkit-acp` | ACP server — agent harness with LLM orchestration, tool routing, and sub-agent invocation |

Configuration is stored in `.llmdevkit/` (local, per-project) and `$XDG_CONFIG_HOME/llmdevkit/` (global), merged with local overriding global. Both directories are invisible to all MCP tools.

- [llmdevkit-config](#llmdevkit-config)
- [llmdevkit-setup](#llmdevkit-setup)
- [llmdevkit-indexer](#llmdevkit-indexer)
- [llmdevkit-mcp](#llmdevkit-mcp)
- [llmdevkit-acp](#llmdevkit-acp)

---

## llmdevkit-config

Manage the configuration file.

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

---

## llmdevkit-setup

Download and configure llama.cpp for embedding and reranking models.

```
./llmdevkit-setup [--global] [rootdirectory]
```

With `--global`, llama.cpp binaries and models are stored in the global config directory (`$XDG_CONFIG_HOME/llmdevkit/`), and the `[llama]` configuration is written there. This is recommended so that all projects share the same binaries and models. A root directory cannot be specified when using `--global`.

Downloads llama.cpp (CPU-only x86_64), an embedding model and a reranking model, then writes the configuration to the config file.

The index storage (vector database) is always local to each project at `[root]/.llmdevkit/index/`, since it is project-specific.

### Configuration

| Key | Description |
|-----|-------------|
| `llama.path` | Path to `llama-server` binary (may include extra flags) |
| `llama.embedder` | HuggingFace repo ID for the embedding model |
| `llama.embedder_flags` | Extra flags for the embedder llama-server instance (e.g. `--ctx-size 4096`) |
| `llama.reranker` | HuggingFace repo ID for the reranking model (not required when `llama.reranker_enabled` is `false`) |
| `llama.search_count` | Number of documents retrieved from the vector database (default: `50`) |
| `llama.result_count` | Number of final results returned after reranking (default: `10`) |
| `llama.reranker_enabled` | Set to `false`, `0`, `no`, `disabled`, or `off` to skip the reranker entirely (default: `true`) |
| `llama.extractor` | HuggingFace repo ID for the chat model used for fact extraction (default: `unsloth/Qwen3.5-0.8B-GGUF`) |
| `llama.extractor_flags` | Extra flags for the extractor llama-server instance (e.g. `--temp 0`) |

---

## llmdevkit-indexer

Build and query the code index. The initial index can take several minutes depending on project size.

```
echo "reindex" | ./llmdevkit-indexer [rootdirectory]
```

Wait for the `ok` response, then start the MCP server with `--enable-indexer`. Subsequent startups will only index changed files (incremental via content hash tracking).

---

## llmdevkit-mcp

MCP server exposing Unix-inspired file tools, task management, command runner, code search, and memory. Designed for low token usage and sandboxed file access.

### Usage

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

### `mcps` — Upstream MCP server proxying

`llmdevkit-mcp` can proxy tools from upstream MCP servers, making them available as if they were built-in. Configuration is loaded from both global (`$XDG_CONFIG_HOME/llmdevkit/mcps.yml`) and local (`[root]/.llmdevkit/mcps.yml`), merged with local overriding global.

```yaml
mcps:
  myserver:
    url: http://localhost:9001/mcp          # streamable HTTP (alternative to sse and stdio)
    # sse: http://localhost:9001/mcp/sse    # SSE transport (alternative to url and stdio)
    # stdio: "./my-executable --flag"       # stdio transport (alternative to url and sse)
    headers:
      Authorization: Bearer token123
    prefix: "my_"                            # prefix added to tool names not in tools map
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
| `mcps.<name>.url` | One of `url`, `sse`, `stdio` | URL of the upstream MCP server (streamable HTTP transport) |
| `mcps.<name>.sse` | One of `url`, `sse`, `stdio` | URL of the upstream MCP server (SSE transport) |
| `mcps.<name>.stdio` | One of `url`, `sse`, `stdio` | Command line for stdio transport (parsed with shell-style splitting) |
| `mcps.<name>.headers` | No | HTTP headers to send with each request (or env vars for stdio) |
| `mcps.<name>.prefix` | No | Prefix added to tool names not explicitly listed in the `tools` map |
| `mcps.<name>.tools` | No | Map of upstream tool names to their configuration |

For each tool entry:

| Field | Required | Description |
|-------|----------|-------------|
| `rename` | No | New name for the proxied tool |
| `description` | No | Override the tool description |
| `arguments` | No | Map of argument names to `{rename, description}` overrides |
| `keep_as_is` | No | If `true`, pass the tool through unchanged (no rename/description overrides) |

When `tools` is omitted, all upstream tools are proxied. When present, only listed tools are proxied. Proxied tools are excluded from `--show`/`--hide` filtering — they are always visible.

### Code Indexer

Requires `--enable-indexer`. Provides `relevant_code` (semantic code search) and `search_symbol_in_code` (symbol substring search). Powered by llama.cpp embedding and reranking models. See [llmdevkit-setup](#llmdevkit-setup) for configuration.

### Memory

Requires `--enable-memory`. Provides `memory_put` (store a fact), `relevant_memory` (semantic fact retrieval), and `memory_extract` (LLM-powered fact extraction from text). Uses the same llama.cpp infrastructure as the code indexer.

---

## llmdevkit-acp

ACP (Agent Client Protocol) server that orchestrates LLMs with tool-calling capabilities. Implements a fully compliant [ACP agent](https://agentclientprotocol.com/) using stdio transport.

Configuration is loaded from `.llmdevkit/` (local) and `$XDG_CONFIG_HOME/llmdevkit/` (global), merged with local overriding global. Three YAML files define the agent behavior:

### `llms.yml` — LLM endpoints

List of OpenAI-compatible endpoints.

```yaml
llms:
  - name: main
    api_base: http://1.2.3.4/v1
    model: my-model        # optional
    api_key: sk-xxx        # optional
    headers:               # optional
      Authorization: Bearer sk-xxx
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Identifier used in `agents.yml` to reference this LLM |
| `api_base` | Yes | OpenAI-compatible API base URL |
| `model` | No | Model name override |
| `api_key` | No | API key (also sent as `Authorization: Bearer <key>` header) |
| `headers` | No | Additional HTTP headers merged into every request |

### `mcps.yml` — MCP tool servers

Same format as [llmdevkit-mcp upstream proxying](#mcps--upstream-mcp-server-proxying). Defines MCP servers whose tools are available to agents. Supports `url` (streamable HTTP), `sse`, and `stdio` transports.

### `agents.yml` — Agent definitions

Each agent binds an LLM, a set of tools, a system prompt, and lifecycle hooks.

```yaml
agents:
  - name: myagent
    llm: main                     # name from llms.yml
    tools: myserver devkit agents # space-separated tool sources
    system_prompt: You are a helpful assistant
    hooks:
      on_conversation_begin:      # fired once when conversation starts
        my_tool:
          argname: "%p"           # %p is replaced with the user prompt
      on_turn_begin:              # fired before each LLM turn
        another_tool:
          argname: fixed-value
      on_turn_end:                # fired after each LLM turn
      on_conversation_end:        # fired when conversation ends
```

#### Tool sources

The `tools` field is a space-separated list. Each token can be:

| Token | Description |
|-------|-------------|
| A name from `mcps.yml` | All tools from that MCP server are attached |
| `devkit` | In-process `llmdevkit-mcp` instance (file tools, tasks, commands, etc.) |
| `agents` | Sub-agent invocation tools: `agents_available` (list agents) and `agent_invoke` (run agent with prompt). Each sub-agent invocation uses a fresh context — no conversation sharing. |

#### Hooks

Hooks are automatic tool calls at specific lifecycle points. Each hook maps tool names to argument overrides. The special `%p` placeholder is replaced with the current user prompt text.

### Usage

```
./llmdevkit-acp
```

Communicates over stdio using the ACP JSON-RPC protocol. Designed to be launched by an ACP-compatible client.
