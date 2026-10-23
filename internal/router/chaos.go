package router

// Chaos presets (auto/chaos, auto/best-chaos): like OmniRoute's chaos mode, a request
// fans out to a panel of the steadiest models from different providers at the same
// time. The first model to accept it answers; the rest are cancelled.

import (
	"context"
	"errors"
	"io"
	"strings"
)

type cancelCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (r *Router) dispatchChaos(ctx context.Context, body map[string]any, pl Plan, stream bool) (*upstream, error) {
	type res struct {
		i   int
		up  *upstream
		err error
	}
	n := len(pl.Targets)
	ch := make(chan res, n)
	cancels := make([]context.CancelFunc, n)
	for i, t := range pl.Targets {
		cctx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		one := Plan{Asked: pl.Asked, Combo: pl.Combo, Targets: []Target{t}}
		go func() {
			up, err := r.dispatch(cctx, body, one, stream)
			ch <- res{i, up, err}
		}()
	}
	var attempts []Attempt
	var msgs []string
	code := 502
	for got := 1; got <= n; got++ {
		x := <-ch
		if x.err == nil {
			for i, c := range cancels {
				if i != x.i {
					c() // stop the rest of the panel
				}
			}
			go func(left int) { // close any slower answers that still arrive
				for ; left > 0; left-- {
					if y := <-ch; y.err == nil {
						y.up.resp.Body.Close()
					}
				}
			}(n - got)
			up := x.up
			up.resp.Body = cancelCloser{up.resp.Body, cancels[x.i]}
			up.plan = pl
			up.attempts = append(attempts, up.attempts...)
			return up, nil
		}
		cancels[x.i]()
		var re *RouteError
		if errors.As(x.err, &re) {
			attempts = append(attempts, re.Attempts...)
			msgs = append(msgs, strings.TrimPrefix(re.Msg, "BlazeRouter: no model could answer. "))
			if re.Code == 429 {
				code = 429
			}
		} else if ctx.Err() == nil {
			msgs = append(msgs, x.err.Error())
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, &RouteError{Code: code, Msg: "BlazeRouter: no model in the chaos panel could answer. " + strings.Join(msgs, "; "), Attempts: attempts}
}
