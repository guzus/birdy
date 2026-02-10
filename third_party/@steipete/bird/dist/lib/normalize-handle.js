const HANDLE_REGEX = /^[A-Za-z0-9_]{1,15}$/;
export function normalizeHandle(input) {
    const raw = (input ?? '').trim();
    if (!raw) {
        return null;
    }
    const withoutAt = raw.startsWith('@') ? raw.slice(1) : raw;
    const handle = withoutAt.trim();
    if (!handle) {
        return null;
    }
    // X/Twitter handles are traditionally max 15 chars; keep strict to avoid surprising queries.
    if (!HANDLE_REGEX.test(handle)) {
        return null;
    }
    return handle;
}
export function mentionsQueryFromUserOption(userOption) {
    if (typeof userOption === 'undefined') {
        return { query: null, error: null };
    }
    const handle = normalizeHandle(userOption);
    if (!handle) {
        return {
            query: null,
            error: 'Invalid --user handle. Expected something like @steipete (letters, digits, underscore; max 15).',
        };
    }
    return { query: `@${handle}`, error: null };
}
//# sourceMappingURL=normalize-handle.js.map