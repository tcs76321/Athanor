// Package composition owns Athanor's model repertoire and the measured
// selection over it. A model is not a "persona": it is a measured capability
// profile. Selection is scoped (user, computer, project, job) and seeded by
// qualification, then updated by grounded outcomes (ADR-0104).
package composition

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Capability is the kind of work a task needs.
type Capability string

const (
	CapabilityCode     Capability = "code"
	CapabilityData     Capability = "data"
	CapabilityDocument Capability = "document"
	CapabilityText     Capability = "text"
	CapabilityMedia    Capability = "media"
	CapabilityJudgment Capability = "judgment"
	CapabilityGeneral  Capability = "general"
)

// Stance is what a call is doing, independent of the model.
type Stance string

const (
	StancePlan       Stance = "plan"
	StanceGenerate   Stance = "generate"
	StanceCritique   Stance = "critique"
	StanceVerify     Stance = "verify"
	StanceSynthesize Stance = "synthesize"
	StanceExplore    Stance = "explore"
)

// Score is a bounded success estimate. Laplace smoothing keeps a single
// result from dominating the ranking.
type Score struct {
	Successes int       `json:"successes"`
	Trials    int       `json:"trials"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Value returns the smoothed success rate (0..1).
func (s Score) Value() float64 {
	return float64(s.Successes+1) / float64(s.Trials+2)
}

// Context sizing. MinUsefulContext is the smallest window still worth casting
// for general work; code phases apply their own larger floor. The safety margin
// leaves room for compute buffers and allocator fragmentation.
const (
	MinUsefulContext   = 8192
	ContextSafetyBytes = int64(1536 << 20)
)

// MaxContextFor is the largest window the weights, KV cache, and fit budget
// allow. It returns 0 when the inputs cannot support a useful window.
func MaxContextFor(weights, kvBytesPerToken int64, native int, fitLimit int64) int {
	if weights <= 0 || kvBytesPerToken <= 0 || fitLimit <= 0 {
		return 0
	}
	available := fitLimit - weights - ContextSafetyBytes
	if available <= 0 {
		return 0
	}
	max := int(available / kvBytesPerToken)
	max = (max / 2048) * 2048
	if native > 0 && max > native {
		max = native
	}
	if max < 0 {
		max = 0
	}
	return max
}

// Profile is one model measured on this computer.
type Profile struct {
	Name            string           `json:"name"`
	Family          string           `json:"family,omitempty"`
	SizeBytes       int64            `json:"size_bytes,omitempty"`
	FileBytes       int64            `json:"file_bytes,omitempty"`
	ResidentBytes   int64            `json:"resident_bytes,omitempty"`
	NativeContext   int              `json:"native_context,omitempty"`
	KVBytesPerToken int64            `json:"kv_bytes_per_token,omitempty"`
	MaxContext      int              `json:"max_context,omitempty"`
	ContextTarget   int              `json:"context_target,omitempty"`
	Temperature     float64          `json:"temperature,omitempty"`
	Scores          map[string]Score `json:"scores,omitempty"`
	Scoped          map[string]Score `json:"scoped,omitempty"`
	QualifiedAt     time.Time        `json:"qualified_at,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at,omitempty"`
	// TooLarge marks a model whose measured or on-disk size exceeds this
	// machine's inference budget. It stays visible in the pool but is never
	// cast.
	TooLarge bool `json:"too_large,omitempty"`
}

// Machine is the measured host capability profile. It conditions every cast:
// selection is predicted performance on this computer, not a global rank.
type Machine struct {
	HostBytes     int64     `json:"host_bytes,omitempty"`
	RuntimeBytes  int64     `json:"runtime_bytes,omitempty"`
	FreeDiskBytes int64     `json:"free_disk_bytes,omitempty"`
	CPUs          int       `json:"cpus,omitempty"`
	Score         int       `json:"score,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// ScoreMachine combines host RAM, CPU, and free disk into a 0–100 capability
// score. It is intentionally simple and visible, not a hidden heuristic.
func ScoreMachine(hostBytes, freeDiskBytes int64, cpus int) int {
	if hostBytes <= 0 && cpus <= 0 && freeDiskBytes <= 0 {
		return 0
	}
	hostGiB := float64(hostBytes) / (1 << 30)
	memory := 20 + hostGiB*1.9
	if memory > 100 {
		memory = 100
	}
	cpu := float64(cpus) * 8
	if cpu > 100 {
		cpu = 100
	}
	disk := float64(freeDiskBytes) / (1 << 30) * 2
	if disk > 100 {
		disk = 100
	}
	if memory < 0 {
		memory = 0
	}
	if cpu < 0 {
		cpu = 0
	}
	if disk < 0 {
		disk = 0
	}
	return int(0.55*memory + 0.30*cpu + 0.15*disk)
}

// SetMachine records the host profile.
func (p *Pool) SetMachine(m Machine) {
	m.Score = ScoreMachine(m.HostBytes, m.FreeDiskBytes, m.CPUs)
	m.UpdatedAt = time.Now().UTC()
	p.mu.Lock()
	p.Machine = m
	p.mu.Unlock()
}

// MachineProfile returns the recorded machine profile.
func (p *Pool) MachineProfile() Machine {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Machine
}

// Pool is the user's model repertoire plus its measured profiles. It is
// machine-local: results do not transfer between computers.
type Pool struct {
	mu     sync.RWMutex
	store  *store.Store
	key    string
	Models map[string]*Profile `json:"models"`
	// Excluded remembers models the user removed so a later catalog sync
	// does not silently re-add them.
	Excluded map[string]bool `json:"excluded,omitempty"`
	// FitLimitBytes is the host inference budget for this machine. Zero means
	// unknown; models are then not filtered by size.
	FitLimitBytes int64
	// Machine is the measured host profile shown to the operator and used to
	// condition casts.
	Machine Machine `json:"machine,omitempty"`
	// Templates records grounded outcomes for deliberation shapes. The first
	// learned choice is solo vs panel; the repertoire is never pruned.
	Templates map[string]Score `json:"templates,omitempty"`
	// last records the models used by a job so a terminal outcome can update
	// the right profiles. It is process-local and intentionally not persisted.
	last map[string]cast
}

type cast struct {
	models     []string
	capability string
	scope      string
}

// PoolKey is the system_state key holding the pool.
const PoolKey = "models:pool"

// New returns an empty pool. Call Load to read persisted state.
func New(st *store.Store) *Pool {
	return &Pool{
		store: st, key: PoolKey,
		Models: map[string]*Profile{}, Excluded: map[string]bool{}, last: map[string]cast{},
		Templates: map[string]Score{},
	}
}

// Load reads the persisted pool. A missing key is an empty pool; corrupt
// state is an error rather than silently discarding measurements.
func (p *Pool) Load(ctx context.Context) error {
	if p.store == nil {
		return nil
	}
	var raw string
	err := p.store.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, p.key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("composition: reading saved model pool: %w", err)
	}
	var saved struct {
		Models    map[string]*Profile `json:"models"`
		Excluded  map[string]bool     `json:"excluded"`
		Machine   Machine             `json:"machine"`
		Templates map[string]Score    `json:"templates"`
	}
	if uerr := json.Unmarshal([]byte(raw), &saved); uerr != nil {
		return fmt.Errorf("composition: parsing saved model pool: %w", uerr)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if saved.Models != nil {
		p.Models = saved.Models
	}
	p.Machine = saved.Machine
	if saved.Templates != nil {
		p.Templates = saved.Templates
	}
	if p.Templates == nil {
		p.Templates = map[string]Score{}
	}
	p.Excluded = saved.Excluded
	if p.Excluded == nil {
		p.Excluded = map[string]bool{}
	}
	for _, prof := range p.Models {
		if prof.Scores == nil {
			prof.Scores = map[string]Score{}
		}
		if prof.Scoped == nil {
			prof.Scoped = map[string]Score{}
		}
	}
	return nil
}

// Save persists the pool.
func (p *Pool) Save(ctx context.Context) error {
	if p.store == nil {
		return nil
	}
	p.mu.RLock()
	raw, err := json.Marshal(struct {
		Models    map[string]*Profile `json:"models"`
		Excluded  map[string]bool     `json:"excluded,omitempty"`
		Machine   Machine             `json:"machine,omitempty"`
		Templates map[string]Score    `json:"templates,omitempty"`
	}{Models: p.Models, Excluded: p.Excluded, Machine: p.Machine, Templates: p.Templates})
	p.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("composition: encoding model pool: %w", err)
	}
	_, err = p.store.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, p.key, string(raw))
	if err != nil {
		return fmt.Errorf("composition: saving model pool: %w", err)
	}
	return nil
}

