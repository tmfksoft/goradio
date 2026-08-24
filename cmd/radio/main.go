// Command radio is the single GoRadio entrypoint, dispatching to the
// `serve`, `station`, and `tokengen` subcommands.
package main

import (
	"fmt"
	"os"

	"github.com/goradioserver/goradio/internal/cli"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	sub, args := os.Args[1], os.Args[2:]

	var err error
	switch sub {
	case "serve":
		err = cli.Serve(args)
	case "station":
		err = cli.Station(args)
	case "tokengen":
		err = cli.TokenGen(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "radio: unknown command %q\n\n", sub)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "radio %s: %v\n", sub, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `GoRadio - a lightweight radio streaming server

Usage:
  radio serve    [--config server.yaml]
  radio station  [--config station.yaml] [--script station.lua] [args...]
  radio tokengen [-secret SECRET] [-subject SUBJECT] [-ttl 24h] <slug...>
`)
}
