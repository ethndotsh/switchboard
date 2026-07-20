package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

const version = "0.2.0"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "init":
		return initProject(args[1:])
	case "build", "dist":
		return build(ctx, args[1:])
	case "deploy":
		return deploy(ctx, args[1:])
	case "inspect":
		return inspect(ctx, args[1:])
	case "eval":
		return evalCommand(ctx, args[1:])
	case "test":
		return testCommand(ctx, args[1:])
	case "replay":
		return replayCommand(ctx, args[1:])
	case "status":
		return statusCommand(ctx, args[1:])
	case "history":
		return historyCommand(ctx, args[1:])
	case "diff":
		return diffCommand(ctx, args[1:])
	case "promote":
		return promoteCommand(ctx, args[1:])
	case "rollback":
		return rollbackCommand(ctx, args[1:])
	case "serve":
		return serveCommand(ctx, args[1:])
	case "version":
		fmt.Printf("switchboard %s\n", version)
		return nil
	default:
		return usage()
	}
}

func usage() error {
	return errors.New(`usage: switchboard <command>

project:
  init      scaffold a rule project
  build     compile the rule package into a bundle (alias: dist)
  test      run a bundle against its behavioral test cases
  eval      run one request against a bundle and print the decision
  replay    replay Caddy access logs against two bundles and diff decisions

deployment:
  deploy    upload the bundle and repoint the channel
  status    show the channel pointer and latest revision
  history   list deployment revisions
  diff      compare two bundles or channels
  promote   copy one channel's bundle to another
  rollback  repoint a channel at an earlier revision
  inspect   print the raw channel pointer

serving:
  serve     standalone reverse proxy with live rule reloads

  version   print the CLI version`)
}

func normalizeFlagArgs(args []string, valueFlags ...string) []string {
	valueFlagSet := map[string]bool{}
	for _, flag := range valueFlags {
		valueFlagSet[flag] = true
	}
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if valueFlagSet[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "switchboard-rules"
	}
	return wd
}
