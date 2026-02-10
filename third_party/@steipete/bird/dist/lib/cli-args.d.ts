export type CliInvocation = {
    argv: string[] | null;
    showHelp: boolean;
};
export declare function looksLikeTweetInput(value: string): boolean;
export declare function resolveCliInvocation(rawArgs: string[], knownCommands: Set<string>): CliInvocation;
//# sourceMappingURL=cli-args.d.ts.map