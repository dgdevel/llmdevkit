# dev-solo: self-calling developer agent

A single-agent setup where the developer agent can invoke itself as a sub-agent.

## Setup

1. Copy or symlink these files into your project's `.llmdevkit/` directory:
   ```
   mkdir -p /path/to/project/.llmdevkit
   cp llms.yml mcps.yml agents.yml /path/to/project/.llmdevkit/
   ```

2. Edit `llms.yml` — point `api_base` to your OpenAI-compatible LLM server.

3. (Optional) Configure `llmdevkit-mcp` tools in the project root:
   ```
   llmdevkit-config set commands.list build,test
   llmdevkit-config set commands.build_cmdline "make"
   llmdevkit-config set commands.test_cmdline "make test"
   ```

4. Run the ACP server:
   ```
   llmdevkit-acp
   ```

5. Connect from an ACP-compatible client.

## How it works

- **`devkit`** tool source: spawns an in-process `llmdevkit-mcp` instance providing file operations, task management, command execution, search, etc.
- **`agents`** tool source: exposes `agents_available` and `agent_invoke`. The dev agent can call `agent_invoke` with a prompt — this launches a fresh dev agent instance with its own context, enabling task delegation without polluting the main conversation.
- Hooks are configured but empty — extend them to trigger automatic tool calls at lifecycle boundaries (e.g., auto-save context on conversation end).

## Extending

Add more agents to `agents.yml` for specialized roles (code reviewer, test writer, etc.) and they will automatically appear in `agents_available`.
