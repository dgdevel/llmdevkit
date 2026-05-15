import { S } from './state.js';
import { renderConvList, selectConversation } from './sidebar.js';

export function getActiveConv() {
  return S.conversations.find(c => c.id === S.activeConvId);
}

export async function loadToolDefs(agentName) {
  if (S.toolDefsCache[agentName]) return S.toolDefsCache[agentName];
  try {
    const r = await fetch('/api/tooldefs?agent=' + encodeURIComponent(agentName));
    const defs = await r.json();
    S.toolDefsCache[agentName] = defs;
    return defs;
  } catch(e) { return []; }
}

export async function newConversation() {
  document.getElementById('setupAgent').value = document.getElementById('agentSelect').value;
  document.getElementById('setupSysPrompt').value = '';
  S.setupModalEl.show();
  return new Promise(r => { S.setupResolve = r; });
}

export function cancelSetup() {
  S.setupModalEl.hide();
  S.setupResolve = null;
}

export async function confirmSetup() {
  const agent = document.getElementById('setupAgent').value;
  const sysPrompt = document.getElementById('setupSysPrompt').value;
  S.setupModalEl.hide();
  const r = await fetch('/api/conversations', {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify({agent, system_prompt: sysPrompt})
  });
  const conv = await r.json();
  const existingIdx = S.conversations.findIndex(c => c.id === conv.id);
  if (existingIdx >= 0) {
    S.conversations[existingIdx] = conv;
  } else {
    S.conversations.unshift(conv);
  }
  S.activeConvId = conv.id;
  renderConvList();
  selectConversation(conv.id);
  if (S.setupResolve) S.setupResolve(conv);
}

export function onAgentChange() {}
