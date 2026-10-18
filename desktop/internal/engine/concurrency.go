package engine

import "github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"

// The adaptive concurrency controller now lives in internal/transfer; these
// aliases keep the engine's existing names working.

// Controller is transfer.Controller.
type Controller = transfer.Controller

// NewController returns a new adaptive concurrency controller.
func NewController(maxLimit int) *Controller { return transfer.NewController(maxLimit) }

const absoluteMax = transfer.AbsoluteMax
