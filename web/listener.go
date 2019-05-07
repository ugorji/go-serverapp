package web

import (
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/ugorji/go-common/runtimeutil"
)

type OnPanicFlags uint8

const (
	OnPanicRecover OnPanicFlags = 1 << iota
	OnPanicLogStack
	OnPanicLog

	OnPanicAll = 0xff // convenience all flags set
)

var ClosedErr = errors.New("<closed>")

type conn struct {
	l *Listener
	net.Conn
}

func (c *conn) Close() error {
	if c.l.trackConnOnAccept {
		atomic.AddInt32(&c.l.numConn, -1)
	}
	return c.Conn.Close()
}

// Listener implements net.Listener
//
// It keeps track of connections doing work.
// It will pause accepting new connections if maxConn is
// above a given max, and resume accepting when below.
//
// It will also close gracefully, giving in-flight requests time to
// shutdown gracefully.
//
// Closing the listener also returns an appropriate error (ClosedErr)
// so that users know that it was closed gracefully.
//
type Listener struct {
	closed            uint32 // 0 or 1 atomically
	paused            uint32 // 0 or 1 atomically
	hardPaused        uint32 // 0 or 1 atomically
	maxNumConnHi      int32
	maxNumConnLo      int32
	numConn           int32
	l                 net.Listener
	pausedCond        *sync.Cond
	closedCond        *sync.Cond
	zeroCond          *sync.Cond
	panicFlags        OnPanicFlags
	trackConnOnAccept bool
}

// Return a new listener.
func NewListener(l net.Listener, maxNumConn int32, panicFlags OnPanicFlags) (s *Listener) {
	if maxNumConn <= 0 {
		maxNumConn = 10
	}
	// println(">>>> maxNumConn: ", maxNumConn)
	s = &Listener{
		l:                 l,
		maxNumConnHi:      maxNumConn,
		maxNumConnLo:      int32(float32(maxNumConn) * 0.95),
		pausedCond:        sync.NewCond(new(sync.Mutex)),
		closedCond:        sync.NewCond(new(sync.Mutex)),
		zeroCond:          sync.NewCond(new(sync.Mutex)),
		panicFlags:        panicFlags,
		trackConnOnAccept: false,
		// we track in-flight requests, regardless of if conn made.
		// If not, Close will hang, since close waits for 0.
		// Also, if true, then it will not interact well with keepAlive. So always leave as false.
	}
	return
}

func (s *Listener) ResetMaxNumConn(maxNumConn int32) {
	// Ensure that server is not closed, and no in flight conns, and hardPaused
	// if atomic.LoadUint32(&s.closed) == 0 &&
	// 	atomic.LoadInt32(&s.numConn) == 0 &&
	// 	atomic.LoadInt32(&s.hardPaused) == 1 {

	// Just update values atomically. No checks needed. It's atomic.
	if maxNumConn <= 0 {
		maxNumConn = 10
	}
	atomic.StoreInt32(&s.maxNumConnHi, maxNumConn)
	atomic.StoreInt32(&s.maxNumConnLo, int32(float32(maxNumConn)*0.95))
}

func (s *Listener) handlePanic() {
	if s.panicFlags&OnPanicRecover == 0 {
		return
	}
	if x := recover(); x != nil {
		if s.panicFlags&OnPanicLog != 0 {
			switch v := x.(type) {
			case error:
				log.IfError(nil, v, "Panic Recovered during ServeHTTP")
			default:
				log.Error(nil, "Panic Recovered during ServeHTTP: %v", x)
			}
		}
		if s.panicFlags&OnPanicLogStack != 0 {
			log.Debug(nil, "Stack from Panic Recovered during ServeHTTP: \n%s", runtimeutil.Stack(nil, false))
		}
	}
}

