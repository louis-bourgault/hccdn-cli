package cdn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadStreamsMultipartAndParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if header.Filename != "hello.txt" || string(body) != "hello" {
			t.Fatalf("unexpected multipart payload: %q %q", header.Filename, body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"up1","filename":"hello.txt","size":5,"content_type":"text/plain","url":"https://cdn.example/up1/hello.txt","created_at":"now"}`)
	}))
	defer server.Close()
	client := NewClient("secret", server.URL, time.Second, 0)
	upload, err := client.Upload(context.Background(), Payload{Filename: "hello.txt", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBufferString("hello")), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if upload.ID != "up1" || upload.Size != 5 {
		t.Fatalf("unexpected upload: %+v", upload)
	}
}

func TestUploadRetriesTransientStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, `{"error":"try later"}`, http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"id":"up1","filename":"x","size":1,"url":"https://cdn.example/x"}`)
	}))
	defer server.Close()
	client := NewClient("secret", server.URL, time.Second, 1)
	_, err := client.Upload(context.Background(), Payload{Filename: "x", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBufferString("x")), nil
	}})
	if err != nil || calls.Load() != 2 {
		t.Fatalf("retry failed: calls=%d err=%v", calls.Load(), err)
	}
}

func TestClientErrorDeleteHashAndMe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"error":"bad file"}`)
		case "/upload/gone":
			w.WriteHeader(http.StatusNoContent)
		case "/file":
			io.WriteString(w, "payload")
		case "/me":
			io.WriteString(w, `{"id":"me","storage_used":10,"storage_limit":100,"quota_tier":"verified"}`)
		}
	}))
	defer server.Close()
	client := NewClient("secret", server.URL, time.Second, 0)
	_, err := client.Upload(context.Background(), Payload{Filename: "x", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBufferString("x")), nil
	}})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Message != "bad file" {
		t.Fatalf("unexpected status error: %v", err)
	}
	if err := client.Delete(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	hash, err := client.RemoteSHA256(context.Background(), server.URL+"/file")
	if err != nil || hash != "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5" {
		t.Fatalf("unexpected remote hash %q err=%v", hash, err)
	}
	account, err := client.Me(context.Background())
	if err != nil || account.ID != "me" || account.StorageLimit != 100 {
		t.Fatalf("unexpected account %+v err=%v", account, err)
	}
}
