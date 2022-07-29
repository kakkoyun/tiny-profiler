package profiler

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/pprof/profile"
	profilestorepb "github.com/parca-dev/parca/gen/proto/go/parca/profilestore/v1alpha1"
)

type RemoteProfileWriter struct {
	profileStoreClient profilestorepb.ProfileStoreServiceClient

	profileBufferPool sync.Pool
}

func NewRemoteProfileWriter(profileStoreClient profilestorepb.ProfileStoreServiceClient) *RemoteProfileWriter {
	return &RemoteProfileWriter{
		profileStoreClient: profileStoreClient,
		profileBufferPool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(nil)
			},
		},
	}
}

// Write sends the profile using the designated write client.
func (rw *RemoteProfileWriter) Write(ctx context.Context, labels map[string]string, prof *profile.Profile) error {
	//nolint:forcetypeassert
	buf := rw.profileBufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		rw.profileBufferPool.Put(buf)
	}()
	if err := prof.Write(buf); err != nil {
		return err
	}

	profileLabels := make([]*profilestorepb.Label, 0, len(labels))
	for key, value := range labels {
		profileLabels = append(profileLabels, &profilestorepb.Label{
			Name:  key,
			Value: value,
		})
	}

	_, err := rw.profileStoreClient.WriteRaw(ctx, &profilestorepb.WriteRawRequest{
		Normalized: true,
		Series: []*profilestorepb.RawProfileSeries{{
			Labels: &profilestorepb.LabelSet{Labels: profileLabels},
			Samples: []*profilestorepb.RawSample{{
				RawProfile: buf.Bytes(),
			}},
		}},
	})

	return err
}

func (p *Profiler) pprofProfile(pr *Profile) (*profile.Profile, error) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{{
			Type: "samples",
			Unit: "count",
		}},
		TimeNanos:     pr.captureTime.UnixNano(),
		DurationNanos: int64(time.Since(pr.captureTime)),

		// We sample at 100Hz, which is every 10 Million nanoseconds.
		PeriodType: &profile.ValueType{
			Type: "cpu",
			Unit: "nanoseconds",
		},
		Period: 10000000,
	}

	// Build Profile from samples, locations and mappings.
	for _, s := range pr.samples {
		prof.Sample = append(prof.Sample, s)
	}

	// Locations.
	prof.Location = pr.allLocations

	// User mappings.
	prof.Mapping = pr.userMappings

	// Kernel mappings.
	pr.kernelMapping.ID = uint64(len(prof.Mapping)) + 1
	prof.Mapping = append(prof.Mapping, pr.kernelMapping)

	// TODO(kakkoyun): Better to separate symbolization from pprof conversion.
	kernelFunctions, err := p.resolveKernelFunctions(pr.kernelLocations)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve kernel functions: %w", err)
	}
	for _, f := range kernelFunctions {
		f.ID = uint64(len(prof.Function)) + 1
		prof.Function = append(prof.Function, f)
	}

	return prof, nil
}
