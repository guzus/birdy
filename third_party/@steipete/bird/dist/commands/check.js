export function registerCheckCommand(program, ctx) {
    program
        .command('check')
        .description('Check credential availability')
        .action(async () => {
        const opts = program.opts();
        const { cookies, warnings } = await ctx.resolveCredentialsFromOptions(opts);
        console.log(`${ctx.p('info')}Credential check`);
        console.log('─'.repeat(40));
        if (cookies.authToken) {
            console.log(`${ctx.p('ok')}auth_token: ${cookies.authToken.slice(0, 10)}...`);
        }
        else {
            console.log(`${ctx.p('err')}auth_token: not found`);
        }
        if (cookies.ct0) {
            console.log(`${ctx.p('ok')}ct0: ${cookies.ct0.slice(0, 10)}...`);
        }
        else {
            console.log(`${ctx.p('err')}ct0: not found`);
        }
        if (cookies.source) {
            console.log(`${ctx.l('source')}${cookies.source}`);
        }
        if (warnings.length > 0) {
            console.log(`\n${ctx.p('warn')}Warnings:`);
            for (const warning of warnings) {
                console.log(`   - ${warning}`);
            }
        }
        if (cookies.authToken && cookies.ct0) {
            console.log(`\n${ctx.p('ok')}Ready to tweet!`);
        }
        else {
            console.log(`\n${ctx.p('err')}Missing credentials. Options:`);
            console.log('   1. Login to x.com in Safari/Chrome/Firefox');
            console.log('   2. Set AUTH_TOKEN and CT0 environment variables');
            console.log('   3. Use --auth-token and --ct0 flags');
            process.exit(1);
        }
    });
}
//# sourceMappingURL=check.js.map