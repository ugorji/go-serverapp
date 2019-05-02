//+build linux

package fsnotify

// watcher tracks the following events:
//    - dir create, delete, move
//    - file write, delete, move
//
// configFile changes and directory moves always trigger a full reload.
// It's not incremental.
//
// Also, any incremental changes to page files (*.page.*) will recreate
// static site completely (if StaticSite=true). There is no "incremental"
// generation of static site data, because tags, feeds and dir indexes
// can refer to files beyond a single file change.
//
// For these MACRO updates (full reload, createStatic), the system will
// bundle the updates and do it once at the end of processing a bunch of reads.
//
// syscall.Read is typically a blocking call.
// Thus, we SHOULDN'T close it from a different thread, since the behaviour
// in linux is undefined.
//
// To accomodate, we use select with a 1 second timeout, and use non-blocking
// reads, so read never hangs. We then also close the file descriptor within the
// readEvents loop.
//
// The design affords the following:
//   - User can use a sleep to allow events to be delivered as a batch.
//     This can help prevent running the same macro operation multiple times
//     because events came in one at a time.
//     The sleep also allows inotify to coalese similar events together.
//   - Access to underlying linux events, so finer handling of moves, etc.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ugorji/go-common/errorutil"
	"github.com/ugorji/go-common/logging"
)

var ClosedErr = errorutil.String("<watcher closed>")

// Use select, so read doesn't block
const useSelect = true

// Use non-block, so we don't block on read.
const useNonBlock = true

type WatchEvent struct {
	syscall.InotifyEvent
	Path string
	Name string
}

func (e *WatchEvent) String() string {
	s := make([]string, 0, 4)
	x := e.Mask
	if x&syscall.IN_ISDIR != 0 {
		s = append(s, "IN_ISDIR")
	}
	if x&syscall.IN_CREATE != 0 {
		s = append(s, "IN_CREATE")
	}
	if x&syscall.IN_CLOSE_WRITE != 0 {
		s = append(s, "IN_CLOSE_WRITE")
	}
	if x&syscall.IN_MOVED_TO != 0 {
		s = append(s, "IN_MOVED_TO")
	}
	if x&syscall.IN_MOVED_FROM != 0 {
		s = append(s, "IN_MOVED_FROM")
	}
	if x&syscall.IN_DELETE != 0 {
		s = append(s, "IN_DELETE")
	}
	if x&syscall.IN_DELETE_SELF != 0 {
		s = append(s, "IN_DELETE_SELF")
	}
	if x&syscall.IN_MOVE_SELF != 0 {
		s = append(s, "IN_MOVE_SELF")
	}
	return fmt.Sprintf("WatchEvent: Path: %s, Name: %s, Wd: %v, Cookie: %v, Mask: %b, %v",
		e.Path, e.Name, e.Wd, e.Cookie, e.Mask, s)
}

// Watcher implements a watch service.
// It allows user to handle events natively, but does
// management of event bus and delivery.
// User just provides a callback function.
type Watcher struct {
	sl         time.Duration
	fd         int
	closed     uint32
	wds        map[int32]string
	flags      map[string]uint32
	mu         sync.Mutex
	fn         func([]*WatchEvent)
	ev         chan []*WatchEvent
	sysbufsize int
}

// NewWatcher returns a new Watcher instance.
//   - bufsize: chan size (ie max number of batches available to be processed)
//   - sysBufSize: syscall Inotify Buf size (ie max number of inotify events in each read)
//   - sleepTime: sleep time between reads.
//     Allows system coalese events, and allows us user handle events in batches.
//   - fn: function to call for each batch of events read
func NewWatcher(bufsize, sysBufSize int, sleepTime time.Duration, fn func([]*WatchEvent),
) (w *Watcher, err error) {
	fd, err := syscall.InotifyInit()
	if err != nil {
		return
	}
	if fd == -1 {
		err = os.NewSyscallError("inotify_init", err)
		return
	}
	if useNonBlock {
		syscall.SetNonblock(fd, true)
	}
	w = &Watcher{
		fd:         fd,
		fn:         fn,
		ev:         make(chan []*WatchEvent, bufsize),
		wds:        make(map[int32]string),
		flags:      make(map[string]uint32),
		sl:         sleepTime,
		sysbufsize: sysBufSize,
	}
	go w.readEvents()
	go w.handleEvents()
	return
}

