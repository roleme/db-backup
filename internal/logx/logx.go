package logx

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	Out io.Writer = os.Stdout
	Err io.Writer = os.Stderr
	Now           = time.Now
)

func stamp() string {
	return Now().Format("2006-01-02 15:04:05")
}

func Infof(format string, a ...any) {
	fmt.Fprintf(Out, "%s %s\n", stamp(), fmt.Sprintf(format, a...))
}

func Warnf(format string, a ...any) {
	fmt.Fprintf(Err, "%s WARN: %s\n", stamp(), fmt.Sprintf(format, a...))
}

func Errorf(format string, a ...any) {
	fmt.Fprintf(Err, "%s ERROR: %s\n", stamp(), fmt.Sprintf(format, a...))
}
