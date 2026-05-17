import { S } from './state.js';
import { md, briefText } from './utils.js';
import { renderConvList } from './sidebar.js';
import { getActiveConv, loadToolDefs, newConversation } from './conversation.js';
import { updateState } from './state-ui.js';
import { renderMessages, scrollToBottom } from './messages.js';
import { renderTaskList } from './tasks.js';
import { populateLazy } from './bubble.js';
import { enqueuePrompt } from './queue.js';

export function scheduleRender(conv) {
  if (S._renderScheduled) return;
  S._renderScheduled = true;
  requestAnimationFrame(() => {
    S._renderScheduled = false;
    const c = conv || getActiveConv();
    if (c) {
      renderMessages(c, false);
    }
  });
}

export function updateStreamingBubble(conv) {
  const el = document.getElementById('messages');
  const last = conv.messages[conv.messages.length - 1];
  if (!last || (last.type !== 'llm' && last.type !== 'thinking')) return false;
  const bubbles = el.querySelectorAll('.bubble-' + last.type);
  const lastBubble = bubbles[bubbles.length - 1];
  if (!lastBubble) return false;
  const contentEl = lastBubble.querySelector('.bubble-content');
  if (!contentEl) return false;
  // Populate lazy content before streaming update
  if (!contentEl.innerHTML.trim()) populateLazy(contentEl);
  contentEl.innerHTML = md(last.content);
  const briefEl = lastBubble.querySelector('.brief-badge');
  if (briefEl) briefEl.textContent = briefText(last.content);
  return true;
}

export function autoGrow(el) {
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, 200) + 'px';
}

export function onPromptKey(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    sendPrompt();
  }
}

export async function sendPrompt() {
  const input = document.getElementById('promptInput');
  const text = input.value.trim();
  if (!text) return;
  input.value = '';
  input.style.height = 'auto';
  await doSendPrompt(text, []);
}

export async function sendPromptWithTools(toolCalls) {
  const input = document.getElementById('promptInput');
  const text = input.value.trim();
  if (!text && toolCalls.length === 0) return;
  input.value = '';
  input.style.height = 'auto';
  await doSendPrompt(text, toolCalls);
}

async function doSendPrompt(text, toolCalls) {
  let conv = getActiveConv();
  if (!conv) {
    await newConversation();
    conv = getActiveConv();
    if (!conv) return;
  }

  if (!conv.acp_session_id) {
    const body = {prompt: text};
    if (toolCalls && toolCalls.length > 0) body.tool_calls = toolCalls;
    const initR = await fetch('/api/conversations/' + conv.id + '/init', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify(body)
    });
    const data = await initR.json();
    if (data.error) { alert(data.error); return; }
    conv = data.conversation || conv;
    const idx = S.conversations.findIndex(c => c.id === conv.id);
    if (idx >= 0) S.conversations[idx] = conv;
    S.running = true;
    S.taskEntries = [];
    S.toolCallIdToName = {};
    renderTaskList();
    if (conv.agent) await loadToolDefs(conv.agent);
    renderMessages(conv);
    renderConvList();
    updateState(conv);
    return;
  }

  // If conversation is running, enqueue the message instead
  if (conv.running || S.running) {
    await enqueuePrompt(text);
    return;
  }

  conv.messages.push({type:'user', content: text});
  conv.running = true;
  renderMessages(conv);
  S.running = true;
  S.taskEntries = [];
  S.toolCallIdToName = {};
  renderTaskList();
  renderConvList();
  updateState(conv);

  try {
    const body = {prompt: text};
    if (toolCalls && toolCalls.length > 0) body.tool_calls = toolCalls;
    const r = await fetch('/api/conversations/' + conv.id + '/prompt', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify(body)
    });
    const data = await r.json();
    if (data.conversation) {
      Object.assign(conv, data.conversation);
    }
    if (data.error) {
      conv.messages.push({type:'error', content: data.error});
      conv.running = false;
      S.running = false;
    }
  } catch(e) {
    conv.messages.push({type:'error', content: 'Fetch error: ' + e.message});
    conv.running = false;
    S.running = false;
  }
  renderMessages(conv);
  renderConvList();
  updateState(conv);
  scrollToBottom();
}

export async function cancelSession() {
  const conv = getActiveConv();
  if (!conv) return;
  await fetch('/api/conversations/' + conv.id + '/cancel', {method:'POST'});
  conv.running = false;
  S.running = false;
  S.taskEntries = S.taskEntries.map(t => t.status === 'in_progress' ? {...t, status: 'completed'} : t);
  renderTaskList();
  renderConvList();
  updateState(conv);
}

export async function undoLastPrompt() {
  const conv = getActiveConv();
  if (!conv) return;
  if (conv.running) { alert('Cannot undo while conversation is running.'); return; }
  const hasUser = (conv.messages || []).some(m => m.type === 'user');
  if (!hasUser) { alert('Nothing to undo.'); return; }
  if (!confirm('Delete conversation from the last user message onward?')) return;
  try {
    const r = await fetch('/api/conversations/' + conv.id + '/undo', {method:'POST'});
    if (!r.ok) {
      const data = await r.json().catch(() => ({}));
      alert(data.error || 'Undo failed');
      return;
    }
    const data = await r.json();
    if (data.messages) {
      Object.assign(conv, data);
    }
    S.taskEntries = [];
    S.toolCallIdToName = {};
    renderMessages(conv);
    renderConvList();
    updateState(conv);
    scrollToBottom();
  } catch(e) {
    alert('Undo error: ' + e.message);
  }
}

export async function trimConversation() {
  const conv = getActiveConv();
  if (!conv) return;
  if (conv.running) { alert('Cannot trim while conversation is running.'); return; }
  const hasTools = (conv.messages || []).some(m => m.type === 'tool_request' || m.type === 'tool_response');
  if (!hasTools) { alert('No tool calls or tool replies to trim.'); return; }
  if (!confirm('Remove all tool calls and tool replies from conversation history? User prompts, thinking blocks, and LLM responses will be kept.')) return;
  try {
    const r = await fetch('/api/conversations/' + conv.id + '/trim', {method:'POST'});
    if (!r.ok) {
      const data = await r.json().catch(() => ({}));
      alert(data.error || 'Trim failed');
      return;
    }
    const data = await r.json();
    if (data.messages) {
      Object.assign(conv, data);
    }
    S.taskEntries = [];
    S.toolCallIdToName = {};
    renderMessages(conv);
    renderConvList();
    updateState(conv);
    scrollToBottom();
  } catch(e) {
    alert('Trim error: ' + e.message);
  }
}
