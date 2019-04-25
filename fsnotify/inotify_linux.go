//+build ignore 

/*
Package fsnotify implements a wrapper for the Linux inotify system.

Re-implemented (instead of using exp/inotify) because I need to keep 
track of paths et al myself.

*/
package fsnotify

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

type Event struct {
	Mask   uint32 
	Cookie uint32 
	Name   string 
	WatchDesc  uint32
}

type Watcher struct {
	FD       int          // File descriptor (as returned by the inotify_init() syscall)
	Error    chan error   // Errors are sent on this channel
	Event    chan *Event  // Events are returned on this channel
	done     chan bool    // Channel for sending a "quit message" to the reader goroutine
	isClosed bool         // Set to true when Close() is first called
}

// NewWatcher creates and returns a new inotify instance using inotify_init(2)
func NewWatcher() (w *Watcher, err error) {
	fd, err := syscall.InotifyInit()
	if err != nil {
		return
	}
	if fd == -1 {
		err = os.NewSyscallError("inotify_init", err)
		return
	}
	w = &Watcher{
		FD:      fd,
		Event:   make(chan *Event),
		Error:   make(chan error),
		done:    make(chan bool),
	}
	return 
}

// Close reader goroutine by sending "done" message and waiting till done.
func (w *Watcher) CloseReader() (err error) {
	if w.isClosed {
		return 
	}
	// Send "quit" message to the reader goroutine
	w.done <- true
	<- w.done
	w.isClosed = true
	return 
}

// readEvents reads from the inotify file descriptor, converts the
// received events into Event objects and sends them via the Event channel
func (w *Watcher) ReadEvents() {
	var buf [syscall.SizeofInotifyEvent * 4096]byte

	for {
		// See if there is a message on the "done" channel
		var (
			done bool
			n int
			err error
		)
		select {
		case done = <-w.done:
		default:
		}
		if !done {
			n, err = syscall.Read(w.FD, buf[0:])
		}
		// If EOF or a "done" message is received
		if n == 0 || done {
			if err = syscall.Close(w.FD); err != nil {
				w.Error <- os.NewSyscallError("close", err)
			}
			close(w.Event)
			close(w.Error)
			close(w.done)
			w.isClosed = true
			return
		}
		if n < 0 {
			w.Error <- os.NewSyscallError("read", err)
			continue
		}
		if n < syscall.SizeofInotifyEvent {
			w.Error <- errors.New("inotify: short read in readEvents()")
			continue
		}

		var offset uint32 = 0
		for offset <= uint32(n-syscall.SizeofInotifyEvent) {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			event := Event{
				WatchDesc: uint32(raw.Wd),
				Mask: uint32(raw.Mask),
				Cookie: uint32(raw.Cookie),
			}
			nameLen := uint32(raw.Len)
			if nameLen > 0 {
				bytes := (*[syscall.PathMax]byte)(unsafe.Pointer(&buf[offset+syscall.SizeofInotifyEvent]))
				event.Name = strings.TrimRight(string(bytes[0:nameLen]), "\000") // filename padded w/ NUL bytes
			}
			w.Event <- &event
			offset += syscall.SizeofInotifyEvent + nameLen
		}
	}
}