// ModelInfo is the catalog data the UI or audit knows before qualification.
type ModelInfo struct {
	Name            string
	Family          string
	SizeBytes       int64
	FileBytes       int64
	ResidentBytes   int64
	NativeContext   int
	KVBytesPerToken int64
	MaxContext      int
	ContextTarget   int
}

// Upsert adds or updates a model without discarding its measurements.
func (p *Pool) Upsert(info ModelInfo) {
	p.mu.Lock()
	prof, ok := p.Models[info.Name]
	if !ok {
		prof = &Profile{Name: info.Name, Scores: map[string]Score{}, Scoped: map[string]Score{}}
		p.Models[info.Name] = prof
	}
	if info.Family != "" {
		prof.Family = info.Family
	}
	if info.SizeBytes > 0 {
		prof.SizeBytes = info.SizeBytes
	}
	if info.FileBytes > 0 {
		prof.FileBytes = info.FileBytes
	}
	if info.ResidentBytes > 0 {
		prof.ResidentBytes = info.ResidentBytes
	}
	if info.NativeContext > 0 {
		prof.NativeContext = info.NativeContext
	}
	if info.KVBytesPerToken > 0 {
		prof.KVBytesPerToken = info.KVBytesPerToken
	}
	if info.MaxContext > 0 {
		prof.MaxContext = info.MaxContext
	}
	if info.ContextTarget > 0 {
		prof.ContextTarget = info.ContextTarget
	}
	prof.UpdatedAt = time.Now().UTC()
	delete(p.Excluded, info.Name)
	p.recomputeContextLocked(prof)
	p.mu.Unlock()
}

