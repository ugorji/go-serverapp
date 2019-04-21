package web

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ugorji/go-common/logging"
)

// AccessLogger handles access logging, including opening/closing
// files and buffering output.
// It also acts as a pipe, so it can participate in the execution
// of a request.
type AccessLogger struct {
	name   string
	file   *os.File
	mu     sync.Mutex
	bufw   *bufio.Writer
	closed uint32
}

func NewAccessLogger(filename string) *AccessLogger {
	return &AccessLogger{name: filename}
}

func (s *AccessLogger) Reopen() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopen()
}

func (s *AccessLogger) Reset(name string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.name != name {
		s.name = name
		err = s.reopen()
	}
	return
}

func (s *AccessLogger) reopen() (err error) {
	// TODO: Only call if not closed
	s.flush()
	closeFile(s.file, s.name)
	s.file, err = os.OpenFile(s.name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		if s.bufw != nil {
			s.bufw.Reset(ioutil.Discard)
		}
		return
	}
	if s.bufw == nil {
		s.bufw = bufio.NewWriterSize(s.file, 4<<10) // 4K
		go s.intervalFlush(1 * time.Second)
	} else {
		s.bufw.Reset(s.file)
	}
	return
}

func (s *AccessLogger) flush() {
	if s.bufw == nil || s.file == nil {
		return
	}
	logging.Error2(nil, s.bufw.Flush(), "Error flushing access log file to disk")
}

func (s *AccessLogger) Close() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flush()
	s.bufw = nil
	closeFile(s.file, s.name)
	s.file = nil
	s.closed = 1
	return
}

func (s *AccessLogger) intervalFlush(t time.Duration) {
	for {
		s.mu.Lock()
		s.flush()
		s.mu.Unlock()
		if atomic.LoadUint32(&s.closed) == 1 {
			break
		}
		time.Sleep(t)
	}
}

// log access in the combined log format.
func (s *AccessLogger) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	f.Next(w, r)
	// w.Flush()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return
	}

	// basic auth, and local computer ident not supported
	_, err := fmt.Fprintf(s.bufw,
		"%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"\n",
		r.RemoteAddr,
		time.Now().Format(time.RFC3339),
		r.Method, r.RequestURI, r.Proto,
		w.ResponseCode(),
		w.NumBytesWritten(),
		r.Referer(),
		r.UserAgent(),
	)
	logging.Error2(nil, err, "Error logging access")
	return

}
