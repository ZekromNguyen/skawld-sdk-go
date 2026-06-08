package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func BenchmarkStoreTaskSubjectUpdate(b *testing.B) {
	for _, size := range []int{100, 1000, 2500} {
		b.Run(strconv.Itoa(size)+"_tasks", func(b *testing.B) {
			ctx := context.Background()
			store := openBenchmarkStore(b)
			defer closeBenchmarkStore(b, store)
			tasks := seedBenchmarkTasks(b, ctx, store, size)
			targetID := tasks[size/2].ID

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				subject := "updated " + strconv.Itoa(i)
				if _, ok, err := store.UpdateTask(ctx, "s", targetID, core.TaskPatch{Subject: &subject}); err != nil || !ok {
					b.Fatalf("update failed: ok=%t err=%v", ok, err)
				}
			}
		})
	}
}

func BenchmarkStoreTaskEdgeUpdate(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run(strconv.Itoa(size)+"_tasks", func(b *testing.B) {
			ctx := context.Background()
			store := openBenchmarkStore(b)
			defer closeBenchmarkStore(b, store)
			tasks := seedBenchmarkTasks(b, ctx, store, size)
			blockerID := tasks[0].ID
			blockedID := tasks[len(tasks)-1].ID

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok, err := store.UpdateTask(ctx, "s", blockerID, core.TaskPatch{AddBlocks: []string{blockedID}}); err != nil || !ok {
					b.Fatalf("add edge failed: ok=%t err=%v", ok, err)
				}
				if _, ok, err := store.UpdateTask(ctx, "s", blockerID, core.TaskPatch{RemoveBlocks: []string{blockedID}}); err != nil || !ok {
					b.Fatalf("remove edge failed: ok=%t err=%v", ok, err)
				}
			}
		})
	}
}

func seedBenchmarkTasks(b *testing.B, ctx context.Context, store *Store, count int) []core.Task {
	b.Helper()
	if _, err := store.Create(ctx, "s", nil); err != nil {
		b.Fatal(err)
	}
	tasks := make([]core.Task, 0, count)
	for i := 0; i < count; i++ {
		task, err := store.CreateTask(ctx, "s", core.CreateTaskInput{
			Subject:     "task " + strconv.Itoa(i),
			Description: "",
		})
		if err != nil {
			b.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func openBenchmarkStore(b *testing.B) *Store {
	b.Helper()
	store, err := Open(filepath.Join(b.TempDir(), "sessions.db"))
	if err != nil {
		b.Fatal(err)
	}
	return store
}

func closeBenchmarkStore(b *testing.B, store *Store) {
	b.Helper()
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}
}
