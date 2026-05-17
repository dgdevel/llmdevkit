# dev-solo: self-calling developer agent

A single-agent setup where the developer agent can invoke itself as a sub-agent.

## Setup

1. Run the setup script:
   ```
   llmdevkit setup --global
   ```

2. Copy or symlink these files into your project's `.llmdevkit/` directory:
   ```
   mkdir -p /path/to/project/.llmdevkit
   cp llms.yml mcps.yml agents.yml /path/to/project/.llmdevkit/
   ```

3. Edit `llms.yml` — point `api_base` to your OpenAI-compatible LLM server.

4. (Optional) Configure `llmdevkit mcp` tools in the project root:
   ```
   llmdevkit config set commands.list build,test
   llmdevkit config set commands.build_cmdline "make"
   llmdevkit config set commands.test_cmdline "make test"
   ```

5. Run the http server:
   ```
   llmdevkit server
   ```

   Or with indexer:
   ```
   echo reindex | llmdevkit indexer
   llmdevkit server --enable-indexer
   ```

6. Open [your browser](http://127.0.0.1:18681/).

## How it works

- **`devkit`** tool source: spawns an in-process `llmdevkit mcp` instance providing file operations, task management, command execution, search, etc.
- **`agents`** tool source: exposes `agents_available` and `agent_invoke`. The dev agent can call `agent_invoke` with a prompt — this launches a fresh dev agent instance with its own context, enabling task delegation without polluting the main conversation.
- **`ask`** tool source: enable interactivity (human in the loop)


