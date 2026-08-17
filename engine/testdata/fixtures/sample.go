package pool

func Acquire(p *Pool) *Block { return p.Take() }

// @note #pool-release observation : Release the pool back to the allocator
// @author jane
// @see #memory-pool
//
// The pool must be returned after every acquire, even on error paths.
func Release(p *Pool) { p.Drain() }

// @note #pool-release todo P1 open P2 #go,#alloc #pool : Re-acquire loop is inconsistent
//
// Re-check the acquire loop for missing releases under early return.
func ReAcquire(p *Pool) { p.Retake() }

// @note #noid observation : 
// @see #missing-note
//
// bodyless note with no title.
func Debug(p *Pool) {}

// @note #memory-pool observation P1 : Track outstanding allocations in a counter
//
// Whenever the counter hits zero the pool can be trimmed.
func Trim(p *Pool) { p.Compact() }