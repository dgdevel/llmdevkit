// Shared mutable state - single object, mutated in-place
export const S = {
  agents: [],
  conversations: [],
  activeConvId: null,
  running: false,
  pendingAsk: null,
  setupResolve: null,
  taskEntries: [],
  toolCallIdToName: {},
  toolDefsCache: {},
  tokenStats: {prompt_tokens:0, completion_tokens:0, total_tokens:0, llm_calls:0},
  _renderScheduled: false,
  _streamUpdateScheduled: false,
  setupModalEl: null,
  _bubbleId: 0,
};
