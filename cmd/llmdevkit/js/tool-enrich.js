import { S } from './state.js';
import { getActiveConv, loadToolDefs } from './conversation.js';
import { sendPromptWithTools } from './prompt.js';

let selectedTools = [];
let toolDefs = [];
let currentToolDef = null;

export function initToolEnrichModal() {
  S.toolEnrichModalEl = new bootstrap.Modal(document.getElementById('toolEnrichModal'));

  document.getElementById('toolEnrichSelect').addEventListener('change', onToolSelect);
  document.getElementById('toolEnrichAddBtn').addEventListener('click', onAddToolCall);
  document.getElementById('toolEnrichSendBtn').addEventListener('click', onSend);
}

export async function openToolEnrichModal() {
  const conv = getActiveConv();
  if (!conv) return;

  // Load tool definitions for the current agent
  try {
    toolDefs = await loadToolDefs(conv.agent) || [];
  } catch (e) {
    toolDefs = [];
  }

  const sel = document.getElementById('toolEnrichSelect');
  sel.innerHTML = '<option value="">— Select a tool —</option>';
  for (const t of toolDefs) {
    const opt = document.createElement('option');
    opt.value = t.name;
    opt.textContent = t.description ? `${t.name} — ${t.description}` : t.name;
    sel.appendChild(opt);
  }

  selectedTools = [];
  currentToolDef = null;
  renderSelectedTools();
  clearArgForm();
  S.toolEnrichModalEl.show();
}

function onToolSelect() {
  const name = document.getElementById('toolEnrichSelect').value;
  currentToolDef = toolDefs.find(t => t.name === name) || null;
  renderArgForm();
}

function renderArgForm() {
  const container = document.getElementById('toolEnrichArgs');
  container.innerHTML = '';

  if (!currentToolDef) return;

  const params = currentToolDef.parameters;
  if (!params || !params.properties) return;

  const required = new Set(params.required || []);

  for (const [key, schema] of Object.entries(params.properties)) {
    const div = document.createElement('div');
    div.className = 'mb-2';

    const label = document.createElement('label');
    label.className = 'form-label small text-body-secondary mb-1';
    const desc = schema.description ? ` — ${schema.description}` : '';
    const reqMark = required.has(key) ? ' *' : '';
    label.textContent = `${key}${reqMark}${desc}`;
    div.appendChild(label);

    if (schema.enum) {
      const sel = document.createElement('select');
      sel.className = 'form-select form-select-sm';
      sel.dataset.argName = key;
      const defOpt = document.createElement('option');
      defOpt.value = '';
      defOpt.textContent = '— select —';
      sel.appendChild(defOpt);
      for (const v of schema.enum) {
        const o = document.createElement('option');
        o.value = v;
        o.textContent = v;
        sel.appendChild(o);
      }
      div.appendChild(sel);
    } else if (schema.type === 'boolean') {
      const check = document.createElement('div');
      check.className = 'form-check';
      const cb = document.createElement('input');
      cb.className = 'form-check-input';
      cb.type = 'checkbox';
      cb.dataset.argName = key;
      const cl = document.createElement('label');
      cl.className = 'form-check-label small';
      cl.textContent = key;
      check.appendChild(cb);
      check.appendChild(cl);
      div.appendChild(check);
    } else if (schema.type === 'number' || schema.type === 'integer') {
      const inp = document.createElement('input');
      inp.className = 'form-control form-control-sm';
      inp.type = 'number';
      if (schema.type === 'integer') inp.step = '1';
      inp.dataset.argName = key;
      inp.placeholder = schema.description || key;
      div.appendChild(inp);
    } else {
      const inp = document.createElement('textarea');
      inp.className = 'form-control form-control-sm';
      inp.rows = 2;
      inp.dataset.argName = key;
      inp.placeholder = schema.description || key;
      div.appendChild(inp);
    }
    container.appendChild(div);
  }
}

function clearArgForm() {
  document.getElementById('toolEnrichArgs').innerHTML = '';
  document.getElementById('toolEnrichSelect').value = '';
  currentToolDef = null;
}

function collectArgs() {
  const args = {};
  const inputs = document.querySelectorAll('#toolEnrichArgs [data-arg-name]');
  for (const el of inputs) {
    const name = el.dataset.argName;
    if (el.type === 'checkbox') {
      args[name] = el.checked;
    } else if (el.tagName === 'SELECT') {
      if (el.value) args[name] = el.value;
    } else if (el.type === 'number') {
      if (el.value !== '') args[name] = Number(el.value);
    } else {
      if (el.value.trim() !== '') args[name] = el.value;
    }
  }
  return args;
}

function onAddToolCall() {
  if (!currentToolDef) return;
  const args = collectArgs();
  selectedTools.push({ name: currentToolDef.name, arguments: args });
  renderSelectedTools();
  clearArgForm();
}

function removeToolCall(idx) {
  selectedTools.splice(idx, 1);
  renderSelectedTools();
}

function renderSelectedTools() {
  const list = document.getElementById('toolEnrichList');
  list.innerHTML = '';
  if (selectedTools.length === 0) {
    list.innerHTML = '<div class="text-body-secondary small">No tool calls added yet.</div>';
    return;
  }
  for (let i = 0; i < selectedTools.length; i++) {
    const tc = selectedTools[i];
    const div = document.createElement('div');
    div.className = 'd-flex align-items-center gap-2 mb-1 p-1 border rounded small';

    const argsStr = Object.entries(tc.arguments).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(', ');
    const span = document.createElement('span');
    span.className = 'flex-grow-1 text-truncate';
    span.innerHTML = `<strong>${escHtml(tc.name)}</strong> <span class="text-body-secondary">(${escHtml(argsStr)})</span>`;
    div.appendChild(span);

    const btn = document.createElement('button');
    btn.className = 'btn btn-sm btn-outline-danger py-0 px-1';
    btn.innerHTML = '✕';
    btn.title = 'Remove';
    const idx = i;
    btn.addEventListener('click', () => removeToolCall(idx));
    div.appendChild(btn);

    list.appendChild(div);
  }
}

function escHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

async function onSend() {
  if (selectedTools.length === 0) {
    S.toolEnrichModalEl.hide();
    return;
  }
  const toolCalls = [...selectedTools];
  S.toolEnrichModalEl.hide();
  await sendPromptWithTools(toolCalls);
}
