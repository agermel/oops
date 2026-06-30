package api

import (
	"fmt"
	"io"
	"net/http"
)

// setSSEHeaders writes standard SSE response headers.
func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// requireFlusher asserts w implements http.Flusher.
func requireFlusher(w http.ResponseWriter) (http.Flusher, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	return flusher, nil
}

// copyAndFlush copies from reader to writer, flushing after each chunk.
func copyAndFlush(w io.Writer, flusher http.Flusher, reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}
