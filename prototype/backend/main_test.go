package main

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRewriteProxyLocation_RelativeUpstreamPath(t *testing.T) {
	header := http.Header{}
	header.Set("Location", "/DL1TEST_1/index.m3u8?cookieCheck=1")
	targetURL, err := url.Parse("http://mediamtx:8888")
	if err != nil {
		t.Fatal(err)
	}

	rewriteProxyLocation(header, targetURL, "/live")

	if got, want := header.Get("Location"), "/live/DL1TEST_1/index.m3u8?cookieCheck=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRewriteProxyLocation_AlreadyPrefixed(t *testing.T) {
	header := http.Header{}
	header.Set("Location", "/live/DL1TEST_1/index.m3u8?cookieCheck=1")
	targetURL, err := url.Parse("http://mediamtx:8888")
	if err != nil {
		t.Fatal(err)
	}

	rewriteProxyLocation(header, targetURL, "/live")

	if got, want := header.Get("Location"), "/live/DL1TEST_1/index.m3u8?cookieCheck=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRewriteProxyLocation_AbsoluteUpstreamURL(t *testing.T) {
	header := http.Header{}
	header.Set("Location", "http://mediamtx:8888/DL1TEST_1/index.m3u8?cookieCheck=1")
	targetURL, err := url.Parse("http://mediamtx:8888")
	if err != nil {
		t.Fatal(err)
	}

	rewriteProxyLocation(header, targetURL, "/live")

	if got, want := header.Get("Location"), "/live/DL1TEST_1/index.m3u8?cookieCheck=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

