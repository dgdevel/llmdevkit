# llmdevkit

A single executable toolkit for building and running LLM-powered agents.  
Provides an MCP server with Unix-inspired file tools, a code indexer (optional), and an ACP server that orchestrates LLMs with tool-calling capabilities and a web UI to use the agents directly.  
While extensible as a general toolkit, its core use case is a lean coding agent (see [examples/dev-solo](https://github.com/dgdevel/llmdevkit/tree/main/examples/dev-solo)) optimized for minimal token usage.  
Run and tested on Linux it should work on any \*nix.

<a href="https://github.com/dgdevel/llmdevkit/releases/tag/v1.2.0" target="_blank">A screenshot for v1.2.0 here</a>

[Feature list here](https://github.com/dgdevel/llmdevkit#llmdevkit-server)


The project is a merge of my previous two projects [nixdevkit (archived)](https://github.com/dgdevel/nixdevkit) and [lite-dev-agent (archived)](https://github.com/dgdevel/lite-dev-agent).

## Quickstart

### Build

`make`

### Install

Copy the `llmdevkit` executable in your $PATH (there's a binary release for x86\_64 linux [here](https://github.com/dgdevel/llmdevkit/releases)).

### Usage

Follow instructions at [examples/dev-solo](https://github.com/dgdevel/llmdevkit/tree/main/examples/dev-solo) to setup the first agent.

## Commands

| Command | Description |
|----------|-------------|
| `llmdevkit config` | Manage global and local configuration files |
| `llmdevkit setup` | Download and configure llama.cpp for embedding/reranking |
| `llmdevkit indexer` | Build and query the code index |
| `llmdevkit mcp` | MCP server -- file tools, task management, command runner, code search, memory |
| `llmdevkit acp` | ACP server -- agent harness with LLM orchestration, tool routing, and sub-agent invocation |
| `llmdevkit server` | Web UI server -- chat interface for agents with real-time streaming, conversation persistence, human-in-the-loop tools, and browser notifications |

Configuration is stored in `.llmdevkit/` (local, per-project) and `$XDG_CONFIG_HOME/llmdevkit/` (global), merged with local overriding global. Both directories are invisible to all MCP tools.

- [llmdevkit config](#llmdevkit-config)
- [llmdevkit setup](#llmdevkit-setup)
- [llmdevkit indexer](#llmdevkit-indexer)
- [llmdevkit mcp](#llmdevkit-mcp)
- [llmdevkit acp](#llmdevkit-acp)
- [llmdevkit server](#llmdevkit-server)

---

## llmdevkit-config

Manage the configuration file.

```
llmdevkit config [--global] <get|set> <namespace.key> [value]
llmdevkit config <root> <get|set> <namespace.key> [value]
```

With `--global`, operations target the global configuration file instead of the local one. The `--global` flag cannot be combined with a root directory argument.

Examples:

```
llmdevkit config set core.readonly true
llmdevkit config --global set core.readonly yes
llmdevkit config get core.readonly
llmdevkit config /path/to/project set core.readonly yes
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

### `commands` -- User-defined commands

The `commands` section lets you define named commands that can be listed and executed through the `available_commands` and `run_command` tools. Each command requires a `cmdline` and can optionally have a `description` and an `arguments` list.

| Key | Required | Description |
|-----|----------|-------------|
| `commands.list` | Yes | Comma-separated list of command names |
| `commands.<name>_cmdline` | Yes | The command line to execute |
| `commands.<name>_description` | No | Human-readable description of the command |
| `commands.<name>_arguments` | No | Comma-separated list of argument names the command accepts |

Example configuration:

```
llmdevkit config set commands.list build,test,run
llmdevkit config set commands.build_cmdline "make"
llmdevkit config set commands.build_arguments "target"
llmdevkit config set commands.test_cmdline "make test"
llmdevkit config set commands.test_description "Run tests"
```

---

## llmdevkit-setup

Download and configure llama.cpp for embedding and reranking models.

```
llmdevkit setup [--global] [rootdirectory]
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
| `llama.reranker_flags` | Extra flags for the reranker llama-server instance (e.g. `--ctx-size 4096`) |
| `llama.search_count` | Number of documents retrieved from the vector database (default: `50`) |
| `llama.result_count` | Number of final results returned after reranking (default: `10`) |
| `llama.reranker_enabled` | Set to `false`, `0`, `no`, `disabled`, or `off` to skip the reranker entirely (default: `true`) |
| `llama.extractor` | HuggingFace repo ID for the chat model used for fact extraction (default: `unsloth/Qwen3.5-0.8B-GGUF`) |
| `llama.extractor_flags` | Extra flags for the extractor llama-server instance (e.g. `--temp 0`) |

---

## llmdevkit-indexer

Build and query the code index. The initial index can take several minutes depending on project size.

```
echo "reindex" | llmdevkit indexer [rootdirectory]
```

Wait for the `ok` response, then start the MCP server with `--enable-indexer`. Subsequent startups will only index changed files (incremental via content hash tracking).

---

## llmdevkit-mcp

MCP server exposing Unix-inspired file tools, task management, command runner, code search, and memory. Designed for low token usage and sandboxed file access.

### Usage

```
llmdevkit mcp [--stdio|--http] [--address host:port] [--ignore pattern] [--show tools] [--hide tools] [--enable-indexer] [--enable-memory] [rootdirectory]
```

All paths are virtual -- `/` maps to the root directory. Path traversal is blocked.

- Default transport is stdio.
- `--http` starts a streamable HTTP server on the given `--address` (default `localhost:8080`).
- `--ignore` accepts a comma-separated list of glob patterns. Each path component (file or directory name) is matched against every pattern. Files and directories matching any pattern are hidden from all tools. Traversal tools (`ls`, `grep`, `sed`) skip entire matched directories. Examples: `--ignore '.*'` hides all dotfiles/dirs at any depth, `--ignore '.*,node_modules'` hides both dotfiles and `node_modules`.
- `--show` accepts a comma-separated list of tool names to expose (whitelist). Only the listed tools are available. Mutually exclusive with `--hide`. Proxied tools (from `mcps.yml`) are always included regardless of this flag.
- `--hide` accepts a comma-separated list of tool names to hide (blacklist). All other tools remain available. Mutually exclusive with `--show`. Proxied tools are always included regardless of this flag.
- If no root directory is given, the current working directory is used.
- `--enable-indexer` starts the code indexer subsystem (see Code Indexer section).
- `--enable-memory` starts the memory subsystem for fact storage and retrieval (see Memory section).

## Tools

| Tool Name | Description | Argument Name | Description |
|-----------|-------------|---------------|-------------|
| `ls` | | `pathspec:string *` | Glob expression for file names |
| `file_read` | Read file | `path:string *` | |
| | | `line_range:string *` | Line range, 1-indexed. Formats: from:to, from-to, [from:to], [from-to] |
| `file_create` | | `path:string *` | |
| | | `content:string *` | |
| `mv` | Move files | `source:string *` | |
| | | `dest:string *` | |
| `grep` | Print lines matching pattern with context (`grep -A1 -B1`) | `pattern:string *` | Regexp |
| | | `pathspec:string *` | Glob expression for file names |
| `sed` | Search and replace in files (`sed -i`) | `pattern:string *` | Regexp |
| | | `replacement:string *` | |
| | | `pathspec:string *` | Glob expression for file names |
| `edit` | | `path:string *` | File path |
| | | `start_line_number:number *` | Line number where original_window begins (1-indexed) |
| | | `original_window:string *` | Text to be replaced |
| | | `modified_window:string *` | Text to be inserted |
| `rm` | | `path:string *` | File path |
| `stat` | Infos on files and directories | `path:string *` | |
| `tree` | Directory tree of the project (like `tree -d`) | | |
| `tasks_list` | List of tasks ([ ] created, [_] in progress, [X] completed) | | |
| `task_create` | Create a new task | `description:string *` | Task description |
| | | `parent:string` | ID of parent task, optional |
| | | `status:string` | One of: created, in_progress, completed. Optional |
| `task_set_status` | Change status of task | `ID:string *` | Task ID |
| | | `status:string *` | One of: created, in_progress, completed |
| `task_delete` | | `ID:string *` | |
| `tasks_clear` | Clear all tasks | | |
| `w3m-dump` | Fetch a webpage text (like `w3m-dump`) | `url:string *` | |
| `online_search` | Search online | `search_query:string *` | |
| `examples` | Show usage examples for a tool | `tool_name:string *` | |
| `available_commands` | List available commands | | |
| `run_command` | Run the command from available_commands | `name:string *` | |
| | | `arguments:array` | Array of strings to pass to the command line |
| | | `timeout:number *` | Timeout in seconds |
| `relevant_code` | *(indexer)* | `prompt:string *` | |
| `search_symbol_in_code` | *(indexer)* | `symbol_name:string *` | |
| `memory_put` | Add a phrase (fact) to the system | `fact:string *` | Fact phrase to memorize |
| `relevant_memory` | Search relevant facts from prompt string | `prompt:string *` | |
| `memory_extract` | Extract facts from text and store them in memory, deduplicating against existing facts | `text:string *` | Text to extract facts from (conversation, notes, document) |

### `mcps` -- Upstream MCP server proxying

`llmdevkit mcp` can proxy tools from upstream MCP servers, making them available as if they were built-in. Configuration is loaded from both global (`$XDG_CONFIG_HOME/llmdevkit/mcps.yml`) and local (`[root]/.llmdevkit/mcps.yml`), merged with local overriding global.

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

When `tools` is omitted, all upstream tools are proxied. When present, only listed tools are proxied. Proxied tools are excluded from `--show`/`--hide` filtering -- they are always visible.

### Code Indexer

Requires `--enable-indexer`. Provides `relevant_code` (semantic code search) and `search_symbol_in_code` (symbol substring search). Powered by llama.cpp embedding and reranking models. See [llmdevkit setup](#llmdevkit-setup) for configuration.

### Memory

Requires `--enable-memory`. Provides `memory_put` (store a fact), `relevant_memory` (semantic fact retrieval), and `memory_extract` (LLM-powered fact extraction from text). Uses the same llama.cpp infrastructure as the code indexer.

---

## llmdevkit-acp

ACP (Agent Client Protocol) server that orchestrates LLMs with tool-calling capabilities. Implements a fully compliant [ACP agent](https://agentclientprotocol.com/) using stdio transport.

Configuration is loaded from `.llmdevkit/` (local) and `$XDG_CONFIG_HOME/llmdevkit/` (global), merged with local overriding global. Three YAML files define the agent behavior:

### `llms.yml` -- LLM endpoints

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

### `mcps.yml` -- MCP tool servers

Same format as [llmdevkit mcp upstream proxying](#mcps--upstream-mcp-server-proxying). Defines MCP servers whose tools are available to agents. Supports `url` (streamable HTTP), `sse`, and `stdio` transports.

### `agents.yml` -- Agent definitions

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
```

#### Tool sources

The `tools` field is a space-separated list. Each token can be:

| Token | Description |
|-------|-------------|
| A name from `mcps.yml` | All tools from that MCP server are attached |
| `devkit` | In-process `llmdevkit mcp` instance (file tools, tasks, commands, etc.) |
| `agents` | Sub-agent invocation tools: `agents_available` (list agents) and `agent_invoke` (run agent with prompt). Each sub-agent invocation uses a fresh context -- no conversation sharing. |

#### Hooks

Hooks are automatic tool calls at specific lifecycle points. Each hook maps tool names to argument overrides. The special `%p` placeholder is replaced with the current user prompt text.

### Usage

```
llmdevkit acp
```

Communicates over stdio using the ACP JSON-RPC protocol. Designed to be launched by an ACP-compatible client.

---

## llmdevkit-server

A web-based chat UI server that provides a browser interface for interacting with agents through `llmdevkit acp`. Manages conversations, streams LLM responses in real-time, and surfaces human-in-the-loop tools (questions, approvals) as interactive UI elements.

```
llmdevkit server [--enable-indexer]
```

Listens on `http://localhost:18681` and serves a single-page chat application.

### Features

- **Web chat UI** -- embedded single-page app with dark theme, conversation sidebar, markdown rendering, and auto-scroll
- **Agent selection** -- pick any agent defined in `agents.yml`; system prompt and tool set are loaded from config
- **LLM selection** -- pick any LLM from `llms.yml` to use with any agent; can switch mid-conversation, persisted across restarts
- **AGENTS.md** -- if present in the project root, its content is appended to the system prompt on every turn, providing project-specific instructions to the agent
- **Real-time streaming** -- LLM text and thinking chunks are streamed live via Server-Sent Events (SSE)
- **Tool call visualization** -- shows tool requests and responses as styled message bubbles
- **Human-in-the-loop tools** -- interactive UI for three tool types proxied from the ACP subprocess:
  - `ask_open_ended` -- text input for free-form answers
  - `ask_exec` -- command approval dialog with confirm/deny
  - `ask_multiple_choice` -- selectable choice list with optional free-text option
- **Conversation persistence** -- each conversation is stored as a JSONL file under `.llmdevkit/conversations/`, reloaded on restart
- **Conversation management** -- rename, undo last exchange, trim from a point onward
- **Message queue** -- enqueue prompts while an agent turn is running; queued messages are sent sequentially once the current turn completes
- **Task management UI** -- task list panel that parses `task_create`/`task_set_status`/`task_delete`/`tasks_list`/`tasks_clear` tool responses and renders status checkboxes; supports deleting tasks via the UI
- **Token usage tracking** -- displays cumulative prompt/completion token counts and LLM call count
- **ACP orchestration** -- spawns `llmdevkit acp` as a subprocess over stdio, communicates via ACP JSON-RPC, and proxies all session updates to the browser
- **Side channel** -- HTTP endpoint (`/api/sidechannel`) that the ACP subprocess uses to forward ask-tool requests, token stats, and tool definition caches back to the server
- **MCP tool definitions** -- resolves tool schemas from configured MCP servers and the in-process devkit tools for display in the UI
- **`--enable-indexer` flag** -- passed through to `llmdevkit acp` via environment variable to enable code indexing tools
- **Browser notifications** -- optional service worker delivers desktop notifications when an agent needs input (ask tools) or finishes a turn; bell icon in top bar toggles on/off; notifications are clickable and reopen the tab

### REST API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Serve the embedded web UI |
| `/api/agents` | GET | List available agents and their LLM models |
| `/api/llms` | GET | List available LLMs from `llms.yml` |
| `/api/tooldefs?agent=<name>` | GET | List tool definitions for an agent |
| `/api/conversations` | GET | List all conversations |
| `/api/conversations` | POST | Create a new conversation |
| `/api/conversations/<id>` | GET | Get a single conversation |
| `/api/conversations/<id>` | DELETE | Delete a conversation |
| `/api/conversations/<id>/init` | POST | Initialize ACP session and send first prompt |
| `/api/conversations/<id>/prompt` | POST | Send a follow-up prompt |
| `/api/conversations/<id>/cancel` | POST | Cancel a running agent turn |
| `/api/conversations/<id>/rename` | POST | Rename a conversation |
| `/api/conversations/<id>/llm_change` | POST | Change the LLM for a conversation |
| `/api/conversations/<id>/undo` | POST | Remove last exchange (assistant + user message) |
| `/api/conversations/<id>/trim` | POST | Remove all exchanges from a given index onward |
| `/api/conversations/<id>/enqueue` | POST | Enqueue a prompt for later delivery |
| `/api/conversations/<id>/queue` | GET | List queued prompts |
| `/api/conversations/<id>/queue/<idx>` | POST | Delete a queued prompt by index |
| `/api/ask/<id>` | POST | Submit an answer to a pending ask-tool request |
| `/api/tasks` | GET | Read current task list |
| `/api/tasks/delete` | POST | Delete a task by ID |
| `/api/sidechannel` | POST | Internal endpoint for ACP subprocess callbacks |
| `/api/events` | GET | SSE stream for real-time updates |
| `/api/notifications?since=<ts>` | GET | Poll notification events (ask-tool and turn completion) since a Unix timestamp; used by the service worker |

## License

This project is licensed under the GNU General Public License v3.0 or later - see the [LICENSE](https://github.com/dgdevel/llmdevkit/blob/main/LICENSE.md) file for details.

Copyright (C) 2026 Daniele Guttuso

