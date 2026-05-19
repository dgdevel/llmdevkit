import { esc, formatTime, formatTokenCount } from './utils.js';
import { S } from './state.js';

// Lazy content store: bubble ID → HTML string
const lazyContent = new Map();

export { lazyContent };

export function bubbleHTML(cls, label, content, raw, collapsed, brief, timestamp, tokenCount) {
  const id = 'bc-' + (S._bubbleId++);
  const briefHtml = (collapsed && brief) ? `<span class="brief-badge badge text-bg-secondary fw-normal text-truncate ms-2" style="max-width:250px">${brief}</span>` : '';
  let tsHtml = '';
  if (timestamp) {
    tsHtml = `<small class="text-body-secondary ms-auto">${formatTime(timestamp)}`;
    if (tokenCount && tokenCount > 0) tsHtml += ` <span class="text-info">• ${esc(formatTokenCount(tokenCount))} tok</span>`;
    tsHtml += '</small>';
  } else if (tokenCount && tokenCount > 0) {
    tsHtml = `<small class="text-body-secondary ms-auto"><span class="text-info">${esc(formatTokenCount(tokenCount))} tok</span></small>`;
  }

  let cardCls = 'card bubble-' + cls;
  let align = 'align-self-start';
  let maxW = '85%';

  switch(cls) {
    case 'system':    cardCls += ' border-secondary fst-italic'; align = 'align-self-center'; maxW = '95%'; break;
    case 'tools':     cardCls += ' border-info';      align = 'align-self-center'; maxW = '95%'; break;
    case 'user':      cardCls += ' text-bg-primary';  align = 'align-self-end';    maxW = '85%'; break;
    case 'llm':       break;
    case 'thinking':  cardCls += ' border-secondary border-dashed bg-transparent'; break;
    case 'tool-req':  cardCls += ' border-start border-warning'; maxW = '90%'; break;
    case 'tool-resp': cardCls += ' border-start border-success'; maxW = '90%'; break;
    case 'error':     cardCls += ' border-danger text-danger';   align = 'align-self-center'; maxW = '90%'; break;
  }

  const cursor = collapsed ? 'cursor:pointer;' : '';
  const toggle = collapsed ? `data-bs-toggle="collapse" data-bs-target="#${id}"` : '';
  const header = `<div class="card-header d-flex align-items-center py-1 px-3 gap-2" ${toggle} style="${cursor}">
    <h6 class="card-title mb-0 small fw-semibold">${label}</h6>
    ${briefHtml}${tsHtml}
  </div>`;

  let body;
  if (collapsed) {
    // Lazy: store content, render empty placeholder
    lazyContent.set(id, content);
    body = `<div class="collapse" id="${id}"><div class="card-body bubble-content py-2 px-3 small"></div></div>`;
  } else {
    body = `<div class="card-body bubble-content py-2 px-3 small">${content}</div>`;
  }

  return `<div class="${cardCls} ${align}" style="max-width:${maxW}">${header}${body}</div>`;
}

export function clearLazyContent() {
  lazyContent.clear();
}

// Fill lazy content into a bubble-content element
export function populateLazy(el) {
  const collapseEl = el.closest('.collapse');
  if (!collapseEl) return;
  const id = collapseEl.id;
  const html = lazyContent.get(id);
  if (html !== undefined) {
    el.innerHTML = html;
    lazyContent.delete(id);
  }
}

// Initialize lazy-load on collapse show events
export function initLazyExpand() {
  document.getElementById('messages').addEventListener('show.bs.collapse', e => {
    const contentEl = e.target.querySelector('.bubble-content');
    if (contentEl && !contentEl.innerHTML.trim()) {
      populateLazy(contentEl);
    }
  });
}
