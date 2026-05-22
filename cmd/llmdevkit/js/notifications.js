// notifications.js -- browser notification bell + service worker management
import {
    S
} from './state.js';

let swReg = null;

export async function initNotifications() {
    const btn = document.getElementById('notifBtn');
    if (!btn) return;

    // Check if SW is already registered
    const existingReg = await navigator.serviceWorker.getRegistration('/sw.js');
    if (existingReg && Notification.permission === 'granted') {
        swReg = existingReg;
        btn.classList.remove('btn-outline-danger');
        btn.classList.add('btn-outline-success');
        btn.title = 'Notifications enabled (click to disable)';
        // Re-send start to ensure polling is active (SW may have been dormant)
        if (existingReg.active) {
            existingReg.active.postMessage({
                type: 'start'
            });
        } else if (existingReg.waiting) {
            existingReg.waiting.postMessage({
                type: 'start'
            });
        } else if (existingReg.installing) {
            existingReg.installing.addEventListener('statechange', function handler() {
                if (existingReg.installing?.state === 'activated' || existingReg.active) {
                    existingReg.active?.postMessage({
                        type: 'start'
                    });
                    existingReg.installing?.removeEventListener('statechange', handler);
                }
            });
        }
    } else {
        btn.classList.remove('btn-outline-success');
        btn.classList.add('btn-outline-danger');
        btn.title = 'Notifications disabled (click to enable)';
    }
}

export async function toggleNotifications() {
    const btn = document.getElementById('notifBtn');
    if (!btn) return;

    if (swReg) {
        // Disable: unregister SW
        await swReg.unregister();
        swReg = null;
        btn.classList.remove('btn-outline-success');
        btn.classList.add('btn-outline-danger');
        btn.title = 'Notifications disabled (click to enable)';
        return;
    }

    // Request permission
    const perm = await Notification.requestPermission();
    if (perm !== 'granted') return;

    // Register service worker
    swReg = await navigator.serviceWorker.register('/sw.js', {
        scope: '/'
    });
    btn.classList.remove('btn-outline-danger');
    btn.classList.add('btn-outline-success');
    btn.title = 'Notifications enabled (click to disable)';

    // Tell SW to start polling -- may need to wait for activation
    const sw = swReg.active || swReg.waiting || swReg.installing;
    if (sw) {
        sw.postMessage({
            type: 'start'
        });
    }
    if (swReg.installing) {
        swReg.installing.addEventListener('statechange', function handler() {
            if (swReg.active) {
                swReg.active.postMessage({
                    type: 'start'
                });
                swReg.installing?.removeEventListener('statechange', handler);
            }
        });
    }

    // Offer test notification
    if (confirm('Notifications enabled! Send a test notification in 10 seconds? (Close the tab to verify it works)')) {
        const sw2 = swReg.active || swReg.waiting || swReg.installing;
        sw2?.postMessage({
            type: 'test'
        });
    }
}