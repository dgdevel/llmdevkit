# Conversations Format

Conversation files are stored as `.jsonl` (JSON Lines) in the conversations directory. Each line is a single JSON object with two top-level fields:

```json
{"type": "<record_type>", "payload": <value>}
```

| Field    | Type   | Description                                      |
|----------|--------|--------------------------------------------------|
| `type`   | string | Record type identifier (see table below)         |
| `payload`| any    | Record-specific data, structure varies by type   |

File naming: `<conversation_id>.jsonl`

---

## Record Types

### 1. `conversation_created`

Creates or updates conversation metadata. Written on conversation creation and when title changes (e.g., via `rename_conversation`).

**Payload:** `Conversation` object

| Field           | Type       | Mandatory | Description                                              |
|-----------------|------------|-----------|----------------------------------------------------------|
| `id`            | string     | yes       | Unique conversation ID                                   |
| `agent`         | string     | yes       | Agent name (e.g. `"code"`)                               |
| `system_prompt` | string     | no        | System prompt for the agent                              |
| `tools`         | string[]   | no        | List of tool names available to the agent                |
| `tool_defs`     | object[]   | no        | Array of `ToolDefInfo` objects (tool definitions)        |
| `title`         | string     | no        | Conversation title                                       |
| `messages`      | object[]   | yes       | Array of `BubbleMessage` objects                         |
| `running`       | bool       | yes       | Whether the agent is currently executing                 |
| `file_size`     | int        | no        | File size in bytes                                       |
| `queue`         | string[]   | no        | Queued prompts waiting to be processed                   |
| `acp_session_id`| string     | no        | ACP session identifier                                   |

**ToolDefInfo** (inside `tool_defs`):

| Field         | Type          | Mandatory | Description                        |
|---------------|---------------|-----------|------------------------------------|
| `name`        | string        | yes       | Tool name                          |
| `description` | string        | no        | Human-readable description         |
| `parameters`  | object        | no        | JSON schema of tool parameters     |

---

### 2. `init`

Records the initialization of a conversation run (agent config, session, system prompt, tools). Written when first prompt is sent.

**Payload:** plain object

| Field           | Type     | Mandatory | Description                              |
|-----------------|----------|-----------|------------------------------------------|
| `agent`         | string   | yes       | Agent name                               |
| `acp_session`   | string   | no        | ACP session ID                           |
| `system_prompt` | string   | no        | System prompt                            |
| `tools`         | string[] | no        | List of tool names                       |

---

### 3. `bubble`

A message in the conversation. All chat content flows through this type.

**Payload:** `BubbleMessage` object

| Field              | Type     | Mandatory | Description                                           |
|--------------------|----------|-----------|-------------------------------------------------------|
| `type`             | string   | yes       | Bubble subtype (see bubble types below)               |
| `content`          | string   | yes       | Message text or JSON-encoded tool data                |
| `name`             | string   | no        | Tool name (for `tool_request`, `tool_response`)       |
| `id`               | string   | no        | Unique ID for ask-type bubbles                        |
| `timestamp`        | string   | no        | ISO 8601 timestamp                                    |
| `cmdline`          | string   | no        | Command to execute (for `ask_exec`)                   |
| `timeout`          | int      | no        | Command timeout in seconds (for `ask_exec`)           |
| `choices`          | string[] | no        | Choice options (for `ask_multiple_choice`)            |
| `allow_open_ended` | bool     | no        | Allow custom text answer (for `ask_multiple_choice`)  |
| `question`         | string   | no        | Question text (for ask types)                         |
| `answered`         | bool     | no        | Whether user has responded (for ask types)            |
| `approved`         | bool     | no        | Whether user approved execution (for `ask_exec`)      |
| `answer`           | string   | no        | User's response text                                  |
| `token_count`      | int      | no        | LLM token count for this message                      |

**Bubble subtypes** (`type` field values):

| Value                  | Description                                    |
|------------------------|------------------------------------------------|
| `user`                 | User message (prompt)                          |
| `llm`                  | LLM response text                              |
| `thinking`             | LLM thinking/reasoning content                 |
| `tool_request`         | Tool call request from agent                   |
| `tool_response`        | Result returned after tool execution           |
| `ask_open_ended`       | Open-ended question to user                    |
| `ask_exec`             | Command execution authorization request        |
| `ask_multiple_choice`  | Multiple choice question to user               |
| `error`                | Error message                                  |

---

### 4. `bubble_merge`

Streams incremental content to the last bubble of the same type. Used for streaming LLM responses (`llm` and `thinking` types). On load, merged into the preceding bubble's `content`.

**Payload:** `BubbleMessage` object (same structure as `bubble`, but only `type` and `content` matter)

| Field     | Type   | Mandatory | Description                        |
|-----------|--------|-----------|------------------------------------|
| `type`    | string | yes       | Must match preceding bubble type   |
| `content` | string | yes       | Incremental text to append         |

---

### 5. `prompt_response`

Records the stop reason when an LLM prompt completes. Informational only, ignored on load.

**Payload:** plain object

| Field         | Type   | Mandatory | Description                          |
|---------------|--------|-----------|--------------------------------------|
| `stop_reason` | string | yes       | Why LLM stopped (e.g. `"end_turn"`)  |

---

### 6. `token_stats`

Token usage statistics from the LLM side-channel. Used to attach `token_count` to the last `llm` bubble on load.

**Payload:** `TokenStats` object

| Field                | Type   | Mandatory | Description                        |
|----------------------|--------|-----------|------------------------------------|
| `prompt_tokens`      | int    | no        | Tokens in the prompt               |
| `completion_tokens`  | int    | no        | Tokens in the completion           |
| `total_tokens`       | int    | no        | Total tokens used                  |
| `llm_calls`          | int    | no        | Number of LLM API calls            |

---

### 7. `tool_request_rawinput`

Raw input injected into a pending tool request. Used when the user provides raw input for a tool call.

**Payload:** plain object

| Field        | Type   | Mandatory | Description                      |
|--------------|--------|-----------|----------------------------------|
| `toolCallId` | string | yes       | Tool call identifier             |
| `rawInput`   | string | yes       | Raw input data                   |

---

### 8. `prompt` (legacy, read-only)

Legacy record type consumed only during file loading. Never written by current code. Converts to a `user` bubble on load.

**Payload:** plain object

| Field    | Type   | Mandatory | Description              |
|----------|--------|-----------|--------------------------|
| `prompt` | string | yes       | User prompt text         |

---

## File Lifecycle

1. **Creation**: `conversation_created` written with initial metadata.
2. **First prompt**: `init` then `bubble` (type `user`) written.
3. **During execution**: `bubble_merge` for streaming LLM output, `bubble` for new messages, `token_stats` for usage data.
4. **Completion**: `prompt_response` written with stop reason.
5. **Rename**: New `conversation_created` appended with updated title.

## Loading Rules

- `conversation_created`: merges metadata fields (last value wins).
- `init`: sets agent, session, system prompt, tools.
- `bubble`: appended to messages array.
- `bubble_merge`: content appended to last message of same type.
- `prompt`: converted to `user` bubble and appended.
- `prompt_response`: ignored (informational).
- `token_stats`: stores `total_tokens`, applied to next `llm` bubble on load.
- `tool_request_rawinput`: ignored during load.
