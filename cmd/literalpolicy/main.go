package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/autobrr/upbrr/internal/literalpolicy"
)

func main() {
	fix := flag.Bool("fix", false, "rewrite inconsistent keyed composite literals")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "literalpolicy: determine working directory: %v\n", err)
		os.Exit(1)
	}
	if *fix {
		fixed, fixErr := literalpolicy.FixRepository(root)
		if fixErr != nil {
			fmt.Fprintf(os.Stderr, "literalpolicy: %v\n", fixErr)
			os.Exit(1)
		}
		fmt.Printf("literalpolicy: fixed %d files\n", fixed)
		return
	}
	violations, err := literalpolicy.CheckRepository(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "literalpolicy: %v\n", err)
		os.Exit(1)
	}
	for _, violation := range violations {
		fmt.Println(violation.String())
	}
	if len(violations) != 0 {
		os.Exit(1)
	}
	fmt.Println("literalpolicy: no issues found")
}
