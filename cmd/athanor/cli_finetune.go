package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/finetune"
	"github.com/tcs76321/athanor/internal/store"
)

// runFinetune exports accepted work as a JSONL instruction-tuning dataset.
// It reads the local state directory; it does not call a trainer or mutate
// models. A fine-tune is adopted later as a new pool entry.
func runFinetune(args []string) error {
	fs := flag.NewFlagSet("finetune", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "state", "state directory (database and artifacts)")
	out := fs.String("out", "finetune.jsonl", "output JSONL path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(*stateDir, "athanor.db"))
	if err != nil {
		return fmt.Errorf("opening state: %w", err)
	}
	defer func() { _ = st.Close() }()
	arts := artifact.NewStore(st, filepath.Join(*stateDir, "artifacts"))
	f, err := os.Create(*out)
	if err != nil {
		return fmt.Errorf("creating %s: %w", *out, err)
	}
	defer func() { _ = f.Close() }()
	n, err := finetune.Export(context.Background(), st, arts, f)
	if err != nil {
		return err
	}
	fmt.Printf("exported %d accepted example(s) to %s\n", n, *out)
	fmt.Println("next: train externally, register the result as a model, then run qualification before it is castable")
	return nil
}
