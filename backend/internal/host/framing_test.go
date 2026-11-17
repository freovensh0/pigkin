package host

import (
	"bufio"
	"strings"
	"testing"
)

func TestScanLinesSplitsOnLFOnly(t *testing.T) {
	// U+2028 is legal inside a JSON string, so it must not split the line.
	input := "{\"text\":\"a\u2028b\"}\r\n{\"n\":2}\n"
	var lines []string
	scanner := ScanLines(strings.NewReader(input))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"{\"text\":\"a\u2028b\"}", "{\"n\":2}"}
	if len(lines) != len(want) || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("got %q, want %q", lines, want)
	}
}

func TestScanLinesRejectsOverlongLine(t *testing.T) {
	scanner := ScanLines(bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", MaxLine+1)+"\n"), 1024))
	for scanner.Scan() {
	}
	if scanner.Err() == nil {
		t.Fatal("want an error for a line over MaxLine")
	}
}

func TestEncodeLine(t *testing.T) {
	line, err := EncodeLine(Message{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "{\"ok\":true}\n" {
		t.Fatalf("got %q", line)
	}
}
