export function registerHelpCommand(program, ctx) {
    program
        .command('help [command]')
        .description('Show help for a command')
        .action((commandName) => {
        if (!commandName) {
            program.outputHelp();
            return;
        }
        const cmd = program.commands.find((c) => c.name() === commandName);
        if (!cmd) {
            console.error(`${ctx.p('err')}Unknown command: ${commandName}`);
            process.exitCode = 2;
            return;
        }
        cmd.outputHelp();
    });
}
//# sourceMappingURL=help.js.map