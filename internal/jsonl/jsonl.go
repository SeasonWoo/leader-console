package jsonl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// lockPoll is how often a contended lock is retried while waiting for ctx.
const lockPoll = 10 * time.Millisecond

// Append locks lockPath and appends one newline-terminated line with one write,
// then fsyncs it. Embedded newlines are rejected. Neither file may be a symlink.
// Under the lock, an unfinished final line (a crashed or short write) is truncated
// first, so the new line is never glued onto a fragment and lost.
func Append(path, lockPath string, line []byte) error {
	return appendLine(context.Background(), os.OpenFile, path, lockPath, line)
}

// AppendRoot is Append confined to root, including intermediate path components.
func AppendRoot(root *os.Root, path, lockPath string, line []byte) error {
	return appendLine(context.Background(), root.OpenFile, path, lockPath, line)
}

// AppendRootContext is AppendRoot whose lock wait ends when ctx is done.
func AppendRootContext(ctx context.Context, root *os.Root, path, lockPath string, line []byte) error {
	return appendLine(ctx, root.OpenFile, path, lockPath, line)
}

func lockContext(ctx context.Context, fd int) error {
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil || !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// dropPartialTail truncates f to its last newline. Writers append whole lines with a
// single write, so bytes after the last newline can only be a crashed write.
func dropPartialTail(f *os.File) error {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	end := info.Size()
	buf := make([]byte, 4096)
	for pos := end; pos > 0; {
		n := int64(len(buf))
		if pos < n {
			n = pos
		}
		pos -= n
		if _, err := f.ReadAt(buf[:n], pos); err != nil && err != io.EOF {
			return err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			if keep := pos + int64(i) + 1; keep != end {
				return f.Truncate(keep)
			}
			return nil
		}
	}
	return f.Truncate(0)
}

// openCreate retries O_CREATE opens that lose a concurrent create race: on darwin
// os.Root.OpenFile (Go 1.24) can then fail with ENOENT although the file exists.
func openCreate(ctx context.Context, open func(string, int, os.FileMode) (*os.File, error), path string, flag int) (*os.File, error) {
	for try := 0; ; try++ {
		f, err := open(path, flag, 0600)
		if err == nil || !errors.Is(err, os.ErrNotExist) || try == 20 {
			return f, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func appendLine(ctx context.Context, open func(string, int, os.FileMode) (*os.File, error), path, lockPath string, line []byte) error {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if bytes.ContainsAny(line, "\r\n") {
		return errors.New("jsonl: embedded newline")
	}
	if path == lockPath {
		return errors.New("jsonl: data and lock paths must differ")
	}
	lock, err := openCreate(ctx, open, lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := regular(lock); err != nil {
		return err
	}
	if err := lockContext(ctx, int(lock.Fd())); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) // Closing also releases the lock.
	f, err := openCreate(ctx, open, path, os.O_RDWR|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := regular(f); err != nil {
		return err
	}
	if err := dropPartialTail(f); err != nil {
		return err
	}
	buf := make([]byte, len(line)+1)
	copy(buf, line)
	buf[len(line)] = '\n'
	n, err := f.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrShortWrite
	}
	return f.Sync()
}

func regular(f *os.File) error {
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("jsonl: not a regular file")
	}
	return err
}

// ReadLines returns complete lines without their trailing newline. Blank and bad
// JSON lines are preserved, so index+1 is the physical line number. JSON decoding
// and warnings belong to the caller. An unfinished final line is ignored.
func ReadLines(path string) ([][]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := regular(f); err != nil {
		return nil, err
	}
	return ReadLinesFrom(f)
}

// ReadLinesFrom reads complete lines from an already securely opened source.
func ReadLinesFrom(r io.Reader) ([][]byte, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return nil, nil
	}
	return bytes.Split(b[:end], []byte{'\n'}), nil
}
