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
  messageQueue: [],

  _renderScheduled: false,
  _streamUpdateScheduled: false,
  setupModalEl: null,
  _bubbleId: 0,
};
