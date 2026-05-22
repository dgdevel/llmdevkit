import {
    S
} from './state.js';
import {
    renderConvList,
    selectConversation
} from './sidebar.js';

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
    } catch (e) {
        return [];
    }
}

export async function newConversation() {
    document.getElementById('setupAgent').value = document.getElementById('agentSelect').value;
    document.getElementById('setupSysPrompt').value = '';
    S.setupModalEl.show();
    return new Promise(r => {
        S.setupResolve = r;
    });
}

export function cancelSetup() {
    S.setupModalEl.hide();
    S.setupResolve = null;
}

export async function confirmSetup() {
    const agent = document.getElementById('setupAgent').value;
    const sysPrompt = document.getElementById('setupSysPrompt').value;
    S.setupModalEl.hide();
    const d = new Date();
    const pad = n => String(n).padStart(2, '0');
    const title = `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    const r = await fetch('/api/conversations', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            agent,
            system_prompt: sysPrompt,
            title
        })
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

export function onSetupAgentChange() {
    const agentName = document.getElementById('setupAgent').value;
    const ag = S.agents.find(a => a.name === agentName);
    if (!ag) return;
    // Update the top-bar LLM select to show the agent's default LLM
    const llmSel = document.getElementById('llmSelect');
    if (ag.llm) {
        // ag.llm is the display name from /api/agents, we need to find the internal name
        const llmEntry = S.llms.find(l => l.display_name === ag.llm || l.name === ag.llm);
        if (llmEntry) llmSel.value = llmEntry.name;
    }
}

export async function onLLMChange() {
    const sel = document.getElementById('llmSelect');
    const llm = sel.value;
    const conv = S.conversations.find(c => c.id === S.activeConvId);
    if (!conv || !llm) return;
    conv.llm = llm;
    await fetch('/api/conversations/' + conv.id + '/llm_change', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            llm
        })
    });
}