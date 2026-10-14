package rsyncgz

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
)

func sampleText(seed uint64, size int) []byte {
	r := rand.New(rand.NewPCG(seed, seed+1))
	var b bytes.Buffer
	for b.Len() < size {
		fmt.Fprintf(&b, "%d\t%d\t[%0.6f,%0.6f,%0.6f]\n", r.IntN(1_000_000), r.IntN(1_000), r.Float64(), r.Float64(), r.Float64())
	}
	return b.Bytes()[:size]
}

func compress(t *testing.T, data []byte, chunk int, level int) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, level)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		if _, err := w.Write(data[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func commonSuffix(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func TestWriterRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		data  []byte
		chunk int
	}{
		{"empty", nil, 1},
		{"one byte", []byte("x"), 1},
		{"below the minimum segment", sampleText(1, minSegment/2), 4096},
		{"several segments in odd chunks", sampleText(2, 3<<20), 7919},
		{"one large write", sampleText(3, 2<<20), 2 << 20},
		{"incompressible", func() []byte {
			r := rand.New(rand.NewPCG(9, 10))
			b := make([]byte, 1<<20)
			for i := range b {
				b[i] = byte(r.UintN(256))
			}
			return b
		}(), 65536},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gunzip(t, compress(t, tc.data, max(tc.chunk, 1), 6))
			if !bytes.Equal(got, tc.data) {
				t.Fatalf("round trip differs: got %d bytes, want %d", len(got), len(tc.data))
			}
		})
	}
}

func TestWriterSegmentsStayWithinBounds(t *testing.T) {
	t.Parallel()
	sizes := memberSizes(t, compress(t, sampleText(4, 4<<20), 32768, 6))
	if len(sizes) < 8 {
		t.Fatalf("expected a 4 MiB input to split into many members, got %d", len(sizes))
	}
	for i, n := range sizes {
		if n > maxSegment {
			t.Errorf("member %d holds %d bytes, over the %d maximum", i, n, maxSegment)
		}
		if n < minSegment && i != len(sizes)-1 {
			t.Errorf("member %d holds %d bytes, under the %d minimum", i, n, minSegment)
		}
	}
}

func memberSizes(t *testing.T, b []byte) []int {
	t.Helper()
	var sizes []int
	br := bytes.NewReader(b)
	r, err := gzip.NewReader(br)
	if err != nil {
		t.Fatal(err)
	}
	for {
		r.Multistream(false)
		n, err := io.Copy(io.Discard, r)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, int(n))
		if err := r.Reset(br); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	return sizes
}

func TestWriterResyncsAfterAnInsertion(t *testing.T) {
	t.Parallel()
	a := sampleText(5, 4<<20)
	insertAt := 1 << 20
	b := append(append(append([]byte{}, a[:insertAt]...), []byte("a hundred bytes of new text inserted into the middle of the dump, long enough to shift everything after it.\n")...), a[insertAt:]...)

	rsA, rsB := compress(t, a, 65536, 6), compress(t, b, 65536, 6)
	if got, want := commonSuffix(rsA, rsB), len(rsA)*6/10; got < want {
		t.Errorf("rsyncable outputs share a %d-byte tail, want at least %d", got, want)
	}

	var plainA, plainB bytes.Buffer
	for _, p := range []struct {
		buf  *bytes.Buffer
		data []byte
	}{{&plainA, a}, {&plainB, b}} {
		w, err := gzip.NewWriterLevel(p.buf, 6)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(p.data)
		_ = w.Close()
	}
	if got := commonSuffix(plainA.Bytes(), plainB.Bytes()); got > plainA.Len()/100 {
		t.Errorf("plain gzip shares a %d-byte tail, the control no longer shows the problem", got)
	}
}

func TestWriterIsDeterministic(t *testing.T) {
	t.Parallel()
	data := sampleText(6, 1<<20)
	if !bytes.Equal(compress(t, data, 1000, 6), compress(t, data, 70000, 6)) {
		t.Fatal("the output depends on how the input was split into writes")
	}
}

func TestNewWriterRejectsAnInvalidLevel(t *testing.T) {
	t.Parallel()
	if _, err := NewWriter(io.Discard, 42); err == nil {
		t.Fatal("expected an error for level 42")
	}
}

func TestWriterReportsADestinationError(t *testing.T) {
	t.Parallel()
	w, err := NewWriter(failingWriter{}, 6)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := w.Write(sampleText(7, 1<<20))
	if werr == nil {
		werr = w.Close()
	}
	if werr == nil {
		t.Fatal("expected the destination's error to surface")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
