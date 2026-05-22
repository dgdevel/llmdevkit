export function esc(s) {
    if (!s) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function md(text) {
    if (!text) return '';
    try {
        return marked.parse(text);
    } catch (e) {
        return esc(text);
    }
}

export function formatTime(ts) {
    try {
        const d = new Date(ts);
        return d.toLocaleTimeString([], {
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        });
    } catch (e) {
        return '';
    }
}

export function briefText(s) {
    if (!s) return '';
    const line = String(s).split('\n')[0].replace(/\r/g, '');
    return esc(line.length > 100 ? line.slice(0, 100) + '...' : line);
}

export function formatFileSize(bytes) {
    if (!bytes || bytes === 0) return '';
    if (bytes < 1024) return bytes + 'b';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1).replace(/\.0$/, '') + 'Kb';
    return (bytes / (1024 * 1024)).toFixed(1).replace(/\.0$/, '') + 'Mb';
}

export function formatTokenCount(n) {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k';
    return '' + n;
}