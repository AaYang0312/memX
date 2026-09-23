package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeDrainsInflightOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	})}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, srv, time.Second) }()
	response := make(chan error, 1)
	client := &http.Client{Timeout: 2 * time.Second}
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String() + "/livez")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = ErrShutdown
			}
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	close(release)
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight request not completed")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestServeRejectsMissingInputs(t *testing.T) {
	if err := Serve(context.Background(), nil, nil, time.Second); err == nil {
		t.Fatal("missing listener/server accepted")
	}
}
