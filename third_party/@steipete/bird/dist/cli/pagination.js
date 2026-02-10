export function parsePositiveIntFlag(raw, flagName) {
    if (raw === undefined) {
        return { ok: true, value: undefined };
    }
    const value = Number.parseInt(raw, 10);
    if (!Number.isFinite(value) || value <= 0) {
        return { ok: false, error: `Invalid ${flagName}. Expected a positive integer.` };
    }
    return { ok: true, value };
}
export function parseNonNegativeIntFlag(raw, flagName, defaultValue) {
    const value = Number.parseInt(raw ?? String(defaultValue), 10);
    if (!Number.isFinite(value) || value < 0) {
        return { ok: false, error: `Invalid ${flagName}. Expected a non-negative integer.` };
    }
    return { ok: true, value };
}
export function parsePaginationFlags(cmdOpts, opts) {
    const maxPagesImpliesPagination = opts?.maxPagesImpliesPagination ?? false;
    const includeDelay = opts?.includeDelay ?? false;
    const defaultDelayMs = opts?.defaultDelayMs ?? 1000;
    const maxPages = parsePositiveIntFlag(cmdOpts.maxPages, '--max-pages');
    if (!maxPages.ok) {
        return maxPages;
    }
    const usePagination = Boolean(cmdOpts.all || cmdOpts.cursor || (maxPagesImpliesPagination && maxPages.value !== undefined));
    let pageDelayMs;
    if (includeDelay) {
        const delay = parseNonNegativeIntFlag(cmdOpts.delay, '--delay', defaultDelayMs);
        if (!delay.ok) {
            return delay;
        }
        pageDelayMs = delay.value;
    }
    return {
        ok: true,
        usePagination,
        maxPages: maxPages.value,
        cursor: cmdOpts.cursor,
        pageDelayMs,
    };
}
//# sourceMappingURL=pagination.js.map