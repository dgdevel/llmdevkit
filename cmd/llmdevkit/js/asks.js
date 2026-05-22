import {
    S
} from './state.js';
import {
    esc,
    md,
    briefText
} from './utils.js';
import {
    bubbleHTML
} from './bubble.js';
import {
    getActiveConv
} from './conversation.js';
import {
    renderMessages
} from './messages.js';

export function handleAskOpenEnded(conv, ev) {
    const msg = {
        type: 'ask_open_ended',
        id: ev.data.ask_id,
        question: ev.data.question,
        answered: false,
        answer: ''
    };
    conv.messages.push(msg);
    if (conv.id === S.activeConvId) renderMessages(conv);
}

export function renderAskOpenEnded(m) {
    if (m.answered) {
        return bubbleHTML('tool-resp', 'Your Answer', esc(m.answer), false, true, briefText(m.answer));
    }
    return `<div class="card border-primary align-self-center" style="max-width:90%" id="ask-${m.id}">
    <div class="card-header"><h6 class="card-title mb-0 small fw-semibold">\uD83D\uDCAC Question</h6></div>
    <div class="card-body">
      <p class="mb-2">${md(m.question)}</p>
      <textarea class="form-control form-control-sm mb-2" id="ask-input-${m.id}" rows="3" placeholder="Your answer..."></textarea>
      <div class="d-flex gap-2">
        <button class="btn btn-sm btn-success" onclick="answerAsk('${m.id}', document.getElementById('ask-input-${m.id}').value)">Submit</button>
      </div>
    </div>
  </div>`;
}

export function handleAskExec(conv, ev) {
    const msg = {
        type: 'ask_exec',
        id: ev.data.ask_id,
        cmdline: ev.data.cmdline,
        timeout: ev.data.timeout || 30,
        answered: false,
        approved: false,
        answer: ''
    };
    conv.messages.push(msg);
    if (conv.id === S.activeConvId) renderMessages(conv);
}

export function renderAskExec(m) {
    if (m.answered) {
        const status = m.approved ? '\u2705 Approved' : '\u274C Denied';
        return bubbleHTML(m.approved ? 'tool-resp' : 'error', `ask_exec ${status}`, esc(m.approved ? m.cmdline : 'Denied: ' + m.answer), false, true, `${status}: ${esc(m.approved ? m.cmdline : m.answer)}`);
    }
    return `<div class="card border-warning align-self-center" style="max-width:90%" id="ask-${m.id}">
    <div class="card-header"><h6 class="card-title mb-0 small fw-semibold">\u26A1 Execute Command</h6></div>
    <div class="card-body">
      <label class="form-label small mb-1 text-body-secondary">Command:</label>
      <input class="form-control form-control-sm mb-2" id="ask-cmd-${m.id}" value="${esc(m.cmdline)}" />
      <label class="form-label small mb-1 text-body-secondary">Timeout (seconds):</label>
      <input class="form-control form-control-sm mb-2" type="number" id="ask-timeout-${m.id}" value="${m.timeout}" min="1" />
      <div class="d-flex gap-2">
        <button class="btn btn-sm btn-success" onclick="answerExec('${m.id}', true)">Approve</button>
        <button class="btn btn-sm btn-outline-danger" onclick="answerExec('${m.id}', false)">Deny</button>
      </div>
      <div class="mt-2 d-none" id="deny-row-${m.id}">
        <label class="form-label small mb-1 text-body-secondary">Reason for denial (optional):</label>
        <input class="form-control form-control-sm mb-1" id="ask-deny-${m.id}" placeholder="Explain to the agent..." />
        <button class="btn btn-sm btn-outline-danger" onclick="submitDeny('${m.id}')">Submit Deny</button>
      </div>
    </div>
  </div>`;
}

export function handleAskMultipleChoice(conv, ev) {
    const msg = {
        type: 'ask_multiple_choice',
        id: ev.data.ask_id,
        question: ev.data.question,
        choices: ev.data.choices || [],
        allow_open_ended: ev.data.allow_open_ended || false,
        answered: false,
        answer: ''
    };
    conv.messages.push(msg);
    if (conv.id === S.activeConvId) renderMessages(conv);
}

export function renderAskMultipleChoice(m) {
    if (m.answered) {
        return bubbleHTML('tool-resp', 'Your Choice', esc(m.answer), false, true, briefText(m.answer));
    }
    const choices = m.choices.map((c, i) =>
        `<button class="btn btn-sm btn-outline-secondary mx-1" onclick="answerChoice('${m.id}', ${i}, this)">${esc(c)}</button>`
    ).join('');
    let openEnd = '';
    if (m.allow_open_ended) {
        openEnd = `<div class="mt-2 d-flex gap-2">
      <input class="form-control form-control-sm flex-grow-1" id="ask-custom-${m.id}" placeholder="Type your own response..." />
      <button class="btn btn-sm btn-outline-secondary" onclick="answerCustom('${m.id}')">Submit</button>
    </div>`;
    }
    return `<div class="card border-info align-self-center" style="max-width:90%" id="ask-${m.id}">
    <div class="card-header"><h6 class="card-title mb-0 small fw-semibold">\u2753 Multiple Choice</h6></div>
    <div class="card-body">
      <p class="mb-2">${md(m.question)}</p>
      <div>${choices}</div>
      ${openEnd}
    </div>
  </div>`;
}

export async function answerAsk(askId, answer) {
    await fetch('/api/ask/' + askId, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            type: 'open_ended',
            answer
        })
    });
    markAskAnswered(askId, false, answer);
}

export function answerExec(askId, approved) {
    if (approved) {
        const cmd = document.getElementById('ask-cmd-' + askId).value;
        const timeout = parseInt(document.getElementById('ask-timeout-' + askId).value) || 30;
        fetch('/api/ask/' + askId, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({
                type: 'exec',
                approved: true,
                cmdline: cmd,
                timeout
            })
        });
        markAskAnswered(askId, true, cmd);
    } else {
        document.getElementById('deny-row-' + askId).classList.remove('d-none');
    }
}

export function submitDeny(askId) {
    const reason = document.getElementById('ask-deny-' + askId).value;
    fetch('/api/ask/' + askId, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            type: 'exec',
            approved: false,
            deny_reason: reason
        })
    });
    markAskAnswered(askId, false, reason);
}

export function answerChoice(askId, idx, btn) {
    const conv = getActiveConv();
    const msg = conv?.messages?.find(m => m.id === askId);
    const choice = msg?.choices?.[idx] || '';
    fetch('/api/ask/' + askId, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            type: 'multiple_choice',
            answer: choice
        })
    });
    markAskAnswered(askId, false, choice);
}

export function answerCustom(askId) {
    const answer = document.getElementById('ask-custom-' + askId)?.value || '';
    if (!answer) return;
    fetch('/api/ask/' + askId, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            type: 'multiple_choice',
            answer
        })
    });
    markAskAnswered(askId, false, answer);
}

export function markAskAnswered(askId, approved, answer) {
    S.conversations.forEach(c => {
        const m = c.messages?.find(m => m.id === askId);
        if (m) {
            m.answered = true;
            m.approved = approved;
            m.answer = answer || '';
        }
    });
    const conv = getActiveConv();
    if (conv) renderMessages(conv);
}