package memory

import (
	"context"
	"sync/atomic"
)

// CodeGen is a process-local, monotonically increasing domain.CodeGenerator.
// It stands in for the Redis INCR / Postgres sequence based generator used
// in production.
type CodeGen struct {
	counter uint64
}

// NewCodeGen returns a generator whose first call to Next returns start+1.
// A non-zero start avoids handing out the reserved value 0 (which base62
// encodes as the single character "0") to the first real link.
func NewCodeGen(start uint64) *CodeGen {
	return &CodeGen{counter: start}
}

func (g *CodeGen) Next(_ context.Context) (uint64, error) {
	return atomic.AddUint64(&g.counter, 1), nil
}
