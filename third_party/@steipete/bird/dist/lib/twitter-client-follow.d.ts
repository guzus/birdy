import type { AbstractConstructor, Mixin, TwitterClientBase } from './twitter-client-base.js';
import type { FollowMutationResult } from './twitter-client-types.js';
export interface TwitterClientFollowMethods {
    follow(userId: string): Promise<FollowMutationResult>;
    unfollow(userId: string): Promise<FollowMutationResult>;
}
export declare function withFollow<TBase extends AbstractConstructor<TwitterClientBase>>(Base: TBase): Mixin<TBase, TwitterClientFollowMethods>;
//# sourceMappingURL=twitter-client-follow.d.ts.map