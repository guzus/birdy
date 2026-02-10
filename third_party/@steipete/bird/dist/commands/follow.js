import { normalizeHandle } from '../lib/normalize-handle.js';
import { TwitterClient } from '../lib/twitter-client.js';
const ONLY_DIGITS_REGEX = /^\d+$/;
async function resolveUserId(client, usernameOrId, ctx) {
    const raw = usernameOrId.trim();
    const isNumeric = ONLY_DIGITS_REGEX.test(raw);
    // Otherwise, treat as username and look up
    const handle = normalizeHandle(raw);
    if (handle) {
        const lookup = await client.getUserIdByUsername(handle);
        if (lookup.success && lookup.userId) {
            return { userId: lookup.userId, username: lookup.username };
        }
        if (!isNumeric) {
            console.error(`${ctx.p('err')}Failed to find user @${handle}: ${lookup.error ?? 'Unknown error'}`);
            return null;
        }
    }
    if (isNumeric) {
        return { userId: raw };
    }
    console.error(`${ctx.p('err')}Invalid username: ${usernameOrId}`);
    return null;
}
export function registerFollowCommands(program, ctx) {
    program
        .command('follow')
        .description('Follow a user')
        .argument('<username-or-id>', 'Username (with or without @) or user ID to follow')
        .action(async (usernameOrId) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        for (const warning of warnings) {
            console.error(`${ctx.p('warn')}${warning}`);
        }
        if (!cookies.authToken || !cookies.ct0) {
            console.error(`${ctx.p('err')}Missing required credentials`);
            process.exit(1);
        }
        const client = new TwitterClient({ cookies, timeoutMs });
        const resolved = await resolveUserId(client, usernameOrId, ctx);
        if (!resolved) {
            process.exit(1);
        }
        const { userId, username } = resolved;
        const displayName = username ? `@${username}` : userId;
        const result = await client.follow(userId);
        if (result.success) {
            const finalName = result.username ? `@${result.username}` : displayName;
            console.log(`${ctx.p('ok')}Now following ${finalName}`);
        }
        else {
            console.error(`${ctx.p('err')}Failed to follow ${displayName}: ${result.error}`);
            process.exit(1);
        }
    });
    program
        .command('unfollow')
        .description('Unfollow a user')
        .argument('<username-or-id>', 'Username (with or without @) or user ID to unfollow')
        .action(async (usernameOrId) => {
        const opts = program.opts();
        const timeoutMs = ctx.resolveTimeoutFromOptions(opts);
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        for (const warning of warnings) {
            console.error(`${ctx.p('warn')}${warning}`);
        }
        if (!cookies.authToken || !cookies.ct0) {
            console.error(`${ctx.p('err')}Missing required credentials`);
            process.exit(1);
        }
        const client = new TwitterClient({ cookies, timeoutMs });
        const resolved = await resolveUserId(client, usernameOrId, ctx);
        if (!resolved) {
            process.exit(1);
        }
        const { userId, username } = resolved;
        const displayName = username ? `@${username}` : userId;
        const result = await client.unfollow(userId);
        if (result.success) {
            const finalName = result.username ? `@${result.username}` : displayName;
            console.log(`${ctx.p('ok')}Unfollowed ${finalName}`);
        }
        else {
            console.error(`${ctx.p('err')}Failed to unfollow ${displayName}: ${result.error}`);
            process.exit(1);
        }
    });
}
//# sourceMappingURL=follow.js.map