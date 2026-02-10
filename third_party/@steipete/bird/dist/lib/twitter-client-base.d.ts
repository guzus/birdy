import { type OperationName } from './twitter-client-constants.js';
import type { CurrentUserResult, TwitterClientOptions } from './twitter-client-types.js';
export type Constructor<T = object> = new (...args: any[]) => T;
export type AbstractConstructor<T = object> = abstract new (...args: any[]) => T;
export type Mixin<TBase extends AbstractConstructor<TwitterClientBase>, TAdded> = abstract new (...args: ConstructorParameters<TBase>) => TwitterClientBase & TAdded;
export declare abstract class TwitterClientBase {
    protected authToken: string;
    protected ct0: string;
    protected cookieHeader: string;
    protected userAgent: string;
    protected timeoutMs?: number;
    protected quoteDepth: number;
    protected clientUuid: string;
    protected clientDeviceId: string;
    protected clientUserId?: string;
    constructor(options: TwitterClientOptions);
    protected abstract getCurrentUser(): Promise<CurrentUserResult>;
    protected sleep(ms: number): Promise<void>;
    protected getQueryId(operationName: OperationName): Promise<string>;
    protected refreshQueryIds(): Promise<void>;
    protected withRefreshedQueryIdsOn404<T extends {
        success: boolean;
        had404?: boolean;
    }>(attempt: () => Promise<T>): Promise<{
        result: T;
        refreshed: boolean;
    }>;
    protected getTweetDetailQueryIds(): Promise<string[]>;
    protected getSearchTimelineQueryIds(): Promise<string[]>;
    protected fetchWithTimeout(url: string, init: RequestInit): Promise<Response>;
    protected getHeaders(): Record<string, string>;
    protected createTransactionId(): string;
    protected getBaseHeaders(): Record<string, string>;
    protected getJsonHeaders(): Record<string, string>;
    protected getUploadHeaders(): Record<string, string>;
    protected ensureClientUserId(): Promise<void>;
}
//# sourceMappingURL=twitter-client-base.d.ts.map