func (w *Watcher) AddAll(ignoreErr, clear, recursive bool, flags uint32, fpaths ...string) error {
	if atomic.LoadUint32(&w.closed) != 0 {
		return ClosedErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var merr errorutil.Multi
	fnErr := func(err error, message string, params ...interface{}) bool {
		if ignoreErr {
			logging.Error2(nil, err, message, params...)
			return false
		} else {
			errorutil.OnErrorf(&err, message, params...)
			merr = append(merr, err)
			return true
		}
	}
	if clear && fnErr(w.clear(), "Error clearing Watcher") {
		return merr.NonNilError()
	}
	for _, fpath := range fpaths {
		var walkDoneErr = errorutil.String("DONE")
		first := true
		walkFn := func(f string, info os.FileInfo, inerr error) error {
			if first || info.Mode().IsDir() {
				if fnErr(w.add(f, flags), "Error adding path: %s", f) {
					return walkDoneErr
				}
			}
			if first && !recursive {
				return walkDoneErr
			}
			first = false
			return nil
		}
		if err := filepath.Walk(fpath, walkFn); err == walkDoneErr {
			break
		}
	}
	return merr.NonNilError()
}

func (w *Watcher) Add(fpath string, flags uint32) error {
	if atomic.LoadUint32(&w.closed) != 0 {
		return ClosedErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.add(fpath, flags)
}

func (w *Watcher) add(fpath string, flags uint32) error {
	if flags == 0 {
		flags =
			// delete: false
			syscall.IN_CREATE |
				syscall.IN_CLOSE_WRITE |
				syscall.IN_MOVED_TO |
				// delete: true
				syscall.IN_MOVED_FROM |
				syscall.IN_DELETE |
				syscall.IN_DELETE_SELF |
				syscall.IN_MOVE_SELF
	}
	// user can add more flags by passing the syscall.IN_MASK_ADD
	wd, err := syscall.InotifyAddWatch(w.fd, fpath, flags)
	if err != nil {
		errorutil.OnErrorf(&err, "Error adding watch for path: %s", fpath)
		return err
	}
	w.wds[int32(wd)] = fpath
	w.flags[fpath] = flags
	return nil
}

func (w *Watcher) Remove(fpath string) (err error) {
	if atomic.LoadUint32(&w.closed) != 0 {
		return ClosedErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.remove(fpath)
}

func (w *Watcher) remove(fpath string) (err error) {
	for k, v := range w.wds {
		if v == fpath {
			_, err = syscall.InotifyRmWatch(w.fd, uint32(k))
			delete(w.wds, k)
			delete(w.flags, v)
			break
		}
	}
	return
}

func (w *Watcher) Clear() error {
	if atomic.LoadUint32(&w.closed) != 0 {
		return ClosedErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clear()
}

func (w *Watcher) clear() error {
	var merr errorutil.Multi
	for k, v := range w.wds {
		if _, err := syscall.InotifyRmWatch(w.fd, uint32(k)); err != nil {
			errorutil.OnErrorf(&err, "Error removing watch for path: %s", v)
			merr = append(merr, err)
		}
	}
	w.wds = make(map[int32]string)
	w.flags = make(map[string]uint32)
	return merr.NonNilError()
}

func (w *Watcher) Close() (err error) {
	// Note that, with blocking read, Close is best effort. This is because read in linux
	// does not error if the file descriptor is closed. Thus the "read" syscall may not unblock.
	//
	// To mitigate, we use select AND NonBlocking IO during the read.
	if !atomic.CompareAndSwapUint32(&w.closed, 0, 1) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clear()
	close(w.ev)
	if !(useSelect || useNonBlock) {
		logging.Error2(nil, syscall.Close(w.fd), "Error closing Watcher")
	}
	return nil
}

func (w *Watcher) readEvents() {
	if useSelect || useNonBlock {
		defer func() {
			logging.Error2(nil, syscall.Close(w.fd), "Error closing Watcher")
		}()
	}

	// inotify events come very quickly, so we can't handle them inline.
	// Instead, we grab the list of events we read and put on a queue.
	// This way, we can still work on a bunch of events at same time.
	var buf = make([]byte, syscall.SizeofInotifyEvent*w.sysbufsize)

	for {
		// always check closed right before syscalls (read/select/sleep), to minimize chance
		// of race condition where fd is closed, OS assigns to someone else, and we try to read.

		// slight sleep gives a chance to coalese similar events into one
		if w.sl != 0 {
			if atomic.LoadUint32(&w.closed) != 0 {
				return
			}
			time.Sleep(w.sl)
		}

		if useSelect {
			if atomic.LoadUint32(&w.closed) != 0 {
				return
			}
			// println(">>>>> select: Checking to read")
			fdset := new(syscall.FdSet)
			fdset.Bits[w.fd/64] |= 1 << (uint(w.fd) % 64) // FD_SET
			// fdIsSet := (fdset.Bits[w.fd/64] & (1 << (uint(w.fd) % 64))) != 0 // FD_ISSET
			// for i := range fdset.Bits { fdset.Bits[i] = 0 } // FD_ZERO
			selTimeout := syscall.NsecToTimeval(int64(1 * time.Second))
			num, err := syscall.Select(w.fd+1, fdset, nil, nil, &selTimeout)
			// if err != nil || num == 0 {
			if (fdset.Bits[w.fd/64] & (1 << (uint(w.fd) % 64))) == 0 { // FD_ISSET
				logging.Error2(nil, err, "Error during Watcher select, which returned: %d", num)
				continue
			}
			// println(">>>>> select: will read")
		}
		if atomic.LoadUint32(&w.closed) != 0 {
			return
		}
		n, err := syscall.Read(w.fd, buf[0:])
		if useNonBlock && err == syscall.EAGAIN {
			// println(">>>>> non-block: EAGAIN")
			continue
		}
		// even if there is an error, see if any events already read and process them.
		logging.Error2(nil, err, "Error during Watcher read, which returned %d bytes", n)
		if n == 0 {
			break // EOF
		}
		if n < syscall.SizeofInotifyEvent {
			continue // short read
		}
		var offset uint32
		wevs := make([]*WatchEvent, 0, n/(syscall.SizeofInotifyEvent*2))
		for offset <= uint32(n-syscall.SizeofInotifyEvent) {
			// raw.Wd, raw.Mask, raw.Cookie, raw.Len (all uint32)
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			fpath := w.wds[raw.Wd]
			// skip some events
			if raw.Mask&syscall.IN_IGNORED != 0 ||
				raw.Mask&syscall.IN_Q_OVERFLOW != 0 ||
				raw.Mask&syscall.IN_UNMOUNT != 0 ||
				fpath == "" {
				offset += syscall.SizeofInotifyEvent + raw.Len
				continue
			}

			wev := &WatchEvent{InotifyEvent: *raw, Path: fpath}
			if raw.Len != 0 {
				bs := (*[syscall.PathMax]byte)(unsafe.Pointer(&buf[offset+syscall.SizeofInotifyEvent]))
				wev.Name = strings.TrimRight(string(bs[0:raw.Len]), "\000")
			}
			wevs = append(wevs, wev)
			offset += syscall.SizeofInotifyEvent + raw.Len
		}
		select {
		case w.ev <- wevs:
		case <-time.After(10 * time.Millisecond):
			// drop if after 30 milliseconds and no one to accept
		}
	}
}

func (w *Watcher) handleEvents() {
	for x := range w.ev {
		w.fn(x)
	}
}
