package profiler

import (
	"fmt"

	"github.com/google/pprof/profile"
)

// resolveKernelFunctions resolves kernel function names.
func (p *Profiler) resolveKernelFunctions(kernelLocations []*profile.Location) (map[uint64]*profile.Function, error) {
	kernelAddresses := map[uint64]struct{}{}
	for _, kloc := range kernelLocations {
		kernelAddresses[kloc.Address] = struct{}{}
	}
	kernelSymbols, err := p.ksymCache.Resolve(kernelAddresses)
	if err != nil {
		return nil, fmt.Errorf("resolve kernel symbols: %w", err)
	}
	kernelFunctions := map[uint64]*profile.Function{}
	for _, kloc := range kernelLocations {
		kernelFunction, ok := kernelFunctions[kloc.Address]
		if !ok {
			name := kernelSymbols[kloc.Address]
			if name == "" {
				name = "not found"
			}
			kernelFunction = &profile.Function{
				Name: name,
			}
			kernelFunctions[kloc.Address] = kernelFunction
		}
		if kernelFunction != nil {
			kloc.Line = []profile.Line{{Function: kernelFunction}}
		}
	}
	return kernelFunctions, nil
}
