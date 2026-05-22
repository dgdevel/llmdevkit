import {
    S
} from './state.js';
import {
    renderQueue
} from './queue.js';
import {
    updateLLMSelect
} from './sidebar.js';
import {
    formatTokenCount
} from './utils.js';

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
        stateEl.innerHTML = '<span class="text-success fw-semibold">\u25CF Running</span>';
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
    updateCtxUsageBadge(conv);
}

export function updateCtxUsageBadge(conv) {
    const badge = document.getElementById('ctxUsageBadge');
    if (!badge) return;
    if (!conv) {
        badge.classList.add('d-none');
        return;
    }
    const llmName = conv.llm || '';
    const llm = S.llms.find(l => l.name === llmName);
    const ctxSize = llm?.context_size || 0;
    if (ctxSize <= 0) {
        badge.classList.add('d-none');
        return;
    }
    const used = S.lastPromptTokens[conv.id] || conv.last_prompt_tokens || 0;
    if (used <= 0) {
        badge.classList.add('d-none');
        return;
    }
    const pct = Math.min(100, Math.round(used / ctxSize * 100));
    badge.classList.remove('d-none');
    let cls = 'text-bg-secondary';
    if (pct >= 90) cls = 'text-bg-danger';
    else if (pct >= 70) cls = 'text-bg-warning';
    else if (pct >= 50) cls = 'text-bg-info';
    badge.className = 'badge small ' + cls;
    badge.textContent = `CTX ${formatTokenCount(used)}/${formatTokenCount(ctxSize)} (${pct}%)`;
    badge.title = `Context: ${used.toLocaleString()} / ${ctxSize.toLocaleString()} tokens (${pct}%)`;
}