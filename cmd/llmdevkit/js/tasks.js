import { S } from './state.js';
import { esc } from './utils.js';

export function updateTasksFromToolResponse(toolName, content) {
  const lines = content.split('\n').filter(l => l.trim());
  const parseLine = (line) => {
    const m = line.match(/^(\d+(?:\.\d+)*)\.\s*\[([ _Xx])\]\s*(.*)/);
    if (!m) return null;
    const statusChar = m[2];
    let status = 'pending';
    if (statusChar === '_') status = 'in_progress';
    if (statusChar === 'X' || statusChar === 'x') status = 'completed';
    return {id: m[1], content: m[3].trim(), status};
  };

  if (['task_create','task_set_status','task_delete'].includes(toolName)) {
    for (const line of lines) {
      const entry = parseLine(line);
      if (entry) {
        const existing = S.taskEntries.find(t => t.id === entry.id);
        if (existing) { existing.status = entry.status; existing.content = entry.content; }
        else S.taskEntries.push(entry);
      }
    }
  } else if (toolName === 'tasks_list') {
    const parsed = lines.map(parseLine).filter(Boolean);
    if (parsed.length > 0) S.taskEntries = parsed;
  } else if (toolName === 'tasks_clear') {
    S.taskEntries = [];
  }
  renderTaskList();
}

export function renderTaskList() {
  const el = document.getElementById('taskItems');
  const container = document.getElementById('taskList');
  const clearBtn = document.getElementById('tasksClearBtn');
  if (S.taskEntries.length === 0) {
    container.style.display = 'none';
    return;
  }
  container.style.display = '';
  if (clearBtn) {
    clearBtn.onclick = async () => {
      await fetch('/api/tasks/clear', {method: 'POST'});
      S.taskEntries = [];
      renderTaskList();
    };
  }
  const icons = {pending: '○', in_progress: '◐', completed: '●', failed: '✕'};
  const iconCls = {pending: 'text-body-secondary', in_progress: 'text-warning', completed: 'text-success', failed: 'text-danger'};
  el.innerHTML = S.taskEntries.map((t, idx) => {
    const icon = icons[t.status] || '○';
    return `<div class="task-item ${t.status} d-flex align-items-baseline gap-1 small text-body-secondary" style="padding:2px 0">
      <span class="task-icon flex-shrink-0 ${iconCls[t.status] || ''}" style="width:14px;text-align:center">${icon}</span>
      <span class="task-id flex-shrink-0 text-body-secondary" style="font-size:.7rem;opacity:.5">#${esc(t.id)}</span>
      <span class="task-text flex-grow-1 overflow-hidden text-truncate" title="${esc(t.content)}">${esc(t.content)}</span>
      <span class="task-del" style="cursor:pointer;display:none;font-size:.75rem" data-idx="${idx}">✕</span>
    </div>`;
  }).join('');
  el.querySelectorAll('.task-item').forEach(item => {
    const delBtn = item.querySelector('.task-del');
    item.addEventListener('mouseenter', () => { delBtn.style.display = 'inline'; });
    item.addEventListener('mouseleave', () => { delBtn.style.display = 'none'; });
    delBtn.addEventListener('click', async e => {
      e.stopPropagation();
      const idx = parseInt(delBtn.dataset.idx);
      const task = S.taskEntries[idx];
      if (task) {
        const resp = await fetch('/api/tasks/delete', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({id: task.id})
        });
        const data = await resp.json();
        if (data.tasks) {
          const statusMap = {'created': 'pending', 'in_progress': 'in_progress', 'completed': 'completed'};
          S.taskEntries = data.tasks.map(t => ({
            id: t.ID || t.id,
            content: t.Description || t.description || t.content || '',
            status: statusMap[t.Status || t.status] || t.status || 'pending'
          }));
        }
      }
      renderTaskList();
    });
  });
}

export async function rebuildTaskState(conv) {
  S.toolCallIdToName = {};
  S.taskEntries = [];
  // Build toolCallIdToName from conversation messages (still needed for rendering)
  if (conv?.messages) {
    for (const m of conv.messages) {
      if (m.type === 'tool_request') {
        try {
          const parsed = JSON.parse(m.content);
          const tcId = parsed.toolCallId || parsed.toolCallID || parsed.id;
          const tcName = parsed.title || parsed.name || m.name;
          if (tcId) S.toolCallIdToName[tcId] = tcName;
        } catch(e) {}
      }
    }
  }
  // Fetch actual tasks from disk (source of truth)
  try {
    const r = await fetch('/api/tasks');
    const tasks = await r.json();
    if (Array.isArray(tasks) && tasks.length > 0) {
      const statusMap = {'created': 'pending', 'in_progress': 'in_progress', 'completed': 'completed'};
      S.taskEntries = tasks.map(t => ({
        id: t.ID || t.id,
        content: t.Description || t.description || t.content || '',
        status: statusMap[t.Status || t.status] || t.status || 'pending'
      }));
    }
  } catch(e) {}
  renderTaskList();
}
