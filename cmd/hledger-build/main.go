// Package main is the entry point for the hledger-build CLI.
package main

import "os"

func main() {
	if err := buildRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
