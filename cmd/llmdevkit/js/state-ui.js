import { S } from './state.js';
import { renderQueue } from './queue.js';
import { updateLLMSelect } from './sidebar.js';

export function updateState(conv) {
  const stateEl = document.getElementById('stateInfo');
  const cancelBtn = document.getElementById('cancelBtn');
  const sendBtn = document.getElementById('sendBtn');
  const undoBtn = document.getElementById('undoBtn');
  const trimBtn = document.getElementById('trimBtn');
  const llmSel = document.getElementById('llmSelect');
  if (!conv) {
    stateEl.innerHTML = '';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = true;
    undoBtn.disabled = true;
    trimBtn.disabled = true;
    llmSel.disabled = true;
    llmSel.innerHTML = '';
    return;
  }
  updateLLMSelect(conv);
  const isRunning = conv.running || false;
  llmSel.disabled = isRunning;
  if (isRunning) {
    stateEl.innerHTML = '<span class="text-success fw-semibold">● Running</span>';
    cancelBtn.classList.remove('d-none');
    sendBtn.disabled = false;
    undoBtn.disabled = true;
    trimBtn.disabled = true;
  } else {
    stateEl.textContent = 'Idle';
    cancelBtn.classList.add('d-none');
    sendBtn.disabled = false;
    undoBtn.disabled = false;
    trimBtn.disabled = false;
  }
  renderQueue();
}
