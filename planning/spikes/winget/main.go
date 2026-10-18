package main

import (
	"flag"
	"fmt"
	"os"
)

// Spike entry point. It only prints the winget argv for a sample job. It never
// calls LaunchInUserSession, which needs SeTcb (a service); the elevated-VM
// test plan in README.md drives that from a test service.
func main() {
	id := flag.String("id", "Git.Git", "winget package id")
	scope := flag.String("scope", "", "user|machine|empty")
	flag.Parse()
	args, err := InstallArgs(Item{Source: "winget", ID: *id, Scope: Scope(*scope)})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(append([]string{"winget"}, args...))
}
