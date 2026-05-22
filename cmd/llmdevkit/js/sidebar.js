import {
    S
} from './state.js';
import {
    esc,
    formatFileSize
} from './utils.js';
import {
    renderMessages
} from './messages.js';
import {
    updateState
} from './state-ui.js';
import {
    rebuildTaskState
} from './tasks.js';
import {
    loadToolDefs
} from './conversation.js';
import {
    renderQueue
} from './queue.js';

export function updateLLMSelect(conv) {
    const sel = document.getElementById('llmSelect');
    sel.innerHTML = '';
    S.llms.forEach(l => {
        sel.innerHTML += `<option value="${l.name}"${conv?.llm === l.name ? ' selected' : ''}>${l.name}</option>`;
    });
    sel.value = conv?.llm || S.llms[0]?.name || '';
}

export function getDefaultLLMForAgent(agentName) {
    const ag = S.agents.find(a => a.name === agentName);
    return ag?.llm || '';
}

export function resolveLLMName(llmInternalName) {
    const l = S.llms.find(x => x.name === llmInternalName);
    return l?.display_name || l?.name || llmInternalName || '';
}

export async function loadAgents() {
    const r = await fetch('/api/agents');
    S.agents = await r.json();
    const sel = document.getElementById('agentSelect');
    const sel2 = document.getElementById('setupAgent');
    sel.innerHTML = '';
    sel2.innerHTML = '';
    S.agents.forEach(a => {
        sel.innerHTML += `<option value="${a.name}">${a.name}</option>`;
        sel2.innerHTML += `<option value="${a.name}">${a.name}</option>`;
    });
}

export async function loadLLMs() {
    const r = await fetch('/api/llms');
    S.llms = await r.json();
}

export async function loadConversations() {
    const r = await fetch('/api/conversations');
    S.conversations = await r.json();
}

export function renderConvList() {
    const el = document.getElementById('convList');
    el.innerHTML = '';
    S.conversations.slice().reverse().forEach(c => {
        const active = c.id === S.activeConvId;
        const label = c.title || c.id.slice(0, 8);
        const statusCls = c.running ? 'running' : 'idle';
        const sizeStr = c.file_size ? formatFileSize(c.file_size) : '';
        el.innerHTML += `<div class="conv-item d-flex justify-content-between align-items-center rounded px-2 py-1 small list-group-item list-group-item-action${active ? ' active' : ''}" data-id="${c.id}">
      <div class="d-flex align-items-center gap-1 flex-grow-1 overflow-hidden">
        <span class="status-dot ${statusCls} rounded-circle flex-shrink-0${statusCls === 'running' ? ' bg-success' : ' bg-secondary'}" style="width:8px;height:8px;display:inline-block;opacity:.4"></span>
        <span class="conv-label overflow-hidden text-truncate">${esc(label)}</span>
        ${sizeStr ? `<span class="text-body-secondary" style="font-size:.65rem;white-space:nowrap">${sizeStr}</span>` : ''}
      </div>
      <span class="d-flex gap-1 align-items-center">
        <span class="conv-rename p-0 border-0 text-body-secondary" style="cursor:pointer;font-size:.65rem;display:none" title="Rename">\u270F</span>
        <button class="btn btn-sm p-0 border-0 conv-del text-danger" style="font-size:1rem;line-height:1">\u00D7</button>
      </span>
    </div>`;
    });
    el.querySelectorAll('.conv-item').forEach(item => {
        item.addEventListener('click', () => selectConversation(item.dataset.id));
        const delBtn = item.querySelector('.conv-del');
        delBtn.addEventListener('click', e => {
            e.stopPropagation();
            deleteConversation(item.dataset.id);
        });
        const renameBtn = item.querySelector('.conv-rename');
        renameBtn.addEventListener('click', e => {
            e.stopPropagation();
            startRename(item.dataset.id);
        });
        item.addEventListener('mouseenter', () => {
            renameBtn.style.display = 'inline';
        });
        item.addEventListener('mouseleave', () => {
            renameBtn.style.display = 'none';
        });
    });
}

export function startRename(convId) {
    const conv = S.conversations.find(c => c.id === convId);
    if (!conv) return;
    const item = document.querySelector(`.conv-item[data-id="${convId}"]`);
    if (!item) return;
    const labelEl = item.querySelector('.conv-label');
    if (!labelEl) return;
    const currentTitle = conv.title || conv.id.slice(0, 8);
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'form-control form-control-sm p-0 border-0 bg-transparent';
    input.style.cssText = 'font-size:inherit;outline:none;box-shadow:none;height:auto;';
    input.value = currentTitle;
    labelEl.replaceWith(input);
    input.focus();
    input.select();
    const finish = async () => {
        const newTitle = input.value.trim() || currentTitle;
        if (newTitle !== conv.title) {
            conv.title = newTitle;
            await fetch('/api/conversations/' + convId + '/rename', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                    title: newTitle
                })
            });
        }
        renderConvList();
    };
    input.addEventListener('blur', finish);
    input.addEventListener('keydown', e => {
        if (e.key === 'Enter') {
            e.preventDefault();
            input.blur();
        }
        if (e.key === 'Escape') {
            input.value = currentTitle;
            input.blur();
        }
    });
}

export async function selectConversation(id) {
    S.activeConvId = id;
    renderConvList();
    const conv = S.conversations.find(c => c.id === id);
    if (conv) {
        document.getElementById('agentSelect').value = conv.agent || S.agents[0]?.name || '';
        updateLLMSelect(conv);
        if (conv.agent) await loadToolDefs(conv.agent);
        await rebuildTaskState(conv);
        renderMessages(conv);
        renderQueue(conv);
        updateState(conv);
    }
}

export async function deleteConversation(id) {
    await fetch('/api/conversations/' + id, {
        method: 'DELETE'
    });
    S.conversations = S.conversations.filter(c => c.id !== id);
    if (S.activeConvId === id) {
        S.activeConvId = S.conversations[0]?.id || null;
    }
    renderConvList();
    if (S.activeConvId) selectConversation(S.activeConvId);
    else document.getElementById('messages').innerHTML = '';
}