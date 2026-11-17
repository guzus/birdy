package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"

	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
)

// Bird accepts a status URL or a long numeric ID as shorthand for read.
var shorthandRead = regexp.MustCompile(`(?i)^(?:[0-9]{8,25}$|(?:https?://)?(?:www\.)?(?:twitter\.com|x\.com)/[^/]+/status/[0-9]+)`)

// nativeInvocation returns the native argv, or nil to forward the original argv
// intact. HTTP and manifest callers can put bird's global display switches first.
func nativeInvocation(args []string) []string {
	if useBird() {
		return nil
	}
	for i, arg := range args {
		switch arg {
		case "--plain", "--no-emoji", "--no-color":
			continue
		}
		name := nativeCommandName(arg)
		tail := args[i+1:]
		if !nativeSupports(name) {
			if !shorthandRead.MatchString(arg) {
				return nil
			}
			name, tail = "read", args[i:]
		}
		if !helpRequested(tail) && !nativeAcceptsFlags(name, tail) {
			return nil
		}
		if i == 0 && name == arg {
			return args
		}
		native := make([]string, 0, 1+i+len(tail))
		native = append(native, name)
		native = append(native, args[:i]...)
		return append(native, tail...)
	}
	return nil
}

func commandEngine(args []string) string {
	if nativeInvocation(args) != nil {
		return "native"
	}
	return "bird"
}

type commandCapture struct {
	dial        func(context.Context, string, string) (net.Conn, error)
	birdOptions runner.Options
}

// A workflow with several calls can reuse one VPN exit and bridge for its
// lifetime. Native calls use the SOCKS dialer directly.
func newCommandCapture(errOut io.Writer, legacy bool) (commandCapture, func(), error) {
	if !vpnFlag && vpnServerFlag == "" {
		return commandCapture{}, func() {}, nil
	}
	if legacy {
		bridge, env, err := startVPN(errOut)
		if err != nil {
			return commandCapture{}, nil, err
		}
		return commandCapture{birdOptions: runner.Options{ExtraEnv: env}}, bridge.Stop, nil
	}
	dial, err := vpnDialer(errOut)
	return commandCapture{dial: dial}, func() {}, err
}

func runCommandCapture(ctx context.Context, account *store.Account, args []string) (runner.Result, string, string, error) {
	var diagnostics bytes.Buffer
	route, closeRoute, err := newCommandCapture(&diagnostics, commandEngine(args) == "bird")
	if err != nil {
		return runner.Result{ExitCode: 1}, "", diagnostics.String(), err
	}
	defer closeRoute()
	res, stdout, stderr, err := route.run(ctx, account, args)
	return res, stdout, diagnostics.String() + stderr, err
}

// A command error is an exit code and stderr. A launch or cancellation error
// is also returned as a Go error so callers can distinguish an incomplete run.
func (route commandCapture) run(ctx context.Context, account *store.Account, args []string) (runner.Result, string, string, error) {
	if account == nil {
		return runner.Result{ExitCode: 1}, "", "", fmt.Errorf("missing account")
	}
	if err := ctx.Err(); err != nil {
		return runner.Result{ExitCode: 1}, "", "", err
	}
	native := nativeInvocation(args)
	if native == nil {
		if route.dial != nil {
			return runner.Result{ExitCode: 1}, "", "", fmt.Errorf("legacy command requires a VPN bridge")
		}
		return runner.RunCaptureContext(ctx, account, args, route.birdOptions)
	}
	var out, errOut bytes.Buffer
	err := route.runNative(ctx, account, native, &out, &errOut)
	if err == nil {
		return runner.Result{}, out.String(), errOut.String(), nil
	}
	if ctx.Err() != nil {
		return runner.Result{ExitCode: 1}, out.String(), errOut.String(), fmt.Errorf("running native %s: %w", native[0], ctx.Err())
	}
	msg := err.Error()
	errOut.WriteString(msg)
	return runner.Result{ExitCode: commandExitCode(err), RateLimited: xapi.IsRateLimited(err), NotFound: runner.IsNotFoundMessage([]byte(msg))}, out.String(), errOut.String(), nil
}

func commandExitCode(err error) int {
	var status interface{ ExitCode() int }
	if errors.As(err, &status) {
		return status.ExitCode()
	}
	return 1
}
