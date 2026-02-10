import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { CurrentUserResult, FollowingResult } from './twitter-client-types.js';
export interface TwitterClientUserMethods {
    getCurrentUser(): Promise<CurrentUserResult>;
    getFollowing(userId: string, count?: number, cursor?: string): Promise<FollowingResult>;
    getFollowers(userId: string, count?: number, cursor?: string): Promise<FollowingResult>;
}
export declare function withUsers<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientUserMethods>;
//# sourceMappingURL=twitter-client-users.d.ts.map