import { S } from './state.js';

export function updateState(conv) {
  const stateEl = document.getElementById('stateInfo');
  const cancelBtn = document.getElementById('cancelBtn');
  const sendBtn = document.getElementById('sendBtn');
  const undoBtn = document.getElementById('undoBtn');
  const trimBtn = document.getElementById('trimBtn');
  const llmEl = document.getElementById('llmName');
  if (!conv) {
    stateEl.innerHTML = '';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = true;
    undoBtn.disabled = true;
    trimBtn.disabled = true;
    llmEl.textContent = '';
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
    undoBtn.disabled = true;
    trimBtn.disabled = true;
  } else {
    stateEl.textContent = 'Idle';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = false;
    undoBtn.disabled = false;
    trimBtn.disabled = false;
  }
}
