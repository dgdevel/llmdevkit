// sw.js — Service Worker for browser notifications
// Polls /api/notifications and shows browser notifications for ask-tool events and conversation completions.
// Robust against server restarts: uses exponential backoff on failures, auto-recovers when server returns.

const BASE_INTERVAL = 5000;   // normal poll interval in ms
const MAX_INTERVAL  = 60000;  // max backoff interval in ms
const BACKOFF_FACTOR = 2;

let pollInterval = null;
let currentInterval = BASE_INTERVAL;
let consecutiveFailures = 0;
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
  // Don't reset lastTimestamp — preserve it so we catch buffered events
  // after server restart (server keeps last 100 events in ring buffer).
  consecutiveFailures = 0;
  currentInterval = BASE_INTERVAL;
  pollInterval = setInterval(poll, currentInterval);
  poll();
}

function scheduleNext() {
  clearInterval(pollInterval);
  pollInterval = setInterval(poll, currentInterval);
}

async function poll() {
  try {
    const resp = await fetch(`${origin}/api/notifications?since=${Math.floor(lastTimestamp)}`);
    if (!resp.ok) {
      onPollError();
      return;
    }
    const events = await resp.json();
    onPollSuccess();
    for (const ev of events) {
      lastTimestamp = Math.max(lastTimestamp, ev.ts || 0);
      showNotification(ev);
    }
  } catch (e) {
    // Network error (server down or unreachable)
    onPollError();
  }
}

function onPollSuccess() {
  if (consecutiveFailures > 0) {
    consecutiveFailures = 0;
    currentInterval = BASE_INTERVAL;
    scheduleNext();
  }
}

function onPollError() {
  consecutiveFailures++;
  const newInterval = Math.min(BASE_INTERVAL * Math.pow(BACKOFF_FACTOR, consecutiveFailures - 1), MAX_INTERVAL);
  if (newInterval !== currentInterval) {
    currentInterval = newInterval;
    scheduleNext();
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
  const targetUrl = event.notification.data?.url || `${origin}/`;
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const c of clients) {
        if (c.url.startsWith(origin) && 'focus' in c) {
          return c.focus();
        }
      }
      return self.clients.openWindow(targetUrl);
    })
  );
});
