package quota

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer is the offline HTTP seam: it records the one request it receives
// and returns a canned response. No test in this package opens a socket.
type fakeDoer struct {
	lastReq *http.Request
	resp    *http.Response
	err     error
}

func (d *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	d.lastReq = req
	if d.err != nil {
		return nil, d.err
	}
	return d.resp, nil
}

// neverDoer fails the test if any request is attempted.
type neverDoer struct{ t *testing.T }

func (d neverDoer) Do(req *http.Request) (*http.Response, error) {
	d.t.Fatalf("no request may leave the process here, got %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

type tokenFunc func() (string, error)

func (f tokenFunc) Token() (string, error) { return f() }

func staticToken(tok string) TokenSource {
	return tokenFunc(func() (string, error) { return tok, nil })
}
