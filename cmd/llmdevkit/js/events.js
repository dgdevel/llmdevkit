import { S } from './state.js';
import { renderConvList, selectConversation } from './sidebar.js';
import { getActiveConv } from './conversation.js';
import { renderMessages, scrollToBottom, updateLastLLMTokenCount } from './messages.js';
import { updateState } from './state-ui.js';
import { scheduleRender, updateStreamingBubble } from './prompt.js';
import { updateTasksFromToolResponse, renderTaskList } from './tasks.js';
import { handleAskOpenEnded, handleAskExec, handleAskMultipleChoice } from './asks.js';
import { updateQueueFromSSE } from './queue.js';

export function connectSSE() {
  const es = new EventSource('/api/events');
  es.onmessage = (e) => {
    const data = JSON.parse(e.data);
    handleEvent(data);
  };
  es.onerror = () => {
    setTimeout(connectSSE, 3000);
  };
}

function handleEvent(ev) {
  if (ev.event === 'conversation_created') {
    if (!S.conversations.find(c => c.id === ev.data?.id)) {
      S.conversations.unshift(ev.data);
      renderConvList();
    }
    return;
  }
  if (ev.event === 'conversation_deleted') {
    S.conversations = S.conversations.filter(c => c.id !== ev.data?.id);
    if (S.activeConvId === ev.data?.id) {
      S.activeConvId = S.conversations[0]?.id || null;
    }
    renderConvList();
    if (S.activeConvId) selectConversation(S.activeConvId);
    return;
  }
  if (ev.event === 'conversation_updated') {
    const idx = S.conversations.findIndex(c => c.id === ev.data?.id);
    if (idx >= 0) {
      const local = S.conversations[idx];
      const remote = ev.data;
      Object.assign(local, {
        title: remote.title,
        agent: remote.agent,
        llm: remote.llm,
        system_prompt: remote.system_prompt,
        tools: remote.tools,
        acp_session_id: remote.acp_session_id,
        file_size: remote.file_size
      });
    }
    renderConvList();
    if (S.activeConvId === ev.data?.id) {
      renderMessages(getActiveConv());
    }
    updateState(getActiveConv());
    return;
    }

  const conv = S.conversations.find(c => c.id === ev.conversation_id);
  if (!conv) return;
  switch(ev.event) {
    case 'session_update':
      handleSessionUpdate(conv, ev.data);
      break;
    case 'ask_open_ended':
      handleAskOpenEnded(conv, ev);
      break;
    case 'ask_exec':
      handleAskExec(conv, ev);
      break;
    case 'ask_multiple_choice':
      handleAskMultipleChoice(conv, ev);
      break;
    case 'state':
      conv.running = ev.data?.running || false;
      S.running = conv.running;
      if (ev.data && !ev.data.running) {
        S.taskEntries = S.taskEntries.map(t => t.status === 'in_progress' ? {...t, status: 'completed'} : t);
        renderTaskList();
      }
      updateState(conv);
      renderConvList();
      break;
    case 'queue_update':
      conv.queue = ev.data || [];
      S.messageQueue = conv.queue;
      if (conv.id === S.activeConvId) updateQueueFromSSE(conv.queue);
      break;
    case 'token_stats':
      if (ev.data && ev.data.total_tokens) {
        const tokens = ev.data.total_tokens || 0;
        for (let i = conv.messages.length - 1; i >= 0; i--) {
          if (conv.messages[i].type === 'llm') {
            conv.messages[i].token_count = tokens;
            break;
          }
        }
        if (conv.id === S.activeConvId) updateLastLLMTokenCount(tokens);
      }
      break;
    case 'tool_request_update':
      if (ev.data && ev.data.toolCallId && ev.data.rawInput) {
        for (let i = conv.messages.length - 1; i >= 0; i--) {
          const m = conv.messages[i];
          if (m.type === 'tool_request') {
            try {
              const parsed = JSON.parse(m.content);
              const tcId = parsed.toolCallId || parsed.toolCallID || parsed.id;
              if (tcId === ev.data.toolCallId) {
                parsed.rawInput = JSON.parse(ev.data.rawInput);
                m.content = JSON.stringify(parsed);
                conv._renderedMsgCount = 0; // force full rebuild
                break;
              }
            } catch(e) {}
          }
        }
      }
      break;
  }
  if (conv.id === S.activeConvId) {
    scheduleRender(conv);
  }
}

function handleSessionUpdate(conv, bubble) {
  if (!bubble || !bubble.type) return;
  if (bubble.type === 'ask_open_ended' || bubble.type === 'ask_exec' || bubble.type === 'ask_multiple_choice') return;
  if (bubble.type === 'llm' || bubble.type === 'thinking') {
    const last = conv.messages[conv.messages.length - 1];
    if (last && last.type === bubble.type && last._merging) {
      last.content += bubble.content;
      if (conv.id === S.activeConvId && updateStreamingBubble(conv)) {
        if (!S._streamUpdateScheduled) {
          S._streamUpdateScheduled = true;
          requestAnimationFrame(() => {
            S._streamUpdateScheduled = false;
            scrollToBottom();
          });
        }
        return;
      }
    } else {
      conv.messages.push({type: bubble.type, content: bubble.content, _merging: true, timestamp: bubble.timestamp, token_count: bubble.token_count});
    }
    if (conv.id === S.activeConvId) scheduleRender(conv);
    return;
  }
  if (bubble.type === 'tool_request') {
    conv.messages.push({type:'tool_request', name: bubble.name, content: bubble.content, timestamp: bubble.timestamp});
    try {
      const parsed = JSON.parse(bubble.content);
      const tcId = parsed.toolCallId || parsed.toolCallID || parsed.id;
      const tcName = parsed.title || parsed.name || bubble.name;
      if (tcId) S.toolCallIdToName[tcId] = tcName;
    } catch(e) {}
    return;
  }
  if (bubble.type === 'tool_response') {
    conv.messages.push({type:'tool_response', name: bubble.name, id: bubble.id, content: bubble.content, timestamp: bubble.timestamp});
    const lookupKey = bubble.id || bubble.name;
    const realToolName = S.toolCallIdToName[lookupKey] || bubble.name;
    updateTasksFromToolResponse(realToolName, bubble.content);
    return;
  }
  if (bubble.type === 'error') {
    conv.messages.push({type:'error', content: bubble.content, timestamp: bubble.timestamp});
    return;
  }
  conv.messages.push(bubble);
}
