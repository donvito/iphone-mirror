package airplay

import (
	"bufio"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

type request struct {
	Method   string
	URL      string
	Protocol string
	Header   textproto.MIMEHeader
	Body     []byte
}

type response struct {
	Status     int
	Reason     string
	headers    [][2]string
	Body       []byte
	Disconnect bool
}

func newResponse() *response {
	return &response{Status: 200, Reason: "OK"}
}

func (r *response) setStatus(code int, reason string) {
	r.Status, r.Reason = code, reason
}

// addHeader keeps the exact header spelling (e.g. "CSeq"), which some AirPlay clients care about.
func (r *response) addHeader(k, v string) {
	r.headers = append(r.headers, [2]string{k, v})
}

func readRequest(br *bufio.Reader) (*request, error) {
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine()
	for err == nil && line == "" { // tolerate stray CRLFs between requests
		line, err = tp.ReadLine()
	}
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed request line %q", line)
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil && err != io.EOF {
		return nil, err
	}
	req := &request{Method: parts[0], URL: parts[1], Protocol: parts[2], Header: hdr}
	if cl := hdr.Get("Content-Length"); cl != "" {
		n, err := strconv.Atoi(cl)
		if err != nil || n < 0 || n > 16<<20 {
			return nil, fmt.Errorf("bad content-length %q", cl)
		}
		req.Body = make([]byte, n)
		if _, err := io.ReadFull(br, req.Body); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func writeResponse(w *bufio.Writer, protocol string, res *response) error {
	fmt.Fprintf(w, "%s %d %s\r\n", protocol, res.Status, res.Reason)
	for _, h := range res.headers {
		fmt.Fprintf(w, "%s: %s\r\n", h[0], h[1])
	}
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(res.Body))
	w.Write(res.Body)
	return w.Flush()
}
