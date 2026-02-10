const TWEET_URL_REGEX = /^(?:https?:\/\/)?(?:www\.)?(?:twitter\.com|x\.com)\/[^/]+\/status\/\d+/i;
const TWEET_ID_REGEX = /^\d{8,}$/;
export function looksLikeTweetInput(value) {
    const trimmed = value.trim();
    if (!trimmed) {
        return false;
    }
    return TWEET_URL_REGEX.test(trimmed) || TWEET_ID_REGEX.test(trimmed);
}
export function resolveCliInvocation(rawArgs, knownCommands) {
    if (rawArgs.length === 0) {
        return { argv: null, showHelp: true };
    }
    const hasKnownCommand = rawArgs.some((arg) => knownCommands.has(arg));
    if (!hasKnownCommand) {
        const tweetArgIndex = rawArgs.findIndex(looksLikeTweetInput);
        if (tweetArgIndex >= 0) {
            const rewrittenArgs = [...rawArgs];
            rewrittenArgs.splice(tweetArgIndex, 0, 'read');
            return { argv: ['node', 'bird', ...rewrittenArgs], showHelp: false };
        }
    }
    return { argv: null, showHelp: false };
}
//# sourceMappingURL=cli-args.js.map