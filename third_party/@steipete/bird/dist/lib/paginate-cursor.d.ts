export type CursorPage<T> = {
    success: true;
    items: T[];
    cursor?: string;
} | {
    success: false;
    error: string;
};
export type CursorPaginationResult<T> = {
    success: true;
    items: T[];
    nextCursor?: string;
} | {
    success: false;
    error: string;
    items?: T[];
    nextCursor?: string;
};
export declare function paginateCursor<T>(opts: {
    cursor?: string;
    maxPages?: number;
    pageDelayMs?: number;
    sleep: (ms: number) => Promise<void>;
    getKey: (item: T) => string;
    fetchPage: (cursor?: string) => Promise<CursorPage<T>>;
}): Promise<CursorPaginationResult<T>>;
//# sourceMappingURL=paginate-cursor.d.ts.map