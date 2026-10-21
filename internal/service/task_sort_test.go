package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"leader-console/internal/model"
)

func TestTasksSortedByLatestChange(t *testing.T) {
	columns := []struct {
		status model.TaskStatus
		column model.BoardColumn
	}{
		{model.TaskWorking, model.ColumnInProgress},
		{model.TaskBlocked, model.ColumnBlocked},
		{model.TaskReviewing, model.ColumnReviewing},
		{model.TaskReadyToMerge, model.ColumnToMerge},
		{model.TaskDone, model.ColumnDone},
	}
	for _, source := range []model.DataSource{model.SourceState, model.SourceLedger} {
		for _, col := range columns {
			t.Run(string(source)+"/"+string(col.column), func(t *testing.T) {
				s, f, _, clock := setup(t)
				now := clock.now()
				tie := now.Add(-time.Hour)
				f.edit(func(pd *model.ProjectData) {
					pd.Source = source
					pd.Tasks = []model.Task{
						{ID: "b-tie", UpdatedAt: tie},
						// A newer creation time must not override an explicit update time.
						{ID: "old", UpdatedAt: now.Add(-3 * time.Hour), CreatedAt: now.Add(time.Hour)},
						{ID: "missing"},
						{ID: "new", UpdatedAt: now.Add(-time.Nanosecond)},
						{ID: "a-tie", UpdatedAt: tie.In(time.FixedZone("UTC+8", 8*3600))},
						{ID: "fallback", CreatedAt: now.Add(-30 * time.Minute)},
					}
					for i := range pd.Tasks {
						pd.Tasks[i].Status = col.status
					}
				})
				f.projects["alpha"] = &model.ProjectData{
					Name: "alpha", Source: source,
					Tasks: []model.Task{
						{ID: "a-tie", Status: col.status, UpdatedAt: tie},
						{ID: "a-missing", Status: col.status},
						{ID: "newest-other", Status: col.status, UpdatedAt: now},
					},
				}
				all := []string{
					"alpha/newest-other", "demo/new", "demo/fallback",
					"alpha/a-tie", "demo/a-tie", "demo/b-tie", "demo/old",
					"alpha/a-missing", "demo/missing",
				}
				demo := []string{"demo/new", "demo/fallback", "demo/a-tie", "demo/b-tie", "demo/old", "demo/missing"}
				for pass := 0; pass < 2; pass++ {
					if err := s.scan(context.Background()); err != nil {
						t.Fatal(err)
					}
					for _, filter := range []struct {
						project, column string
						want            []string
					}{
						{"", "", all},
						{"", string(col.column), all},
						{"demo", "", demo},
						{"demo", string(col.column), demo},
					} {
						result, err := s.Tasks(filter.project, filter.column)
						if err != nil {
							t.Fatal(err)
						}
						got := make([]string, 0, len(result.Tasks))
						for _, tv := range result.Tasks {
							if tv.Column != col.column {
								t.Fatalf("task %s column = %s, want %s", tv.Task.ID, tv.Column, col.column)
							}
							got = append(got, tv.Project+"/"+tv.Task.ID)
						}
						if !slices.Equal(got, filter.want) {
							t.Fatalf("pass %d, filter (%q, %q): got %v, want %v", pass, filter.project, filter.column, got, filter.want)
						}
					}
					// Source order changes must not affect time/ID/project tie breaking.
					for _, pd := range f.projects {
						slices.Reverse(pd.Tasks)
					}
				}
			})
		}
	}
}

func TestNewlyCompletedTaskMovesToTop(t *testing.T) {
	s, f, _, clock := setup(t)
	now := clock.now()
	f.edit(func(pd *model.ProjectData) {
		pd.Tasks = []model.Task{
			{ID: "a-old-done", Status: model.TaskDone, UpdatedAt: now.Add(-time.Hour)},
			{ID: "z-finishing", Status: model.TaskWorking, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
		}
	})
	if err := s.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := s.Tasks("", string(model.ColumnDone))
	if err != nil || len(before.Tasks) != 1 || before.Tasks[0].Task.ID != "a-old-done" {
		t.Fatalf("before completion: %+v, %v", before, err)
	}
	f.edit(func(pd *model.ProjectData) {
		pd.Tasks[1].Status = model.TaskDone
		pd.Tasks[1].UpdatedAt = now
	})
	if err := s.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.Tasks("", string(model.ColumnDone))
	if err != nil || len(after.Tasks) != 2 || after.Tasks[0].Task.ID != "z-finishing" || after.Tasks[1].Task.ID != "a-old-done" {
		t.Fatalf("after completion: %+v, %v", after, err)
	}
	if before.Version == after.Version {
		t.Fatal("completion must publish a new snapshot version for SSE refresh")
	}
}
