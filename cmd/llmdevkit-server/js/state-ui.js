import { S } from './state.js';
import { formatTokenCount } from './utils.js';

export function updateState(conv) {
  const stateEl = document.getElementById('stateInfo');
  const cancelBtn = document.getElementById('cancelBtn');
  const sendBtn = document.getElementById('sendBtn');
  const llmEl = document.getElementById('llmName');
  if (!conv) {
    stateEl.innerHTML = '';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = true;
    llmEl.textContent = '';
    updateTokenPill();
    return;
  }
  const agentName = conv.agent || '';
  const ag = S.agents.find(a => a.name === agentName);
  llmEl.textContent = ag?.llm || agentName;
  const isRunning = conv.running || false;
  if (isRunning) {
    stateEl.innerHTML = '<span class="text-success fw-semibold">● Running</span>';
    cancelBtn.classList.remove('d-none');
    sendBtn.disabled = true;
  } else {
    stateEl.textContent = 'Idle';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = false;
  }
  if (conv.token_stats) {
    S.tokenStats = conv.token_stats;
  }
  updateTokenPill();
}

export function updateTokenPill() {
  const pill = document.getElementById('tokenPill');
  const tokTotal = document.getElementById('tokTotal');
  if (S.tokenStats.total_tokens > 0) {
    pill.classList.remove('d-none');
    pill.classList.add('d-inline-flex');
    tokTotal.textContent = formatTokenCount(S.tokenStats.total_tokens);
  } else {
    pill.classList.add('d-none');
    pill.classList.remove('d-inline-flex');
  }
}
