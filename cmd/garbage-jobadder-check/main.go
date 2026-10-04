// A bounded read-only connection check; it never persists tokens or profiles.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/connectors/jobadder"
)

func main() {
	base := os.Getenv("GARBAGE_JOBADDER_API_BASE")
	if base == "" {
		base = jobadder.DefaultBase
	}
	token := os.Getenv("GARBAGE_JOBADDER_ACCESS_TOKEN")
	client, err := jobadder.New(base, func(context.Context) (string, error) { return token, nil })
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var page jobadder.Page
		page, err = client.Candidates(ctx, "")
		if err == nil {
			fmt.Printf("Read-only JobAdder check passed: %d records in the first page; %d reported total. No profiles were saved or changed.\n", len(page.Items), page.TotalCount)
			return
		}
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
