package main

import (
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/logpolicy"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logpolicy: determine working directory: %v\n", err)
		os.Exit(1)
	}

	violations, err := logpolicy.CheckRepository(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logpolicy: %v\n", err)
		os.Exit(1)
	}
	if len(violations) == 0 {
		return
	}

	for _, violation := range violations {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: %s\n", violation.File, violation.Line, violation.Column, violation.Message)
	}
	os.Exit(1)
}
