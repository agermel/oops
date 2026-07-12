package api

import (
	"context"
	"io"
	"net/http"
)

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
