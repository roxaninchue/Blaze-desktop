package runevents

import (
	"context"
	"time"

	"blaze-desktop/internal/bgrun"
)

// Offline retry: a create that can't reach Blaze (no HTTP status at all) is tried again with the
// SAME request, so the same client_msg_id: the gateway is idempotent on it, so a try that did land
// before the network dropped is answered with that run (existing), never a second one.

// RetryWaits are the waits between tries (then the last one repeats until MaxTries).
// (STEPS_DELIVERY_CONTRACT.md 1: backoff 1, 2, 4, 8, 16, 30, 30 … s).
var RetryWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}

// PerTry is one create's time limit (a try that times out counts as offline and is retried).
var PerTry = 20 * time.Second

// MaxTries is how many creates one message gets before the app gives up on the run (~3.5 min).
var MaxTries = 12

// Create posts req until the gateway answers. emit sees message_sending for each try (Offline set
// when the try before it could not reach Blaze) and message_received on the answer. An HTTP error
// (the gateway answered) is returned at once, except 408 / 429; "couldn't reach it" is retried.
func Create(ctx context.Context, api bgrun.API, req bgrun.CreateReq, emit func(Ev), sleep func(context.Context, time.Duration) error) (*bgrun.Created, error) {
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if emit == nil {
		emit = func(Ev) {}
	}
	var lastErr error
	for try := 1; try <= MaxTries; try++ {
		emit(Sent(req.ClientMsgID, try, try > 1))
		tctx, cancel := context.WithTimeout(ctx, PerTry)
		cr, err := api.Create(tctx, req)
		cancel()
		if err == nil {
			for _, e := range Delivered(cr, req.ClientMsgID) {
				emit(e)
			}
			return cr, nil
		}
		lastErr = err
		if c := bgrun.StatusOf(err); (c != 0 && c != 408 && c != 429) || ctx.Err() != nil {
			return nil, err // the gateway answered: no auto retry (408 / 429 are retried)
		}
		w := RetryWaits[len(RetryWaits)-1]
		if try-1 < len(RetryWaits) {
			w = RetryWaits[try-1]
		}
		if err := sleep(ctx, w); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}
