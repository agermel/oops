package workspace

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShellOutputCaptureSanitizesChunkedUTF8(t *testing.T) {
	capture := newShellOutputCapture(10, 1024)
	input := []byte("a🙂\x00\r\nb\x02")
	for _, chunk := range [][]byte{input[:3], input[3:5], input[5:]} {
		if _, err := capture.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	result, err := capture.finish()
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if result.content != "a🙂\nb" {
		t.Fatalf("content = %q, want %q", result.content, "a🙂\nb")
	}
	if result.details.Truncated || result.fullOutputPath != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestShellOutputCaptureReplacesInvalidUTF8(t *testing.T) {
	capture := newShellOutputCapture(10, 1024)
	if _, err := capture.Write([]byte{'a', 0xff, 'b'}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := capture.finish()
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if result.content != "a�b" || !utf8.ValidString(result.content) {
		t.Fatalf("content = %q", result.content)
	}
}

func TestShellOutputCaptureSavesFullOutputForLineTruncation(t *testing.T) {
	capture := newShellOutputCapture(2, 1024)
	fullOutput := "one\ntwo\nthree\n"
	if _, err := capture.Write([]byte(fullOutput)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := capture.finish()
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if result.content != "two\nthree" {
		t.Fatalf("content = %q, want %q", result.content, "two\nthree")
	}
	if !result.details.Truncated || result.details.TruncatedBy != "lines" || result.details.TotalLines != 3 {
		t.Fatalf("details = %#v", result.details)
	}
	assertFullOutput(t, result.fullOutputPath, fullOutput)
}

func TestShellOutputCaptureBoundsMemoryAndSavesFullOutput(t *testing.T) {
	capture := newShellOutputCapture(100, 64)
	fullOutput := strings.Repeat("0123456789", 100)
	for start := 0; start < len(fullOutput); start += 37 {
		end := minInt(start+37, len(fullOutput))
		if _, err := capture.Write([]byte(fullOutput[start:end])); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if len(capture.tail) > 128 {
			t.Fatalf("tail bytes = %d, want <= 128", len(capture.tail))
		}
	}
	result, err := capture.finish()
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if len(result.content) != 64 || result.content != fullOutput[len(fullOutput)-64:] {
		t.Fatalf("content = %q", result.content)
	}
	if result.details.TotalBytes != len(fullOutput) || result.details.OutputBytes != 64 {
		t.Fatalf("details = %#v", result.details)
	}
	assertFullOutput(t, result.fullOutputPath, fullOutput)
}

func assertFullOutput(t *testing.T, path, want string) {
	t.Helper()
	if path == "" {
		t.Fatal("missing full output path")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("full output = %q, want %q", data, want)
	}
}
