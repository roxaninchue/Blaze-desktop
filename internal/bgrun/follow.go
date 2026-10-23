package bgrun

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Device runs one tool call on this computer. ok is false when the tool failed (its result is
// still sent back, so the model can say so honestly).
type Device func(ctx context.Context, call ToolCall) (result string, ok bool)

// Follower follows one run to its final, answering device tool calls on the way.
type Follower struct {
	API    API
	Device Device
	// OnDelta gets streamed reply text (reset=true: drop what this step showed so far). Optional:
	// only a run the user is watching in the foreground streams.
	OnDelta func(text string, reset bool)
	// OnEvent sees every run event and OnCall every device tool call before it runs (the chat's
	// working pill and delivery status read them through internal/runevents). Optional.
	OnEvent func(e Event)
	OnCall  func(c ToolCall)
	// Sleep waits between polls; tests replace it. It returns early when ctx ends.
	Sleep func(ctx context.Context, d time.Duration) error
	// Poll is the first wait between polls (it grows to MaxPoll while nothing happens).
	Poll, MaxPoll time.Duration
	// NetTries is how many failed polls in a row end the follow (the run stays on the gateway
	// and the next resume picks it up).
	NetTries int
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrLost is a follow that gave up on the network; the run is still on the gateway.
var ErrLost = errors.New("lost touch with the run")

// Follow polls run id (events after `after`) until it is done or failed. action marks a request
// that had an ack (its done shows "Done ✓ …"). It returns ctx.Err() when ctx ends first: the run
// keeps going on the gateway and can be followed again.
func (f *Follower) Follow(ctx context.Context, id string, after int, action bool) (Final, error) {
	sleep := f.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	poll, maxPoll := f.Poll, f.MaxPoll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	if maxPoll <= 0 {
		maxPoll = 3 * time.Second
	}
	tries := f.NetTries
	if tries <= 0 {
		tries = 6
	}
	fin := Final{RunID: id, Action: action, DeviceOK: true}
	var done, failed *Event
	answered := map[string]bool{} // tool calls already answered (a slow status never re-runs a tool)
	wait, fails := poll, 0
	for {
		if err := ctx.Err(); err != nil {
			return fin, err
		}
		v, err := f.API.Get(ctx, id, after)
		if err != nil {
			if ctx.Err() != nil {
				return fin, ctx.Err()
			}
			if c := StatusOf(err); c == 404 || c == 410 {
				fin.Kind, fin.Code = KindFailed, "gone"
				fin.Reason = "This task is no longer on Blaze Cloud, so I can't tell how it ended."
				fin.Try = "Ask again and I'll start it fresh."
				return fin, nil
			}
			fails++
			if fails >= tries {
				return fin, ErrLost
			}
			if err := sleep(ctx, backoff(fails)); err != nil {
				return fin, err
			}
			continue
		}
		fails = 0
		moved := len(v.Events) > 0
		for i := range v.Events {
			e := v.Events[i]
			if f.OnEvent != nil {
				f.OnEvent(e)
			}
			switch e.Type {
			case "delta":
				if f.OnDelta != nil && e.Text != "" {
					f.OnDelta(e.Text, false)
				}
			case "reset":
				if f.OnDelta != nil {
					f.OnDelta("", true)
				}
			case "done":
				done = &e
			case "failed":
				failed = &e
			case "error":
				if failed == nil {
					failed = &e
				}
			}
		}
		if v.NextAfter > after {
			after = v.NextAfter
		}
		switch {
		case v.Status == StDone:
			fin.Kind = KindDone
			if done != nil {
				fin.Reply, fin.Receipt = done.Reply, done.Receipt
			}
			if strings.TrimSpace(fin.Reply) == "" {
				fin.Reply = v.Reply
			}
			return fin, nil
		case v.Status == StFailed || v.Status == StCancelled || v.Status == StExpired:
			fin.Kind = KindFailed
			fin.Code, fin.Reason, fin.Try = failReason(v, failed)
			return fin, nil
		case v.Status == StNeedsDevice && len(v.Pending) > 0:
			var res []ToolResult
			for _, c := range v.Pending {
				if answered[c.ID] {
					continue
				}
				if f.OnCall != nil {
					f.OnCall(c)
				}
				out, ok := "Error: this computer can't run that tool.", false
				if f.Device != nil {
					out, ok = f.Device(ctx, c)
				}
				fin.DeviceRan = true
				fin.DeviceOK = fin.DeviceOK && ok
				res = append(res, ToolResult{ToolCallID: c.ID, Content: out})
			}
			if ctx.Err() != nil {
				return fin, ctx.Err()
			}
			if len(res) > 0 {
				if err := f.API.ToolResults(ctx, id, res); err != nil && StatusOf(err) != 409 {
					// 409: already answered (another follower or a retry). Anything else: try again.
					fails++
					if fails >= tries {
						return fin, ErrLost
					}
					if err := sleep(ctx, backoff(fails)); err != nil {
						return fin, err
					}
					continue
				}
				for _, r := range res {
					answered[r.ToolCallID] = true
				}
				wait = poll
				continue
			}
		}
		if v.More {
			continue
		}
		if moved {
			wait = poll
		} else if wait < maxPoll {
			wait = wait * 3 / 2
			if wait > maxPoll {
				wait = maxPoll
			}
		}
		if v.RetryInMs > 0 && time.Duration(v.RetryInMs)*time.Millisecond > wait {
			wait = time.Duration(v.RetryInMs) * time.Millisecond
			if wait > 15*time.Second {
				wait = 15 * time.Second
			}
		}
		if err := sleep(ctx, wait); err != nil {
			return fin, err
		}
	}
}

func backoff(n int) time.Duration {
	d := time.Duration(n) * time.Second
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

// failReason is an honest reason for a failed / cancelled / expired run, from the failed event
// (gateway 0.8.38), else the older error event or the view's error. Never a provider or model.
func failReason(v *View, e *Event) (code, reason, try string) {
	if e != nil {
		code, reason, try = e.Code, e.Reason, e.Try
		if reason == "" {
			reason = e.MessageText()
		}
	}
	if v.Error != nil {
		if code == "" {
			code = v.Error.Code
		}
		if reason == "" {
			reason = v.Error.Message
		}
	}
	switch v.Status {
	case StCancelled:
		if reason == "" {
			reason = "This task was stopped before it finished."
		}
		if code == "" {
			code = "cancelled"
		}
	case StExpired:
		if reason == "" {
			reason = "This task ran out of time before it finished."
		}
		if code == "" {
			code = "expired"
		}
	}
	if reason == "" {
		reason = "Blaze Cloud couldn't finish this."
	}
	if try == "" {
		switch code {
		case "models_busy":
			try = "Try again in a minute."
		default:
			try = "Ask again and I'll have another go."
		}
	}
	return code, reason, try
}
