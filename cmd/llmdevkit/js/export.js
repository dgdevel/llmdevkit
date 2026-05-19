import { S } from './state.js';
import { lazyContent } from './bubble.js';

// Export all lazy content into the DOM so it's visible in the snapshot
function expandAllLazy() {
  const messages = document.getElementById('messages');
  if (!messages) return;
  // Populate any remaining lazy content
  messages.querySelectorAll('.collapse').forEach(collapseEl => {
    const id = collapseEl.id;
    const html = lazyContent.get(id);
    if (html !== undefined) {
      const contentEl = collapseEl.querySelector('.bubble-content');
      if (contentEl && !contentEl.innerHTML.trim()) {
        contentEl.innerHTML = html;
        lazyContent.delete(id);
      }
    }
  });
  // Expand all collapsed elements: add 'show' class and set aria-expanded
  messages.querySelectorAll('.collapse:not(.show)').forEach(collapseEl => {
    collapseEl.classList.add('show');
    const toggle = collapseEl.getAttribute('data-bs-target');
    if (toggle) {
      const target = document.querySelector(toggle);
      if (target) target.setAttribute('aria-expanded', 'true');
    }
  });
}

export function exportConversation() {
  const conv = S.conversations.find(c => c.id === S.activeConvId);
  if (!conv) return;

  // Step 1: expand all lazy content
  expandAllLazy();

  // Step 2: capture messages HTML
  const messagesEl = document.getElementById('messages');
  if (!messagesEl) return;
  const messagesHTML = messagesEl.innerHTML;

  // Step 3: build standalone HTML
  const cssLink = `<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.8/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-sRIl4kxILFvY47J16cr9ZwB07vP4J8+LH7qKQnuqkuIAvNWLzeN8tE5YBujZqJLB" crossorigin="anonymous">`;
  const bootstrapJS = `<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.8/dist/js/bootstrap.bundle.min.js" integrity="sha384-FKyoEForCGlyvX9Hj09JcYn3nv7wiPVlz7YYwJrWVcXK/BmnVDxM+D2scQbITxI" crossorigin="anonymous"><\/script>`;
  const markedJS = `<script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"><\/script>`;

  const standaloneHTML = `<!DOCTYPE html>
<html lang="en" data-bs-theme="dark">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LLM DevKit - ${esc(conv.title || 'Conversation')}</title>
${cssLink}
<style>
html,body{height:100%;overflow:auto;background:var(--bs-body-bg)}
.border-dashed{border-style:dashed!important}
.chat-messages{padding:1rem;display:flex;flex-direction:column;gap:.5rem;overflow:visible}
.bubble-content p{margin:.25rem 0}
.bubble-content ul,.bubble-content ol{margin:.25rem 0 .25rem 1.25rem}
.bubble-content h1,.bubble-content h2,.bubble-content h3,.bubble-content h4{margin:.5rem 0 .25rem}
.bubble-content pre{padding:.5rem;border-radius:.25rem;overflow-x:auto;margin:.375rem 0;font-size:.8125rem}
.bubble-content code{padding:1px 4px;border-radius:.1875rem;font-size:.8125rem}
.bubble-content pre code{background:none;padding:0}
.bubble-content table{border-collapse:collapse;margin:.375rem 0}
.bubble-content th,.bubble-content td{border:1px solid var(--bs-border-color);padding:.25rem .5rem;font-size:.8125rem}
.bubble-content th{background:var(--bs-tertiary-bg)}
.tool-args-table{border-collapse:collapse;margin:.375rem 0;width:100%}
.tool-args-table td{padding:.125rem .5rem;font-size:.75rem;border-bottom:1px solid var(--bs-border-color)}
.tool-args-table .arg-name{font-family:monospace;font-weight:600;white-space:nowrap;width:1%}
.tool-args-table .arg-val{font-family:monospace;white-space:pre-wrap;word-break:break-all}
.bubble-user{align-self:flex-end!important;max-width:85%!important}
</style>
</head>
<body>
<div class="chat-messages" id="messages">
${messagesHTML}
</div>
${markedJS}
${bootstrapJS}
<script>
// Re-mark any bubble-content that contains raw markdown but no HTML tags
document.querySelectorAll('.bubble-content').forEach(el => {
  if (!el.querySelector('pre, code, table, ul, ol, h1, h2, h3, h4, p') && el.textContent.trim()) {
    // Try to parse as markdown if it looks like raw text
    try {
      const parsed = marked.parse(el.textContent);
      if (parsed.includes('<')) {
        el.innerHTML = parsed;
      }
    } catch(e) {}
  }
});
<\/script>
</body>
</html>`;

  // Step 4: download
  const blob = new Blob([standaloneHTML], { type: 'text/html' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `llmdevkit-${conv.id}.html`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function esc(s) {
  if (!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
