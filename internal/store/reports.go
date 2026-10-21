package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"leader-console/internal/model"
)

type reportFile struct {
	ref    model.ReportRef
	path   string
	legacy bool
	mtime  time.Time
}

func reportID(path string) string {
	sum := sha1.Sum([]byte(path))
	return "r-" + hex.EncodeToString(sum[:5])
}

func (f *FS) reports(ctx context.Context, project, taskID string) ([]reportFile, error) {
	if !idRE.MatchString(project) {
		return nil, ErrProjectNotFound
	}
	if !idRE.MatchString(taskID) {
		return nil, ErrReportNotFound
	}
	pd, err := f.LoadProject(ctx, project)
	if err != nil {
		return nil, err
	}
	var task *model.Task
	for i := range pd.Tasks {
		if pd.Tasks[i].ID == taskID {
			task = &pd.Tasks[i]
			break
		}
	}
	if task == nil {
		return nil, ErrReportNotFound
	}
	root, err := f.openProject(ctx, project)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := []reportFile{}
	dir := filepath.Join("reports", taskID)
	entries, err := readDir(root, dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := safeOpen(root, path)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		file.Close()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, reportFile{ref: model.ReportRef{ID: reportID(filepath.Join(pd.Dir, path)), Title: e.Name(), Size: info.Size()}, path: path, mtime: info.ModTime()})
	}
	// Only legacy ledger references authorize /tmp files; state.json cannot
	// turn arbitrary filesystem paths into readable reports.
	if pd.Source == model.SourceLedger {
		seen := make(map[string]bool)
		for _, path := range task.Reports {
			if !tmpReportRE.MatchString(path) || seen[path] {
				continue
			}
			seen[path] = true
			tmp, err := os.OpenRoot("/tmp")
			if err != nil {
				continue
			}
			file, err := safeOpen(tmp, filepath.Base(path))
			tmp.Close()
			if err != nil {
				continue
			}
			info, err := file.Stat()
			file.Close()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			files = append(files, reportFile{ref: model.ReportRef{ID: reportID(path), Title: filepath.Base(path), Size: info.Size()}, path: path, legacy: true, mtime: info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mtime.Equal(files[j].mtime) {
			return files[i].ref.ID < files[j].ref.ID
		}
		return files[i].mtime.After(files[j].mtime)
	})
	return files, ctx.Err()
}

func (f *FS) ListReports(ctx context.Context, project, taskID string) ([]model.ReportRef, error) {
	files, err := f.reports(ctx, project, taskID)
	if err != nil {
		return nil, err
	}
	refs := make([]model.ReportRef, 0, len(files))
	for _, file := range files {
		refs = append(refs, file.ref)
	}
	return refs, nil
}

func (f *FS) ReadReport(ctx context.Context, project, taskID, id string) ([]byte, error) {
	if !idRE.MatchString(project) {
		return nil, ErrProjectNotFound
	}
	if !idRE.MatchString(id) {
		return nil, ErrReportNotFound
	}
	files, err := f.reports(ctx, project, taskID)
	if err != nil {
		return nil, err
	}
	for _, report := range files {
		if report.ref.ID != id {
			continue
		}
		var root *os.Root
		path := report.path
		if report.legacy {
			root, err = os.OpenRoot("/tmp")
			path = filepath.Base(path)
		} else {
			root, err = f.openProject(ctx, project)
		}
		if err != nil {
			return nil, ErrReportNotFound
		}
		file, err := safeOpen(root, path)
		root.Close()
		if err != nil {
			return nil, ErrReportNotFound
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrReportNotFound
		}
		b, err := io.ReadAll(io.LimitReader(file, f.opts.MaxReportBytes))
		if err == nil {
			err = ctx.Err()
		}
		return b, err
	}
	return nil, ErrReportNotFound
}
