package profiler

import (
	"github.com/parca-dev/parca-agent/pkg/debuginfo"
)

type Option func(p *Profiler)

func WithDebugInfoUploader(d *debuginfo.DebugInfo) Option {
	return func(p *Profiler) {
		p.debugInfoUploader = d
	}
}

func WithProfileWriter(w ProfileWriter) Option {
	return func(p *Profiler) {
		p.profileWriter = w
	}
}
