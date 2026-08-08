package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"email-cleanup/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "Interrupted.")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
