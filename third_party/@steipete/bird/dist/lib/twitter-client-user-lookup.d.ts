import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { AboutAccountResult } from './twitter-client-types.js';
/** Result of username to userId lookup */
export interface UserLookupResult {
    success: boolean;
    userId?: string;
    username?: string;
    name?: string;
    error?: string;
}
export interface TwitterClientUserLookupMethods {
    getUserIdByUsername(username: string): Promise<UserLookupResult>;
    getUserAboutAccount(username: string): Promise<AboutAccountResult>;
}
export declare function withUserLookup<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientUserLookupMethods>;
//# sourceMappingURL=twitter-client-user-lookup.d.ts.map