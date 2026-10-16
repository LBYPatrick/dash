package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// downloadStream claims a job exactly once. The request owns the worker and
// its lifetime: disconnect, stalled network, cancellation, or ignition stops it.
func (e *ExportManager) downloadStream(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed, errors.New("downloads require GET"))
		return
	}
	e.mu.Lock()
	job := e.jobs[id]
	if job == nil || !job.Streaming || job.Status != "queued" || job.ctx.Err() != nil || e.index.state() != "parked" {
		e.mu.Unlock()
		fail(w, http.StatusConflict, errors.New("download unavailable; start a new export while parked"))
		return
	}
	job.Status = "running"
	job.expires.Stop()
	e.mu.Unlock()
	defer job.cancel()

	ctx, cancel := context.WithCancel(job.ctx)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	controller := http.NewResponseController(w)
	interrupted := make(chan struct{})
	interrupt := context.AfterFunc(ctx, func() {
		defer close(interrupted)
		_ = controller.SetWriteDeadline(time.Now())
	})
	defer func() {
		// Do not let a late cancellation callback change the deadline of the
		// next request on this keep-alive connection after this handler returns.
		if !interrupt() {
			<-interrupted
		}
		_ = controller.SetWriteDeadline(time.Time{})
	}()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.Name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", map[string]string{"zip": "application/zip", "raw": "application/zip", "mp4": "video/mp4", "mcap": "application/octet-stream"}[job.Format])
	// Announce the download before slow conversion begins. No disk-sized Blob
	// or output file is buffered on either the frontend or device.
	_ = controller.SetWriteDeadline(time.Now().Add(ExportWriteTimeout))
	if err := ctx.Err(); err != nil {
		e.update(id, "canceled", "Download canceled", 0)
		return
	}
	if err := controller.Flush(); err != nil {
		e.update(id, "failed", "Download connection closed", 0)
		job.cancel()
		return
	}
	target := &boundedWriter{ctx: ctx, writer: &exportResponseWriter{ctx: ctx, response: w, controller: controller, manager: e, id: id}}
	if err := e.run(ctx, job, job.request, job.assets, target); err != nil {
		// A truncated ZIP/MP4/MCAP must be reported as an interrupted download,
		// never a successful file containing a JSON error or an incomplete footer.
		panic(http.ErrAbortHandler)
	}
	if err := controller.Flush(); err != nil {
		e.update(id, "failed", "Download connection closed", 0)
		panic(http.ErrAbortHandler)
	}
}

type exportResponseWriter struct {
	ctx        context.Context
	response   http.ResponseWriter
	controller *http.ResponseController
	manager    *ExportManager
	id         string
}

func (w *exportResponseWriter) Write(p []byte) (int, error) {
	_ = w.controller.SetWriteDeadline(time.Now().Add(ExportWriteTimeout))
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.response.Write(p)
	w.manager.mu.Lock()
	w.manager.jobs[w.id].Bytes += int64(n)
	w.manager.mu.Unlock()
	return n, err
}
