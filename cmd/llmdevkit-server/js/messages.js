import { S } from './state.js';
import { esc, md, briefText } from './utils.js';
import { bubbleHTML, clearLazyContent, populateLazy } from './bubble.js';
import { renderAskOpenEnded, renderAskExec, renderAskMultipleChoice } from './asks.js';

export function renderMessages(conv) {
  const el = document.getElementById('messages');
  el.innerHTML = '';
  clearLazyContent();
  S._bubbleId = 0;
  if (!conv) return;
  if (conv.system_prompt) {
    const spBrief = briefText(conv.system_prompt);
    el.innerHTML += bubbleHTML('system', 'System Prompt', md(conv.system_prompt), true, true, spBrief);
  }
  const defs = S.toolDefsCache[conv.agent] || [];
  if (defs.length > 0) {
    let toolsHTML = defs.map(t => {
      let desc = t.description ? `<div class="text-body-secondary small">${esc(t.description)}</div>` : '';
      let args = '';
      if (t.parameters && t.parameters.properties) {
        const props = t.parameters.properties;
        const required = t.parameters.required || [];
        args = Object.entries(props).map(([name, schema]) => {
          const req = required.includes(name) ? ' *' : '';
          const typ = schema.type || 'any';
          const d = schema.description ? ` — ${esc(schema.description)}` : '';
          return `<span class="badge font-monospace text-bg-secondary me-1">${esc(name)}:${typ}${req}${d}</span>`;
        }).join('');
      }
      return `<div class="border rounded p-2 mb-1">
        <span class="fw-semibold font-monospace small text-info">${esc(t.name)}</span>
        ${desc}
        <div class="mt-1">${args}</div>
      </div>`;
    }).join('');
    el.innerHTML += bubbleHTML('tools', `Available Tools (${defs.length})`, toolsHTML, true, true, `${defs.length} tools`);
  } else if (conv.tools && conv.tools.length > 0) {
    const toolsHTML = conv.tools.map(t => `<span class="badge text-bg-secondary me-1">${esc(t)}</span>`).join(' ');
    el.innerHTML += bubbleHTML('tools', 'Available Tools', toolsHTML, true, true, conv.tools.join(', '));
  }
  (conv.messages || []).forEach(m => {
    el.innerHTML += renderBubble(m);
  });
  scrollToBottom();
}

export function scrollToBottom() {
  if (!document.getElementById('autoscrollCheck').checked) return;
  const el = document.getElementById('messages');
  el.scrollTop = el.scrollHeight;
}

export function renderBubble(m) {
  switch(m.type) {
    case 'user': return bubbleHTML('user', 'You', md(m.content), false, false, null, m.timestamp);
    case 'llm': return bubbleHTML('llm', 'Assistant Response', md(m.content), false, false, null, m.timestamp, m.token_count);
    case 'thinking': return bubbleHTML('thinking', 'Assistant Thinking', md(m.content), false, true, briefText(m.content), m.timestamp);
    case 'tool_request': return renderToolRequest(m);
    case 'tool_response': return renderToolResponse(m);
    case 'ask_open_ended': return renderAskOpenEnded(m);
    case 'ask_exec': return renderAskExec(m);
    case 'ask_multiple_choice': return renderAskMultipleChoice(m);
    case 'error': return bubbleHTML('error', 'Error', esc(m.content), false, true, briefText(m.content), m.timestamp);
    default: return bubbleHTML('system', m.type, esc(m.content||''), false, true, briefText(m.content), m.timestamp);
  }
}

function renderToolRequest(m) {
  let title = esc(m.name || 'Tool');
  let argsHTML = '';
  let parsed = null;
  try { parsed = JSON.parse(m.content); } catch(e) {}
  if (parsed) {
    if (parsed.title) title = esc(parsed.title);
    else if (parsed.name) title = esc(parsed.name);
    const argEntries = [];
    let args = null;
    if (parsed.arguments && typeof parsed.arguments === 'object') args = parsed.arguments;
    else if (parsed.input && typeof parsed.input === 'object') args = parsed.input;
    else if (parsed.rawInput && typeof parsed.rawInput === 'object') args = parsed.rawInput;
    else if (typeof parsed.rawInput === 'string') { try { args = JSON.parse(parsed.rawInput); } catch(e2) {} }
    else if (parsed.params && typeof parsed.params === 'object') args = parsed.params;
    if (!args && typeof parsed === 'object') {
      const skip = new Set(['title','name','toolCallId','toolCallID','id','status','kind','content','_meta','locations','rawOutput','rawInput','arguments','input']);
      const candidates = Object.keys(parsed).filter(k => !skip.has(k));
      if (candidates.length > 0) {
        args = {};
        candidates.forEach(k => args[k] = parsed[k]);
      }
    }
    if (args) {
      for (const [k, v] of Object.entries(args)) {
        const val = typeof v === 'string' ? esc(v) : esc(JSON.stringify(v, null, 2));
        argEntries.push(`<tr><td class="arg-name">${esc(k)}</td><td class="arg-val">${val}</td></tr>`);
      }
    }
    if (argEntries.length > 0) {
      argsHTML = `<table class="tool-args-table">${argEntries.join('')}</table>`;
    }
  }
  const brief = briefToolReq(m);
  return bubbleHTML('tool-req', `⚙ ${title}`, argsHTML, true, false, brief, m.timestamp);
}

function renderToolResponse(m) {
  let name = m.name || '';
  if (m.id && S.toolCallIdToName[m.id]) name = S.toolCallIdToName[m.id];
  else if (S.toolCallIdToName[name]) name = S.toolCallIdToName[name];
  const brief = briefText(m.content);
  const content = `<pre>${esc(m.content)}</pre>`;
  return bubbleHTML('tool-resp', `📋 ${esc(name)}`, content, true, true, brief, m.timestamp);
}

function briefToolReq(m) {
  try {
    const obj = JSON.parse(m.content);
    const name = obj.title || m.name || '';
    let args = '';
    let argsObj = null;
    if (obj.arguments && typeof obj.arguments === 'object') argsObj = obj.arguments;
    else if (obj.input && typeof obj.input === 'object') argsObj = obj.input;
    else if (obj.rawInput && typeof obj.rawInput === 'object') argsObj = obj.rawInput;
    if (argsObj) {
      args = Object.entries(argsObj).map(([k,v]) => `${esc(k)}: ${esc(String(v).substring(0,50))}`).join(' | ');
    } else if (obj.input_schema && obj.input_schema.properties) {
      args = Object.keys(obj.input_schema.properties).map(k => esc(k)).join(', ');
    } else if (!args && m.content) {
      const fallback = m.content.split('\n')[0];
      return esc(fallback.length > 100 ? fallback.slice(0,100)+'…' : fallback);
    }
    return esc(name) + (args ? ' | ' + args : '');
  } catch(e) {
    return briefText(m.content);
  }
}
