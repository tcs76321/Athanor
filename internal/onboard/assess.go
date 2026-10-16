package onboard

// Assessment is the M9-T2 production shape: the onboarding inventory plus a
// routing proposal built from whatever per-role qualification measurements are
// available. At runtime the daemon holds no measurements, so Recommendations
// say "no qualification data; keeping the configured model" unless the operator
// supplies a measurements file to `athanor models -qualification`. It is the
// value `athanor models` presents (and a future `GET /models` would serve).
type Assessment struct {
	Report          Report           `json:"report"`
	Recommendations []Recommendation `json:"recommendations"`
}

// Assess composes the inventory and the routing proposal. It is pure: the
// caller supplies the installed catalog, memory budget, and any measurements.
// measurements may be nil (no qualification run: Route then keeps every
// configured model and says why).
func Assess(roles []Role, families map[string]string, models []Model,
	availableBytes, reserveBytes int64, measurements []Measurement, opts RoutingOptions) Assessment {
	return Assessment{
		Report:          Inventory(roles, models, availableBytes, reserveBytes),
		Recommendations: Route(roles, families, measurements, opts),
	}
}
