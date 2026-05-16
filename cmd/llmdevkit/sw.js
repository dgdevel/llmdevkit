// sw.js — Service Worker for browser notifications
// Polls /api/notifications and shows browser notifications for ask-tool events and conversation completions.

let pollInterval = null;
let lastTimestamp = Date.now() / 1000; // Unix seconds
let origin = self.location.origin;

self.addEventListener('message', (event) => {
  if (event.data?.type === 'start') {
    startPolling();
  }
  if (event.data?.type === 'test') {
    setTimeout(() => {
      self.registration.showNotification('🔔 Test Notification', {
        body: 'Notifications are working! You can close this tab.',
        tag: 'test',
        data: { url: `${origin}/` },
        icon: 'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🤖</text></svg>',
      });
    }, 10000);
  }
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
  startPolling();
});

function startPolling() {
  if (pollInterval) clearInterval(pollInterval);
  lastTimestamp = Date.now() / 1000;
  pollInterval = setInterval(poll, 5000);
  poll();
}

async function poll() {
  try {
    const resp = await fetch(`${origin}/api/notifications?since=${Math.floor(lastTimestamp)}`);
    if (!resp.ok) return;
    const events = await resp.json();
    for (const ev of events) {
      lastTimestamp = Math.max(lastTimestamp, ev.ts || 0);
      showNotification(ev);
    }
  } catch (e) {
    // Network error, retry next poll
  }
}

function showNotification(ev) {
  let title, body, tag;
  switch (ev.event) {
    case 'ask':
      title = 'Agent needs input';
      body = ev.title || 'An agent is waiting for your response.';
      tag = 'ask-' + ev.conv_id;
      break;
    case 'done':
      title = 'Agent finished';
      body = ev.title || 'The agent has completed its response.';
      tag = 'done-' + ev.conv_id;
      break;
    default:
      return;
  }

  self.registration.showNotification(title, {
    body,
    tag,
    data: { url: `${origin}/` },
    icon: 'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🤖</text></svg>',
  });
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const c of clients) {
        if (c.url.startsWith(origin) && 'focus' in c) {
          return c.focus();
        }
      }
      return self.clients.openWindow(origin + '/');
    })
  );
});
