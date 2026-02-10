export type RuntimeQueryIdSnapshot = {
    fetchedAt: string;
    ttlMs: number;
    ids: Record<string, string>;
    discovery: {
        pages: string[];
        bundles: string[];
    };
};
export type RuntimeQueryIdSnapshotInfo = {
    snapshot: RuntimeQueryIdSnapshot;
    cachePath: string;
    ageMs: number;
    isFresh: boolean;
};
export type RuntimeQueryIdsOptions = {
    cachePath?: string;
    ttlMs?: number;
    fetchImpl?: typeof fetch;
};
export type RuntimeQueryIdStore = {
    cachePath: string;
    ttlMs: number;
    getSnapshotInfo: () => Promise<RuntimeQueryIdSnapshotInfo | null>;
    getQueryId: (operationName: string) => Promise<string | null>;
    refresh: (operationNames: string[], opts?: {
        force?: boolean;
    }) => Promise<RuntimeQueryIdSnapshotInfo | null>;
    clearMemory: () => void;
};
export declare function createRuntimeQueryIdStore(options?: RuntimeQueryIdsOptions): RuntimeQueryIdStore;
export declare const runtimeQueryIds: RuntimeQueryIdStore;
//# sourceMappingURL=runtime-query-ids.d.ts.map