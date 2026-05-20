// Shared mutable state - single object, mutated in-place
export const S = {
  agents: [],
  llms: [],
  conversations: [],
  activeConvId: null,
  running: false,
  pendingAsk: null,
  setupResolve: null,
  taskEntries: [],
  toolCallIdToName: {},
  toolDefsCache: {},
  messageQueue: [],
  lastPromptTokens: {},  // convId → last prompt_tokens from token_stats

  _renderScheduled: false,
  _streamUpdateScheduled: false,
  setupModalEl: null,
  toolEnrichModalEl: null,
  _bubbleId: 0,
};
