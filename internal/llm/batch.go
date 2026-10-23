package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MaxParallel caps how many read-only tool calls run at the same time.
const MaxParallel = 6

// CallTimeout bounds one read-only call (a search, a fetch, a file read). A call that
// runs longer is answered with a timeout error so the rest of the batch can go back to
// the model; its context is cancelled so the tool can give up. Calls that change things
// (writes, commands, approvals) are never cut off here: they have their own limits.
var CallTimeout = 2 * time.Minute

// RunBatch runs a model's tool calls 0..n-1 in order, except that a run of consecutive
// calls safe(i) allows (reads: file reads, searches, fetches) runs at the same time, up
// to MaxParallel at once, like the Blaze CLI's batchEnd/runBatch. Anything else (a
// write, a command, git, an approval) runs on its own and ends the run of reads.
//
// start and finish are always called in call order on the caller's goroutine, so
// history and the UI see the calls exactly as the model made them; only run happens
// concurrently. Each read gets its own context with CallTimeout, and a call that
// panics, errors or times out only fails itself: the others keep their results.
// start returning false stops the batch (Stop was pressed): calls already started still
// finish. It reports whether every call ran.
func RunBatch(ctx context.Context, n int, safe func(i int) bool, start func(i int) bool, run func(ctx context.Context, i int) string, finish func(i int, res string)) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	for i := 0; i < n; {
		if !safe(i) {
			if !start(i) {
				return false
			}
			finish(i, guard(ctx, i, run))
			i++
			continue
		}
		j := i + 1
		for j < n && safe(j) {
			j++
		}
		res := make([]string, j-i)
		var wg sync.WaitGroup
		sem := make(chan struct{}, MaxParallel)
		started := i
		for k := i; k < j; k++ {
			if !start(k) {
				break
			}
			started = k + 1
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res[k-i] = timed(ctx, k, run)
			}(k)
		}
		wg.Wait()
		for k := i; k < started; k++ {
			finish(k, res[k-i])
		}
		if started < j {
			return false
		}
		i = j
	}
	return true
}

// PanicTap lets tests see a recovered tool panic.
var PanicTap func(any)

// guard runs one call and turns a panic into an error result.
func guard(ctx context.Context, i int, run func(ctx context.Context, i int) string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			if PanicTap != nil {
				PanicTap(r)
			}
			out = fmt.Sprintf("Error: the tool crashed (%v). Try again or take a different approach.", r)
		}
	}()
	return run(ctx, i)
}

// timed runs one read with its own deadline. A call that overruns is answered with a
// timeout error at once; its late result is dropped.
func timed(ctx context.Context, i int, run func(ctx context.Context, i int) string) string {
	cctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	ch := make(chan string, 1)
	go func() { ch <- guard(cctx, i, run) }()
	select {
	case r := <-ch:
		return r
	case <-cctx.Done():
		if ctx.Err() != nil {
			return "Stopped."
		}
		return fmt.Sprintf("Error: this call took longer than %s and was stopped. The other results are fine; try it again or another way.", CallTimeout)
	}
}
