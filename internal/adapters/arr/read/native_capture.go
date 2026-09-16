package read

import (
	"io"
	"net/http"
	"sync"
)

const maxNativeCompatibilityCapture = 1 << 20

// nativeResponseCapture retains only the bounded response bytes needed to
// decide whether a strict standalone-client failure is an explicitly
// supported legacy response shape. It is never exposed in domain evidence or
// error text. The root adapter's ordinary HTTP client remains untouched.
type nativeResponseCapture struct {
	base     http.RoundTripper
	maxBytes int64

	mu   sync.Mutex
	last map[string]nativeCapturedResponse
}

type nativeCapturedResponse struct {
	body     []byte
	complete bool
}

func newNativeResponseCapture(base http.RoundTripper, maxBytes int64) *nativeResponseCapture {
	if base == nil {
		base = http.DefaultTransport
	}
	if maxBytes <= 0 || maxBytes > maxNativeCompatibilityCapture {
		maxBytes = maxNativeCompatibilityCapture
	}
	return &nativeResponseCapture{base: base, maxBytes: maxBytes, last: make(map[string]nativeCapturedResponse)}
}

func (capture *nativeResponseCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := capture.base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	response.Body = &nativeCaptureBody{
		ReadCloser: response.Body,
		capture:    capture,
		key:        request.URL.Path,
		body:       make([]byte, 0, minInt64(capture.maxBytes, 4096)),
	}
	return response, nil
}

func (capture *nativeResponseCapture) latest(path string) ([]byte, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	response, ok := capture.last[path]
	if !ok || !response.complete {
		return nil, false
	}
	return append([]byte(nil), response.body...), true
}

type nativeCaptureBody struct {
	io.ReadCloser
	capture *nativeResponseCapture
	key     string
	body    []byte
	tooLong bool
	stored  bool
}

func (body *nativeCaptureBody) Read(destination []byte) (int, error) {
	read, err := body.ReadCloser.Read(destination)
	if read > 0 && !body.tooLong {
		remaining := body.capture.maxBytes - int64(len(body.body))
		if int64(read) > remaining {
			if remaining > 0 {
				body.body = append(body.body, destination[:int(remaining)]...)
			}
			body.tooLong = true
		} else {
			body.body = append(body.body, destination[:read]...)
		}
	}
	if err != nil {
		body.store(err == io.EOF && !body.tooLong)
	}
	return read, err
}

func (body *nativeCaptureBody) Close() error {
	err := body.ReadCloser.Close()
	body.store(false)
	return err
}

func (body *nativeCaptureBody) store(complete bool) {
	if body.stored {
		return
	}
	body.stored = true
	body.capture.mu.Lock()
	body.capture.last[body.key] = nativeCapturedResponse{body: append([]byte(nil), body.body...), complete: complete}
	body.capture.mu.Unlock()
}

func minInt64(left, right int64) int {
	if left < right {
		return int(left)
	}
	return int(right)
}