func (s *Listener) Run(onClosed, onPaused, onRun func()) {
	//If closed, set Connection Closed Header, so that this connection is not reused.
	if atomic.LoadUint32(&s.closed) == 1 {
		onClosed()
		return
	}

	var n int32

	if s.trackConnOnAccept {
		n = atomic.LoadInt32(&s.numConn)
	} else {
		n = atomic.AddInt32(&s.numConn, 1)
	}

	if atomic.LoadUint32(&s.hardPaused) == 1 || atomic.LoadUint32(&s.paused) == 1 {
		onPaused()
	} else if nHi := atomic.LoadInt32(&s.maxNumConnHi); n >= nHi {
		if atomic.CompareAndSwapUint32(&s.paused, 0, 1) {
			log.Warning(nil, "PAUSE: Reached max num connections threshold (%d): %d", nHi, n)
		}
	}

	// use defer here, to ensure that calculations are performed regardless at end of function.
	// We saw situations before where a hang in onRun caused whole server to hang.
	defer s.afterRun()

	// We explicitly do not use a timeout here for running requests.
	// If we do, then it's possible that requests are still running but we are acting
	// like they have been completed.
	//
	// The onus is on the handlers to ensure that requests are completed in good time.
	// All requests must be completed in good time.
	onRun()
}

func (s *Listener) afterRun() {
	var n int32
	if s.trackConnOnAccept {
		n = atomic.LoadInt32(&s.numConn)
	} else {
		n = atomic.AddInt32(&s.numConn, -1)
	}
	if nLo := atomic.LoadInt32(&s.maxNumConnLo); n <= nLo { //handle when Lo=0 (e.g. if Hi=1)
		if atomic.CompareAndSwapUint32(&s.paused, 1, 0) {
			log.Warning(nil, "UNPAUSE. Below max num connections threshold (%d): %d", nLo, n)
			signalCond(s.pausedCond)
		}
		if n == 0 {
			if atomic.LoadUint32(&s.closed) == 1 {
				signalCond(s.closedCond)
			}
			signalCond(s.zeroCond)
		}
	}
}

func (s *Listener) Accept() (c net.Conn, err error) {
	if atomic.LoadUint32(&s.closed) == 1 {
		return nil, ClosedErr
	}
	// use condition to wait if numConn is at maxConn, and wait till below threshold.
	// Do not wait till Accept returns before releasing lock.
	waitCond(s.pausedCond, true, func() bool {
		return atomic.LoadUint32(&s.hardPaused) == 1 || atomic.LoadUint32(&s.paused) == 1
	})
	c, err = s.l.Accept()
	if s.trackConnOnAccept {
		if err == nil && c != nil {
			atomic.AddInt32(&s.numConn, 1)
			c = &conn{s, c}
		} else {
			c = nil
		}
	}
	return
}

func (s *Listener) Close() (err error) {
	if !atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		return
	}
	err = s.l.Close() //This unblocks Accept.
	//wait for all connections to close, ie wait till numConn==0.
	waitCond(s.closedCond, true, func() bool { return atomic.LoadInt32(&s.numConn) > 0 })
	return
}

func (s *Listener) HardPause() {
	if atomic.LoadUint32(&s.closed) == 1 {
		return
	}
	// atomic.CompareAndSwapUint32(&s.paused, 0, 1)
	atomic.StoreUint32(&s.hardPaused, 1)
}

func (s *Listener) WaitZeroInflight() {
	waitCond(s.zeroCond, true, func() bool { return atomic.LoadInt32(&s.numConn) != 0 })
}

func (s *Listener) ResumeFromHardPause() {
	if atomic.LoadUint32(&s.closed) == 1 {
		return
	}
	// atomic.CompareAndSwapUint32(&s.paused, 1, 0)
	atomic.StoreUint32(&s.hardPaused, 0)
	signalCond(s.pausedCond)
	return
}

func (s *Listener) IsClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *Listener) Addr() net.Addr {
	return s.l.Addr()
}

func closeFile(w *os.File, name string) {
	// Note: We pass *File, not io.Writer, so that a nil doesn't get turned into a non-nil io.Writer
	if w == nil {
		return
	}
	log.IfError(nil, w.Close(), "Error closing file: %s", name)
}

func signalCond(c *sync.Cond) {
	// Even while signalling, we have to lock, to prevent races.
	c.L.Lock()
	c.Broadcast()
	c.L.Unlock()
}

func waitCond(c *sync.Cond, unlock bool, fn func() bool) {
	c.L.Lock()
	for fn() {
		c.Wait()
	}
	if unlock {
		c.L.Unlock()
	}
}

// Don't use Helper function for logging, else wrong file/line info is in log file
// func logErr(err error, message string, params ...interface{}) {
// 	if err != nil {
// 		log.IfError(nil, err, message, params...)
// 	}
// }
