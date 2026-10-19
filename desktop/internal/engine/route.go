package engine

import (
	"context"
	"encoding/json"
	"time"
)

// Uploads interrupted by a pause resume where they stopped. If the upload
// route changed while paused (direct LAN uploads turned on or off), the
// interrupted sessions are wiped on the server instead and the files start
// again over the new route.

// pausedUploadsKey holds the sessions in flight when sync was paused.
const pausedUploadsKey = "paused_uploads"

type pausedUploads struct {
	Route   string   `json:"route"` // direct address in use, "" for the public URL
	Uploads []string `json:"uploads"`
}

// routeLocked returns the direct address upload traffic goes to, "" for the
// public URL. Callers hold e.mu.
func (e *Engine) routeLocked() string {
	if e.client == nil {
		return ""
	}
	if d := e.client.Direct(); d != nil {
		return d.String()
	}
	return ""
}

// recordPausedUploads remembers the sessions of the transfers being
// stopped. Callers hold e.mu.
func (e *Engine) recordPausedUploads() {
	rec := pausedUploads{Route: e.routeLocked()}
	if b := e.st.Get(pausedUploadsKey); b != "" {
		var prev pausedUploads
		if json.Unmarshal([]byte(b), &prev) == nil && prev.Route == rec.Route {
			rec.Uploads = prev.Uploads // paused again before resuming
		}
	}
	for _, at := range e.active {
		if id, _ := at.uploadID.Load().(string); id != "" {
			rec.Uploads = append(rec.Uploads, id)
		}
	}
	b, _ := json.Marshal(rec)
	e.st.Set(pausedUploadsKey, string(b))
}

// restartIfRouteChanged handles a resume: when the route differs from the
// one the paused sessions used, it asks the server to wipe those sessions
// (in the background) and unpauses afterwards, so their files start over on
// the new route. It reports whether it took over the unpause.
func (e *Engine) restartIfRouteChanged() bool {
	raw := e.st.Get(pausedUploadsKey)
	if raw == "" {
		return false
	}
	var rec pausedUploads
	_ = json.Unmarshal([]byte(raw), &rec)
	e.mu.Lock()
	c, route := e.client, e.routeLocked()
	e.mu.Unlock()
	if c == nil {
		return false // keep the record until connected
	}
	e.st.Set(pausedUploadsKey, "")
	if route == rec.Route || len(rec.Uploads) == 0 {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
		defer cancel()
		wiped := 0
		for _, id := range rec.Uploads {
			if err := c.RestartUpload(ctx, id); err != nil {
				e.log.Warn("could not wipe an interrupted upload; the server expires it", "upload", id, "err", err)
				continue
			}
			wiped++
		}
		e.log.Info("upload route changed while paused; interrupted uploads start over", "wiped", wiped, "route", route)
		e.SetPaused(false)
	}()
	return true
}
