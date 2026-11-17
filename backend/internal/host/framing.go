package host

import (
	"bufio"
	"encoding/json"
	"io"
)

// MaxLine bounds one JSONL line. Images travel base64 inside prompts, so lines
// are allowed to be large.
const MaxLine = 16 * 1024 * 1024

// EncodeLine writes msg as one JSON line, the only framing the app and the host
// share: LF is the delimiter and JSON carries its own escaping.
func EncodeLine(msg map[string]any) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ScanLines reads JSONL lines from r. It splits on LF only, unlike bufio's
// default scanners, and strips a trailing CR; U+2028 and U+2029 are legal
// inside JSON strings and must not split.
func ScanLines(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), MaxLine)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b != '\n' {
				continue
			}
			line := data[:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return i + 1, line, nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	return scanner
}
