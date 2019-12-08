package trap

import (
	"container/list"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
)

type Callback func()
type CallbackRemover func()

var (
	wait    sync.WaitGroup
	mux     sync.RWMutex
	cbsList map[os.Signal]*list.List
	ch      chan os.Signal
)

func init() {
	cbsList = make(map[os.Signal]*list.List)
	ch = make(chan os.Signal, 3)

	wait.Add(1)

	go func() {
		defer wait.Done()

		var s os.Signal
		var ok bool

		for true {
			// When the channel is closed, trap instance should exit
			if s, ok = <-ch; !ok {
				break
			}

			processSignal(s)
		}
	}()
}

func processSignal(s os.Signal) {
	mux.Lock()
	defer mux.Unlock()

	// Try and select the list, continue otherwise
	cbs, ok := cbsList[s]

	if !ok {
		return
	}

	switch s {
	case syscall.SIGKILL, syscall.SIGINT, syscall.SIGQUIT:
		// Ensure quit callbacks are only run once!
		cbsList[syscall.SIGKILL] = list.New()
		cbsList[syscall.SIGINT] = list.New()
		cbsList[syscall.SIGQUIT] = list.New()
	}

	// Execute from back to front, so the order is FILO
	for e := cbs.Back(); e != nil; e = e.Prev() {
		if cb, cbok := e.Value.(Callback); cbok {
			cb()
		} else {
			// This should never happen,
			// but handled here anyway so it does not error silently
			fmt.Printf("trap error: list item not a callback")
		}
	}
}

// Deferrer should be called when the go routing terminates,
// as part of the deferred functions, for normal signal termination, normal termination AND panics.
// This will ensure any caught signals won't be written to a closed channel.
func Deferrer() {
	if err := recover(); err != nil {
		fmt.Printf("Panic received, attempting gracefull exit\nError:\n%s", err)

		debug.PrintStack()

		defer os.Exit(1)
	}

	signal.Stop(ch)

	// Send an interrupt signal to the channel to ensure all kill callbacks are triggered,
	// even on panic or normal termination
	ch <- syscall.SIGINT

	close(ch)

	wait.Wait()
}

func OnReload(cb Callback) CallbackRemover {
	return OnSignal(syscall.SIGUSR1, cb)
}

func OnKill(cb Callback) CallbackRemover {
	var rfns []func()

	rfns = append(rfns, OnSignal(syscall.SIGKILL, cb))
	rfns = append(rfns, OnSignal(syscall.SIGQUIT, cb))
	rfns = append(rfns, OnSignal(syscall.SIGINT, cb))

	return func() {
		for _, dfn := range rfns {
			dfn()
		}
	}
}

func OnSignal(sig os.Signal, cb Callback) CallbackRemover {
	mux.Lock()
	defer mux.Unlock()

	l, ok := cbsList[sig]

	if !ok {
		signal.Notify(ch, sig)

		l = list.New()

		cbsList[sig] = l
	}

	e := l.PushBack(cb)

	return func() {
		mux.Lock()
		defer mux.Unlock()

		l.Remove(e)
	}
}
