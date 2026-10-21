package jsonl

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
)

// Append locks lockPath and appends one newline-terminated line with one write,
// then fsyncs it. Embedded newlines are rejected. Neither file may be a symlink.
func Append(path, lockPath string, line []byte) error {
	return appendLine(os.OpenFile, path, lockPath, line)
}

// AppendRoot is Append confined to root, including intermediate path components.
func AppendRoot(root *os.Root, path, lockPath string, line []byte) error {
	return appendLine(root.OpenFile, path, lockPath, line)
}

func appendLine(open func(string, int, os.FileMode) (*os.File, error), path, lockPath string, line []byte) error {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if bytes.ContainsAny(line, "\r\n") {
		return errors.New("jsonl: embedded newline")
	}
	if path == lockPath {
		return errors.New("jsonl: data and lock paths must differ")
	}
	lock, err := open(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := regular(lock); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) // Closing also releases the lock.
	f, err := open(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := regular(f); err != nil {
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
