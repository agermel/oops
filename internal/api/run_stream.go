package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func streamRunItems(w io.Writer, flusher http.Flusher, events <-chan runStreamItem) error {
	if _, err := fmt.Fprintf(w, ":ok\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	for item := range events {
		if err := writeNamedSSE(w, flusher, item.name, item.payload); err != nil {
			return err
		}
	}
	return nil
}

func streamRunSubscription(w io.Writer, flusher http.Flusher, subscription *runSubscription, ctx context.Context) error {
	for {
		frame, live, ok := subscription.next(ctx)
		if !ok {
			return nil
		}
		if _, err := w.Write(frame.data); err != nil {
			return err
		}
		flusher.Flush()
		if live {
			subscription.acknowledge(frame)
		}
	}
}