// Remove drops a model and every measurement attached to it.
func (p *Pool) Remove(name string) {
	p.mu.Lock()
	delete(p.Models, name)
	p.mu.Unlock()
}

// Exclude removes a model and remembers the user's choice, so a catalog sync
// does not silently re-add it.
func (p *Pool) Exclude(name string) {
	p.mu.Lock()
	delete(p.Models, name)
	p.Excluded[name] = true
	p.mu.Unlock()
}

// Sync reconciles the pool with the installed catalog. New installed models
// join unless the user excluded them; models no longer installed leave.
func (p *Pool) Sync(infos []ModelInfo) int {
	added, _ := p.SyncChanged(infos)
	return added
}

// SyncChanged is Sync with a change flag. The UI calls it on every render of
// the model pages; saving the pool on every render wrote the same system_state
// row every couple of seconds. The flag is true when a model was added or
// removed, or when any retained metadata actually changed.
func (p *Pool) SyncChanged(infos []ModelInfo) (added int, changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	present := make(map[string]bool, len(infos))
	for _, info := range infos {
		if info.Name == "" {
			continue
		}
		present[info.Name] = true
		prof, ok := p.Models[info.Name]
		profileChanged := !ok
		if !ok {
			if p.Excluded[info.Name] {
				continue
			}
			prof = &Profile{Name: info.Name, Scores: map[string]Score{}, Scoped: map[string]Score{}}
			p.Models[info.Name] = prof
			added++
		}
		if info.Family != "" && prof.Family != info.Family {
			prof.Family = info.Family
			profileChanged = true
		}
		if info.SizeBytes > 0 && prof.SizeBytes != info.SizeBytes {
			prof.SizeBytes = info.SizeBytes
			profileChanged = true
		}
		if info.FileBytes > 0 && prof.FileBytes != info.FileBytes {
			prof.FileBytes = info.FileBytes
			profileChanged = true
		}
		if info.ResidentBytes > 0 && prof.ResidentBytes != info.ResidentBytes {
			prof.ResidentBytes = info.ResidentBytes
			profileChanged = true
		}
		if info.NativeContext > 0 && prof.NativeContext != info.NativeContext {
			prof.NativeContext = info.NativeContext
			profileChanged = true
		}
		if info.KVBytesPerToken > 0 && prof.KVBytesPerToken != info.KVBytesPerToken {
			prof.KVBytesPerToken = info.KVBytesPerToken
			profileChanged = true
		}
		if info.MaxContext > 0 && prof.MaxContext != info.MaxContext {
			prof.MaxContext = info.MaxContext
			profileChanged = true
		}
		if info.ContextTarget > 0 && prof.ContextTarget != info.ContextTarget {
			prof.ContextTarget = info.ContextTarget
			profileChanged = true
		}
		beforeMax, beforeTarget, beforeTooLarge := prof.MaxContext, prof.ContextTarget, prof.TooLarge
		p.recomputeContextLocked(prof)
		if prof.MaxContext != beforeMax || prof.ContextTarget != beforeTarget || prof.TooLarge != beforeTooLarge {
			profileChanged = true
		}
		if profileChanged {
			prof.UpdatedAt = time.Now().UTC()
			changed = true
		}
	}
	for name := range p.Models {
		if !present[name] {
			delete(p.Models, name)
			changed = true
		}
	}
	return added, changed
}

