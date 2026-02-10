export type PaginationCmdOpts = {
    all?: boolean;
    maxPages?: string;
    cursor?: string;
    delay?: string;
};
export declare function parsePositiveIntFlag(raw: string | undefined, flagName: string): {
    ok: true;
    value: number | undefined;
} | {
    ok: false;
    error: string;
};
export declare function parseNonNegativeIntFlag(raw: string | undefined, flagName: string, defaultValue: number): {
    ok: true;
    value: number;
} | {
    ok: false;
    error: string;
};
export declare function parsePaginationFlags(cmdOpts: PaginationCmdOpts, opts?: {
    maxPagesImpliesPagination?: boolean;
    defaultDelayMs?: number;
    includeDelay?: boolean;
}): {
    ok: true;
    usePagination: boolean;
    maxPages?: number;
    cursor?: string;
    pageDelayMs?: number;
} | {
    ok: false;
    error: string;
};
//# sourceMappingURL=pagination.d.ts.map