import { S } from './state.js';
import { getActiveConv } from './conversation.js';
import { esc } from './utils.js';

export function renderQueue() {
  const el = document.getElementById('queueContainer');
  if (!el) return;
  const conv = getActiveConv();
  const queue = (conv && conv.queue) || [];
  S.messageQueue = queue;

  // Update send button text based on running state
  const sendBtn = document.getElementById('sendBtn');
  if (sendBtn) {
    const isRunning = conv && conv.running;
    sendBtn.textContent = isRunning ? '\u23F3 Queue' : '\u25B6 Send';
  }

  if (queue.length === 0) {
    el.innerHTML = '';
    el.style.display = 'none';
    return;
  }
  el.style.display = 'block';
  el.innerHTML = queue.map((msg, i) => `
    <div class="d-flex align-items-center gap-2 py-1 px-2 border rounded small mb-1 queue-item" data-idx="${i}">
      <span class="text-body-secondary flex-shrink-0 small">#${i+1}</span>
      <span class="flex-grow-1 text-truncate">${esc(msg)}</span>
      <button class="btn btn-sm btn-outline-secondary py-0 px-1 flex-shrink-0 queue-del-btn" data-idx="${i}" title="Remove from queue">\u2715</button>
    </div>
  `).join('');
}

export async function enqueuePrompt(text) {
  const conv = getActiveConv();
  if (!conv) return;
  try {
    const r = await fetch('/api/conversations/' + conv.id + '/enqueue', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt: text })
    });
    const data = await r.json();
    if (data.queue) {
      conv.queue = data.queue;
      S.messageQueue = data.queue;
    }
    renderQueue();
  } catch (e) {
    console.error('enqueue error:', e);
  }
}

export async function removeFromQueue(idx) {
  const conv = getActiveConv();
  if (!conv) return;
  try {
    const r = await fetch('/api/conversations/' + conv.id + '/queue/' + idx, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' }
    });
    const data = await r.json();
    if (data.queue) {
      conv.queue = data.queue;
      S.messageQueue = data.queue;
    }
    renderQueue();
  } catch (e) {
    console.error('queue delete error:', e);
  }
}

export function updateQueueFromSSE(queue) {
  const conv = getActiveConv();
  if (conv) {
    conv.queue = queue || [];
    S.messageQueue = conv.queue;
  } else {
    S.messageQueue = queue || [];
  }
  renderQueue();
}
