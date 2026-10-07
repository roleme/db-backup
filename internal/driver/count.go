package driver

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"regexp"
)

type gzFile struct {
	*gzip.Reader
	f *os.File
}

func (g gzFile) Close() error {
	g.Reader.Close()
	return g.f.Close()
}

func openGzip(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return gzFile{gz, f}, nil
}

func countTables(path string, re *regexp.Regexp) (int, error) {
	r, err := openGzip(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	br := bufio.NewReaderSize(r, 64<<10)
	n := 0
	atStart := true
	for {
		line, isPrefix, err := br.ReadLine()
		if len(line) > 0 || err == nil {
			if atStart && re.Match(line) {
				n++
			}
			atStart = !isPrefix
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
