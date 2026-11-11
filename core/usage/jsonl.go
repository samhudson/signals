package usage

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
)

// maxLine is the longest line read; longer ones (huge tool results) are
// skipped rather than stopping the whole file.
var maxLine = 64 << 20

// readLines calls fn for each complete line of a file, starting at byte
// offset from, and returns the offset just past the last complete line. A last
// line with no newline yet (the agent is still writing it) is left for next
// time. Windows line endings are trimmed.
func readLines(path string, from int64, fn func(line []byte)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return from, err
	}
	defer f.Close()
	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return from, err
		}
	}
	r := bufio.NewReaderSize(f, 1<<20)
	off := from
	var buf []byte
	var pending int64
	skipping := false
	for {
		chunk, err := r.ReadSlice('\n')
		pending += int64(len(chunk))
		if !skipping {
			if len(buf)+len(chunk) > maxLine {
				skipping, buf = true, buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			return off, nil // an unfinished line: read it next time
		}
		if err != nil {
			return off, err
		}
		off += pending
		if !skipping {
			if line := bytes.TrimRight(buf, "\r\n"); len(line) > 0 {
				fn(line)
			}
		}
		buf, pending, skipping = buf[:0], 0, false
	}
}

// headHash identifies a file by its first 4 KB, so a file that was replaced or
// rewritten is read again from the start.
func headHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, 4096)
	n, _ := io.ReadFull(f, b)
	return hash16(string(b[:n]))
}
