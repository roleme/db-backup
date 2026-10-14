package rsyncgz

import (
	"compress/gzip"
	"errors"
	"io"
)

const (
	minSegment = 16 << 10
	maxSegment = 256 << 10
	cutMask    = uint64(0xffff) << 48
)

var errClosed = errors.New("rsyncgz: write to a closed writer")

var gear = newGear()

func newGear() [256]uint64 {
	var t [256]uint64
	s := uint64(0x9e3779b97f4a7c15)
	for i := range t {
		s += 0x9e3779b97f4a7c15
		z := s
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		t[i] = z ^ (z >> 31)
	}
	return t
}

type Writer struct {
	dst     io.Writer
	gz      *gzip.Writer
	size    int
	hash    uint64
	open    bool
	members int
	closed  bool
	err     error
}

func NewWriter(dst io.Writer, level int) (*Writer, error) {
	gz, err := gzip.NewWriterLevel(dst, level)
	if err != nil {
		return nil, err
	}
	return &Writer{dst: dst, gz: gz}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(p) > 0 {
		n, cut := w.scan(p)
		if _, err := w.gz.Write(p[:n]); err != nil {
			w.err = err
			return written, err
		}
		w.open = true
		written += n
		p = p[n:]
		if cut {
			if err := w.rotate(); err != nil {
				w.err = err
				return written, err
			}
		}
	}
	return written, nil
}

func (w *Writer) scan(p []byte) (int, bool) {
	h, size := w.hash, w.size
	for i, b := range p {
		h = h<<1 + gear[b]
		size++
		if size >= maxSegment || (size >= minSegment && h&cutMask == 0) {
			w.hash, w.size = h, 0
			return i + 1, true
		}
	}
	w.hash, w.size = h, size
	return len(p), false
}

func (w *Writer) rotate() error {
	if err := w.gz.Close(); err != nil {
		return err
	}
	w.gz.Reset(w.dst)
	w.open = false
	w.members++
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	if w.open || w.members == 0 {
		w.err = w.gz.Close()
	}
	return w.err
}
