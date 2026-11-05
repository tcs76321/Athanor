package operator

// BaseCap is the existing power/config concurrency source.
type BaseCap interface {
	MaxConcurrentJobs() int
}

// Ceiling composes the operator mode with the power/config ceiling. Deep and
// Full use the operator ceiling; Lite is one; Stopped is zero (the engine's
// gate is authoritative).
type Ceiling struct {
	Controller *Controller
	Base       BaseCap
}

// MaxConcurrentJobs implements engine.ConcurrencyCap.
func (c Ceiling) MaxConcurrentJobs() int {
	if c.Controller != nil {
		n := c.Controller.Concurrency()
		if n == 0 {
			return 0
		}
		if c.Base != nil {
			if base := c.Base.MaxConcurrentJobs(); base > 0 && n > base {
				n = base
			}
		}
		return n
	}
	if c.Base != nil {
		if base := c.Base.MaxConcurrentJobs(); base > 0 {
			return base
		}
	}
	return 1
}