// recomputeContextLocked derives the maximum window from the budget and clamps
// the working target to it. A model is only "too large" when even the minimum
// useful window does not fit; otherwise it is castable at MaxContext or below.
func (p *Pool) recomputeContextLocked(prof *Profile) {
	weights := prof.FileBytes
	if weights <= 0 {
		weights = prof.SizeBytes
	}
	if prof.KVBytesPerToken > 0 {
		prof.MaxContext = MaxContextFor(weights, prof.KVBytesPerToken, prof.NativeContext, p.FitLimitBytes)
		prof.TooLarge = prof.MaxContext < MinUsefulContext
		if prof.ContextTarget > prof.MaxContext {
			prof.ContextTarget = prof.MaxContext
		}
		return
	}
	// No KV metadata/measurement: keep the old weight-only guard so a model
	// that cannot even hold its weights is still excluded.
	if p.FitLimitBytes <= 0 {
		prof.TooLarge = false
		return
	}
	size := prof.ResidentBytes
	if size == 0 {
		size = weights
	}
	prof.TooLarge = size > p.FitLimitBytes
}

// SetMeasuredResident records a qualification-time resident size and derives
// KV bytes/token from the context the screen used. It is a fallback for models
// whose architecture metadata does not expose KV-head counts (notably MLX).
func (p *Pool) SetMeasuredResident(name string, residentBytes, contextUsed int64) {
	if residentBytes <= 0 || contextUsed <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, ok := p.Models[name]
	if !ok {
		return
	}
	weights := prof.FileBytes
	if weights <= 0 {
		weights = prof.SizeBytes
	}
	prof.ResidentBytes = residentBytes
	if weights > 0 && residentBytes > weights {
		kv := (residentBytes - weights) / contextUsed
		if kv > 0 {
			prof.KVBytesPerToken = kv
		}
	}
	p.recomputeContextLocked(prof)
}

// Has reports whether a model is in the pool.
func (p *Pool) Has(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.Models[name]
	return ok
}

// Count returns the pool size.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.Models)
}

