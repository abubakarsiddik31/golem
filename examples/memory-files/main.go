// Command memory-files stores the memory notebook as Markdown files and
// shows compare-and-swap writes rejecting a stale edit. It runs offline
// in a temporary directory.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem/memory"
)

func main() {
	root, err := os.MkdirTemp("", "golem-memory-*")
	if err != nil {
		fmt.Println("temp dir:", err)
		return
	}
	defer os.RemoveAll(root)

	store, err := memory.NewFileStore(root)
	if err != nil {
		fmt.Println("memory.NewFileStore:", err)
		return
	}
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "ada", memory.MainFile, "The user prefers concise answers.", ""); err != nil {
		fmt.Println("create:", err)
		return
	}
	file, err := store.Read(ctx, "ada", memory.MainFile)
	if err != nil {
		fmt.Println("read:", err)
		return
	}
	fmt.Printf("stored %q (version %s...)\n", file.Content, file.Version[:8])

	if err := store.WriteVersioned(ctx, "ada", memory.MainFile, "A concurrent edit.", file.Version); err != nil {
		fmt.Println("update:", err)
		return
	}
	if err := store.WriteVersioned(ctx, "ada", memory.MainFile, "A stale edit.", file.Version); err != nil {
		fmt.Println("stale edit rejected:", err)
	}
	files, _ := store.List(ctx, "ada")
	fmt.Printf("notebook files: %v\n", files)
}
