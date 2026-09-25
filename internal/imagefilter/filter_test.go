package imagefilter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestUniqueChecksAndOnly403Removal(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected method %s", r.Method)
		}
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/blocked":
			w.WriteHeader(403)
		case "/missing":
			w.WriteHeader(404)
		case "/limited":
			w.WriteHeader(429)
		case "/redirect":
			http.Redirect(w, r, "/blocked", 302)
		default:
			w.WriteHeader(200)
		}
	}))
	defer s.Close()
	urls := []string{s.URL + "/ok", s.URL + "/blocked", s.URL + "/ok", s.URL + "/missing", s.URL + "/limited", s.URL + "/redirect"}
	p, err := CheckURLs(context.Background(), urls, DefaultOptions(), s.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Report.Checked != 5 || p.Report.Removed != 2 || p.Report.Failed != 0 {
		t.Fatalf("%+v", p.Report)
	}
	if counts["/ok"] != 1 || counts["/missing"] != 1 {
		t.Fatal(counts)
	}
	got := p.List(strings.Join(urls, "\n"))
	if strings.Contains(got, "blocked") || strings.Contains(got, "redirect") || strings.Count(got, "/ok") != 1 || !strings.Contains(got, "missing") || !strings.Contains(got, "limited") {
		t.Fatal(got)
	}
}

func TestHTMLRemovesBlockedAndRepeatedImagesPreservingText(t *testing.T) {
	u := "https://example.com/blocked?a=1&b=2"
	p := &Policy{blocked: map[string]bool{u: true}}
	in := `<p class='x'>Keep &amp; text</p><img src="https://example.com/blocked?a=1&amp;b=2"><img src='https://example.com/good'><img src='https://example.com/good'><picture><source srcset="https://example.com/blocked?a=1&amp;b=2 2x"><img src="https://example.com/other"></picture>`
	out := p.HTML(in)
	if strings.Contains(out, "blocked") || strings.Count(out, "https://example.com/good") != 1 || !strings.Contains(out, `<p class='x'>Keep &amp; text</p>`) || !strings.Contains(out, "other") {
		t.Fatal(out)
	}
	if urls := HTMLURLs(in); len(urls) != 5 || urls[0] != u {
		t.Fatal(urls)
	}
}

func TestErrorsPreservedAndCancellationStopsConversion(t *testing.T) {
	p, err := CheckURLs(context.Background(), []string{"not-a-url"}, DefaultOptions(), nil, nil)
	if err != nil || p.Report.Failed != 1 || p.List("not-a-url") != "not-a-url" {
		t.Fatalf("%+v %v", p, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckURLs(ctx, []string{"https://example.com/a"}, DefaultOptions(), nil, nil); err == nil {
		t.Fatal("cancellation ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type unreadBody struct{ closed bool }

func (b *unreadBody) Read([]byte) (int, error) { panic("probe must not read the image body") }
func (b *unreadBody) Close() error             { b.closed = true; return nil }
func TestProbeClosesWithoutReadingBody(t *testing.T) {
	b := &unreadBody{}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: b, Header: http.Header{}}, nil
	})}
	_, err := CheckURLs(context.Background(), []string{"https://example.com/x"}, DefaultOptions(), client, nil)
	if err != nil || !b.closed {
		t.Fatalf("closed=%v err=%v", b.closed, err)
	}
}
