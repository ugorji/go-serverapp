package web

import (
	"encoding/base64"
	"encoding/json"
	"time"
	"strings"
	"github.com/ugorji/go-common/errorutil"
	"net/http"
)

const FlashMessage = "FlashMessage"

var cookieValueSanitizer = strings.NewReplacer("\n", " ", "\r", " ", ";", " ")

type HandlerMessage struct {
	Text  string
	Error bool
}

// Returns a new cookie.
// Note: NewCookie sets the domain from the Host if possible (as an appropriate .XYZ.tld)
// The cookie domain is only set if the app Host is not localhost, and is a real Hostname.
func NewCookie(host, name, value string, ttlsec int, encode bool) *http.Cookie {
	if value == "" || ttlsec < 0 {
		ttlsec = 0
	}
	ck := new(http.Cookie)
	if host != "" {
		if colon := strings.Index(host, ":"); colon != -1 {
			host = host[:colon]
		}
	}
	if host != "" {
		if dot := strings.Index(host, "."); dot != -1 {
			ck.Domain = "." + host
		}
	}
	ck.Name, ck.Value, ck.Path, ck.MaxAge = name, value, "/", ttlsec
	ck.Expires = time.Now().Add(time.Duration(int64(ttlsec))).UTC()
	if encode {
		ck.Value = base64.URLEncoding.EncodeToString([]byte(value))
	}
	return ck
}

func AddHandlerMessages(r *http.Request, w http.ResponseWriter,
	ckName string, messages ...HandlerMessage,
) (err error) {
	defer errorutil.OnError(&err)
	// find the cookie
	// if not there, add cookie
	// if there before, update cookie that was set
	// unfortunately, function to do most of this in net/http/cookie.go is unexported (so reproduce here)

	var (
		ckval string
		newck bool = true
		indx  int
		line  string
		lines = w.Header()["Set-Cookie"]
	)
	for indx, line = range lines {
		parts := strings.Split(strings.TrimSpace(line), ";")
		if len(parts) == 1 && parts[0] == "" {
			continue
		}
		parts[0] = strings.TrimSpace(parts[0])
		j := strings.Index(parts[0], "=")
		if j < 0 {
			continue
		}
		name, value := parts[0][:j], parts[0][j+1:]
		if name == ckName {
			newck = false
			ckval = value
			break
		}
	}

	msgs := []HandlerMessage{}
	if !newck {
		if err = json.Unmarshal([]byte(ckval), &msgs); err != nil {
			return
		}
	}
	msgs = append(msgs, messages...)
	if len(msgs) == 0 {
		return
	}
	bs, err := json.Marshal(msgs)
	if err != nil {
		return
	}
	ckval = cookieValueSanitizer.Replace(string(bs))
	if newck {
		http.SetCookie(w, NewCookie(r.Host, ckName, ckval, 60, false))
	} else {
		icolon := strings.Index(lines[indx], ";")
		lines[indx] = ckName + "=" + ckval + lines[indx][icolon:]
	}
	return
}
