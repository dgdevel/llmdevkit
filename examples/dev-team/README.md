# dev-team: multi-agent developer team

A multi-agent setup where a manager agent orchestrates a team of specialized agents.

## Agents

| Agent | Role |
|-------|------|
| **manager** | Orchestrates work, delegates tasks, reviews outputs, coordinates the team |
| **developer** | Implements code changes, creates files, fixes bugs |
| **qa** | Reviews code quality, tests changes, catches regressions |
| **researcher** | Researches libraries, APIs, documentation, online sources |

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

3. Edit `llms.yml` -- point `api_base` to your OpenAI-compatible LLM server.

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

- **Manager** receives user requests, breaks them into tasks, and delegates via `agent_invoke` to specialist agents.
- **Developer** handles code changes -- file creation, editing, refactoring. Has full `devkit` tools.
- **QA** reviews changes, runs tests, checks for regressions. Has `devkit` read/search tools + `ask`.
- **Researcher** investigates external info -- web search, documentation, repo wiki. Has `devkit` tools + `agents` tool for deeper lookups.
- **`agents`** tool source: exposes `agents_available` and `agent_invoke`. Manager uses this to dispatch work to specialists.
- **`ask`** tool source: enables human-in-the-loop interaction for approvals or clarifications.
