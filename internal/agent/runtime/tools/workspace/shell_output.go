package workspace

import (
	"bytes"
	"errors"
	"os"
	"unicode/utf8"
)

type shellCaptureResult struct {
	content        string
	details        truncationDetails
	fullOutputPath string
}

type shellOutputCapture struct {
	maxLines int
	maxBytes int
	tail     []byte
	pending  []byte

	totalBytes int
	newlines   int
	lastByte   byte
	hasOutput  bool

	fullOutput     *os.File
	fullOutputPath string
	captureErr     error
	finished       bool
}

func newShellOutputCapture(maxLines, maxBytes int) *shellOutputCapture {
	return &shellOutputCapture{maxLines: maxLines, maxBytes: maxBytes}
}

func (c *shellOutputCapture) Write(p []byte) (int, error) {
	if c.finished {
		return 0, errors.New("shell output capture is closed")
	}
	originalLength := len(p)
	data := p
	if len(c.pending) > 0 {
		combined := make([]byte, 0, len(c.pending)+len(p))
		combined = append(combined, c.pending...)
		combined = append(combined, p...)
		c.pending = c.pending[:0]
		data = combined
	}

	var sanitized bytes.Buffer
	sanitized.Grow(len(data))
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			c.pending = append(c.pending[:0], data...)
			break
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			sanitized.WriteRune(utf8.RuneError)
		} else if keepShellRune(r) {
			sanitized.WriteRune(r)
		}
		data = data[size:]
	}
	c.appendSanitized(sanitized.Bytes())
	return originalLength, nil
}

func (c *shellOutputCapture) finish() (shellCaptureResult, error) {
	if c.finished {
		return shellCaptureResult{}, errors.New("shell output capture already finished")
	}
	c.finished = true
	c.flushPending()

	result := truncateTail(string(c.tail), c.maxLines, c.maxBytes)
	result.details.TotalBytes = c.totalBytes
	result.details.TotalLines = c.totalLines()
	if c.totalBytes > c.maxBytes || c.totalLines() > c.maxLines {
		result.details.Truncated = true
		if result.details.TruncatedBy == "" {
			if c.totalBytes > c.maxBytes {
				result.details.TruncatedBy = "bytes"
			} else {
				result.details.TruncatedBy = "lines"
			}
		}
	}
	if result.details.Truncated {
		c.ensureFullOutput()
	}
	if c.fullOutput != nil {
		if err := c.fullOutput.Close(); err != nil && c.captureErr == nil {
			c.captureErr = err
		}
	}
	return shellCaptureResult{
		content:        result.content,
		details:        result.details,
		fullOutputPath: c.fullOutputPath,
	}, c.captureErr
}

func (c *shellOutputCapture) flushPending() {
	if len(c.pending) == 0 {
		return
	}
	var sanitized bytes.Buffer
	for len(c.pending) > 0 {
		r, size := utf8.DecodeRune(c.pending)
		if r == utf8.RuneError && size == 1 {
			sanitized.WriteRune(utf8.RuneError)
		} else if keepShellRune(r) {
			sanitized.WriteRune(r)
		}
		c.pending = c.pending[size:]
	}
	c.appendSanitized(sanitized.Bytes())
}

func keepShellRune(r rune) bool {
	if r == '\r' {
		return false
	}
	if r == '\t' || r == '\n' {
		return true
	}
	if r <= 0x1f {
		return false
	}
	return r < 0xfff9 || r > 0xfffb
}

func (c *shellOutputCapture) appendSanitized(data []byte) {
	if len(data) == 0 {
		return
	}
	previousBytes := c.totalBytes
	c.totalBytes += len(data)
	c.newlines += bytes.Count(data, []byte{'\n'})
	c.lastByte = data[len(data)-1]
	c.hasOutput = true

	if c.fullOutput == nil && previousBytes <= c.maxBytes && c.totalBytes > c.maxBytes {
		c.ensureFullOutput()
	}
	if c.fullOutput != nil && c.captureErr == nil {
		if _, err := c.fullOutput.Write(data); err != nil {
			c.captureErr = err
		}
	}

	c.tail = append(c.tail, data...)
	memoryLimit := c.maxBytes * 2
	if len(c.tail) > memoryLimit {
		start := len(c.tail) - memoryLimit
		for start < len(c.tail) && !utf8.RuneStart(c.tail[start]) {
			start++
		}
		c.tail = append(c.tail[:0], c.tail[start:]...)
	}
}

func (c *shellOutputCapture) ensureFullOutput() {
	if c.fullOutput != nil || c.captureErr != nil {
		return
	}
	file, err := os.CreateTemp("", "agent-bash-*.log")
	if err != nil {
		c.captureErr = err
		return
	}
	c.fullOutput = file
	c.fullOutputPath = file.Name()
	if len(c.tail) == 0 {
		return
	}
	if _, err := file.Write(c.tail); err != nil {
		c.captureErr = err
		_ = file.Close()
		_ = os.Remove(file.Name())
		c.fullOutput = nil
		c.fullOutputPath = ""
	}
}

func (c *shellOutputCapture) totalLines() int {
	if !c.hasOutput {
		return 1
	}
	lines := c.newlines + 1
	if c.lastByte == '\n' {
		lines--
	}
	return lines
}