// Names returns the pool in deterministic order.
func (p *Pool) Names() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.Models))
	for name := range p.Models {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// EligibleNames returns the models that fit this machine, in deterministic
// order. Qualification and casting use this set.
func (p *Pool) EligibleNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.Models))
	for name, prof := range p.Models {
		if prof.TooLarge {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Profiles returns a deterministic snapshot.
func (p *Pool) Profiles() []Profile {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Profile, 0, len(p.Models))
	for _, prof := range p.Models {
		cp := *prof
		cp.Scores = copyScores(prof.Scores)
		cp.Scoped = copyScores(prof.Scoped)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Profile returns one profile.
func (p *Pool) Profile(name string) (Profile, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	prof, ok := p.Models[name]
	if !ok {
		return Profile{}, false
	}
	cp := *prof
	cp.Scores = copyScores(prof.Scores)
	cp.Scoped = copyScores(prof.Scoped)
	return cp, true
}

func copyScores(in map[string]Score) map[string]Score {
	out := make(map[string]Score, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Record adds one grounded observation for a model. key is a capability
// (e.g. "code") or a stance key prefixed with "stance:".
func (p *Pool) Record(name, key string, success bool) {
	if key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, ok := p.Models[name]
	if !ok {
		return
	}
	if prof.Scores == nil {
		prof.Scores = map[string]Score{}
	}
	s := prof.Scores[key]
	s.Trials++
	if success {
		s.Successes++
	}
	s.UpdatedAt = time.Now().UTC()
	prof.Scores[key] = s
	prof.UpdatedAt = time.Now().UTC()
}

// RecordScoped adds a scoped observation (for example a project-specific
// result). It is combined with the global score by partial pooling.
func (p *Pool) RecordScoped(name, scope, key string, success bool) {
	if scope == "" || key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, ok := p.Models[name]
	if !ok {
		return
	}
	if prof.Scoped == nil {
		prof.Scoped = map[string]Score{}
	}
	k := scope + "/" + key
	s := prof.Scoped[k]
	s.Trials++
	if success {
		s.Successes++
	}
	s.UpdatedAt = time.Now().UTC()
	prof.Scoped[k] = s
	prof.UpdatedAt = time.Now().UTC()
}

// SetMeasured replaces the qualification counts for a capability so a
// completed run is idempotent rather than cumulative.
func (p *Pool) SetMeasured(name, key string, successes, trials int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, ok := p.Models[name]
	if !ok {
		return
	}
	if prof.Scores == nil {
		prof.Scores = map[string]Score{}
	}
	prof.Scores[key] = Score{Successes: successes, Trials: trials, UpdatedAt: time.Now().UTC()}
	prof.QualifiedAt = time.Now().UTC()
	prof.UpdatedAt = time.Now().UTC()
}

// Request describes what a cast needs.
type Request struct {
	Phase       string
	Capability  Capability
	Stance      Stance
	MinContext  int
	AvoidFamily string
	Scope       string
	ProjectID   string
	JobID       string
	// Explore asks for the runner-up rather than the top model. It is a
	// budgeted, visible experiment, not the default.
	Explore bool
	// PreferQuality breaks an unmeasured tie toward the larger model. It is
	// for interactive authoring where quality matters more than latency.
	PreferQuality bool
}

// Choice is one selected model.
type Choice struct {
	Model         string
	Family        string
	ContextTarget int
	Temperature   float64
	Score         float64
	Reason        string
}

type candidate struct {
	prof  Profile
	score float64
	why   string
}

func (p *Pool) candidates(req Request) []candidate {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]candidate, 0, len(p.Models))
	for _, prof := range p.Models {
		if prof.TooLarge {
			continue
		}
		if req.MinContext > 0 && prof.ContextTarget > 0 && prof.ContextTarget < req.MinContext {
			continue
		}
		score, why := combinedScore(*prof, req)
		out = append(out, candidate{prof: *prof, score: score, why: why})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].prof.ContextTarget != out[j].prof.ContextTarget {
			return out[i].prof.ContextTarget > out[j].prof.ContextTarget
		}
		// Before measurement, prefer the smaller model by default: it is the
		// safer choice on a machine whose resident capacity is unknown. An
		// authoring call asks for quality instead.
		if out[i].prof.SizeBytes != out[j].prof.SizeBytes {
			if req.PreferQuality {
				return out[i].prof.SizeBytes > out[j].prof.SizeBytes
			}
			return out[i].prof.SizeBytes < out[j].prof.SizeBytes
		}
		return out[i].prof.Name < out[j].prof.Name
	})
	return out
}

// combinedScore blends the scoped posterior with the global one. A scope
// needs at least two observations before it can outvote the parent prior.
func combinedScore(prof Profile, req Request) (float64, string) {
	key := string(req.Capability)
	if key == "" {
		key = string(CapabilityGeneral)
	}
	global := prof.Scores[key]
	if global.Trials == 0 {
		if s, ok := prof.Scores["stance:"+string(req.Stance)]; ok {
			global = s
		}
	}
	base := global.Value()
	why := "not yet measured; chosen by fit"
	if global.Trials > 0 {
		why = fmt.Sprintf("%.0f%% on %d measured %s case(s)", base*100, global.Trials, key)
	}
	if req.Scope != "" {
		if s, ok := prof.Scoped[req.Scope+"/"+key]; ok && s.Trials >= 2 {
			w := float64(s.Trials) / float64(s.Trials+4)
			base = w*s.Value() + (1-w)*base
			why += fmt.Sprintf("; %.0f%% on %d scoped case(s)", s.Value()*100, s.Trials)
		}
	}
	return base, why
}

// Choose returns the best model for a request. The second result is false
// when the pool is empty or every model fails the context requirement.
func (p *Pool) Choose(req Request) (Choice, bool) {
	cs := p.candidates(req)
	if len(cs) == 0 {
		return Choice{}, false
	}
	if req.Explore && len(cs) > 1 {
		p.remember(req, cs[1].prof.Name)
		return choiceOf(cs[1]), true
	}
	if req.AvoidFamily != "" {
		for _, c := range cs {
			if c.prof.Family != "" && c.prof.Family != req.AvoidFamily {
				p.remember(req, c.prof.Name)
				return choiceOf(c), true
			}
		}
	}
	p.remember(req, cs[0].prof.Name)
	return choiceOf(cs[0]), true
}

// ChooseMany returns up to n distinct models, preferring different families
// so a deliberation draws on decorrelated sources when the pool allows it.
func (p *Pool) ChooseMany(req Request, n int) []Choice {
	if n < 1 {
		return nil
	}
	cs := p.candidates(req)
	if len(cs) == 0 {
		return nil
	}
	out := make([]Choice, 0, n)
	used := map[string]bool{}
	families := map[string]bool{}
	// First pass: distinct families.
	for _, c := range cs {
		if len(out) >= n {
			break
		}
		if c.prof.Family != "" && families[c.prof.Family] {
			continue
		}
		families[c.prof.Family] = true
		used[c.prof.Name] = true
		out = append(out, choiceOf(c))
	}
	// Second pass: fill remaining slots, including same-family models.
	for _, c := range cs {
		if len(out) >= n {
			break
		}
		if used[c.prof.Name] {
			continue
		}
		used[c.prof.Name] = true
		out = append(out, choiceOf(c))
	}
	p.mu.Lock()
	for _, c := range out {
		p.rememberLocked(req, c.Model)
	}
	p.mu.Unlock()
	return out
}

func choiceOf(c candidate) Choice {
	return Choice{
		Model:         c.prof.Name,
		Family:        c.prof.Family,
		ContextTarget: c.prof.ContextTarget,
		Temperature:   c.prof.Temperature,
		Score:         c.score,
		Reason:        c.why,
	}
}

func (p *Pool) remember(req Request, model string) {
	p.mu.Lock()
	p.rememberLocked(req, model)
	p.mu.Unlock()
}

func (p *Pool) rememberLocked(req Request, model string) {
	if req.JobID == "" {
		return
	}
	c := p.last[req.JobID]
	c.models = append(c.models, model)
	c.capability = string(req.Capability)
	c.scope = req.Scope
	p.last[req.JobID] = c
}

// RecordTemplate records one grounded result for a deliberation shape.
func (p *Pool) RecordTemplate(name string, success bool) {
	if name == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Templates == nil {
		p.Templates = map[string]Score{}
	}
	s := p.Templates[name]
	s.Trials++
	if success {
		s.Successes++
	}
	s.UpdatedAt = time.Now().UTC()
	p.Templates[name] = s
}

// PreferPanel reports whether the measured evidence favors drawing more than
// one model for a low-risk task. It needs a few outcomes before switching away
// from the safe default.
func (p *Pool) PreferPanel() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.Models) < 2 {
		return false
	}
	solo, panel := p.Templates["solo"], p.Templates["panel"]
	if solo.Trials+panel.Trials < 4 {
		return false
	}
	return panel.Value() >= solo.Value()+0.05
}

// Observe records a terminal job outcome against the models it used. Only
// grounded outcomes should reach this method.
func (p *Pool) Observe(ctx context.Context, jobID string, success bool) {
	p.mu.Lock()
	c, ok := p.last[jobID]
	delete(p.last, jobID)
	p.mu.Unlock()
	if !ok {
		return
	}
	seen := map[string]bool{}
	for _, name := range c.models {
		if seen[name] {
			continue
		}
		seen[name] = true
		p.Record(name, c.capability, success)
		if c.scope != "" {
			p.RecordScoped(name, c.scope, c.capability, success)
		}
	}
	template := "solo"
	if len(seen) > 1 {
		template = "panel"
	}
	p.RecordTemplate(template, success)
	if err := p.Save(ctx); err != nil {
		// Outcome learning is best-effort; a save failure must not fail a
		// terminal job that has already been recorded.
		return
	}
}
