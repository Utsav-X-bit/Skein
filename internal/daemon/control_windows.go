//go:build windows

package daemon

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// serveControl serves the desktop API over the Windows named pipe. Each
// connection carries one HTTP request and one response; a full http.Server
// would start a background read that races the response write on a pipe.
func (d *Daemon) serveControl(ctx context.Context) {
	cfg := d.config()
	if cfg.API.Pipe == "" {
		log.Printf("skein: warning: control pipe disabled (api.pipe is empty)")
		return
	}
	log.Printf("skein: control pipe %q", cfg.API.Pipe)
	ln, err := api.NewPipeListener(ctx, cfg.API.Pipe, controlAccessFile, d.api.ConnectionCap())
	if err != nil {
		log.Printf("skein: error: control pipe %v: %v", cfg.API.Pipe, err)
		return
	}
	log.Printf("skein: control pipe listening at \\\\.\\pipe\\%s", cfg.API.Pipe)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("skein: error: control pipe accept: %v", err)
			}
			return
		}
		go d.handlePipeConn(ctx, conn)
	}
}

func (d *Daemon) handlePipeConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		log.Printf("skein: pipe request: %v", err)
		return
	}
	req = req.WithContext(api.CallerContext(ctx, conn))
	rec := httptest.NewRecorder()
	d.api.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Close = true
	if err := resp.Write(conn); err != nil {
		log.Printf("skein: pipe response: %v", err)
	}
}
