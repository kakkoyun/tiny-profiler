package profiler

import "C" // nolint

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"

	bpf "github.com/aquasecurity/libbpfgo"
	"github.com/dustin/go-humanize"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/google/gops/goprocess"
	"github.com/google/pprof/profile"
	"github.com/parca-dev/parca-agent/pkg/byteorder"
	"github.com/parca-dev/parca-agent/pkg/debuginfo"
	"github.com/parca-dev/parca-agent/pkg/ksym"
	"github.com/parca-dev/parca-agent/pkg/maps"
	"github.com/parca-dev/parca-agent/pkg/objectfile"
	"golang.org/x/sys/unix"
)

//go:embed cpu.bpf.o
var bpfObj []byte

const (
	stackDepth       = 127 // Always needs to be sync with MAX_STACK_DEPTH in BPF program.
	doubleStackDepth = stackDepth * 2

	defaultRLimit = 1024 << 20 // ~1GB

	programName = "profile_cpu"
)

var errUnrecoverable = errors.New("unrecoverable error")

type ProfileWriter interface {
	Write(ctx context.Context, labels map[string]string, prof *profile.Profile) error
}

type Profiler struct {
	node              string
	logger            log.Logger
	profilingDuration time.Duration

	byteOrder binary.ByteOrder
	bpfMaps   *bpfMaps

	mtx                         *sync.RWMutex
	loopStartedAt               time.Time
	lastSuccessfulLoopStartedAt time.Time

	// Caches, caches everywhere!
	pidMappingFileCache *maps.PIDMappingFileCache
	ksymCache           *ksym.Cache
	objFileCache        objectfile.Cache

	profileWriter     ProfileWriter
	debugInfoUploader *debuginfo.DebugInfo
}

func NewProfiler(logger log.Logger, node string, profilingDuration time.Duration, opts ...Option) *Profiler {
	p := &Profiler{
		logger: logger,

		node:              node,
		profilingDuration: profilingDuration,

		mtx:       &sync.RWMutex{},
		byteOrder: byteorder.GetHostByteOrder(),

		ksymCache:           ksym.NewKsymCache(logger),
		pidMappingFileCache: maps.NewPIDMappingFileCache(logger),
		objFileCache:        objectfile.NewCache(10),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Profiler) Run(ctx context.Context) error {
	level.Debug(p.logger).Log("msg", "starting cgroup profiler")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m, err := bpf.NewModuleFromBufferArgs(bpf.NewModuleArgs{
		BPFObjBuff: bpfObj,
		BPFObjName: "tiny-profiler",
	})
	if err != nil {
		return fmt.Errorf("new bpf module: %w", err)
	}
	defer m.Close()

	if err := p.bumpMemlockRlimit(); err != nil {
		return fmt.Errorf("bump memlock rlimit: %w", err)
	}

	if err := m.BPFLoadObject(); err != nil {
		return fmt.Errorf("load bpf object: %w", err)
	}

	cpus := runtime.NumCPU()

	for i := 0; i < cpus; i++ {
		fd, err := unix.PerfEventOpen(&unix.PerfEventAttr{
			Type:   unix.PERF_TYPE_SOFTWARE,
			Config: unix.PERF_COUNT_SW_CPU_CLOCK,
			Size:   uint32(unsafe.Sizeof(unix.PerfEventAttr{})),
			Sample: 100,
			Bits:   unix.PerfBitDisabled | unix.PerfBitFreq,
		}, -1 /* pid */, i /* cpu id */, -1 /* group */, 0 /* flags */)
		if err != nil {
			return fmt.Errorf("open perf event: %w", err)
		}

		prog, err := m.GetProgram(programName)
		if err != nil {
			return fmt.Errorf("get bpf program: %w", err)
		}

		_, err = prog.AttachPerfEvent(fd)
		if err != nil {
			return fmt.Errorf("attach perf event: %w", err)
		}
	}

	counts, err := m.GetMap(countsMapName)
	if err != nil {
		return fmt.Errorf("get counts map: %w", err)
	}

	stackTraces, err := m.GetMap(stackTracesMapName)
	if err != nil {
		return fmt.Errorf("get stack traces map: %w", err)
	}
	p.bpfMaps = &bpfMaps{byteOrder: byteorder.GetHostByteOrder(), counts: counts, stackTraces: stackTraces}

	ticker := time.NewTicker(p.profilingDuration)
	defer ticker.Stop()

	level.Debug(p.logger).Log("msg", "start profiling loop")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		if err := p.profileLoop(ctx); err != nil {
			level.Warn(p.logger).Log("msg", "profile loop error", "err", err)
		}

		p.loopReport(err)
	}
}

func (p *Profiler) loopReport(lastError error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	if lastError == nil {
		p.lastSuccessfulLoopStartedAt = p.loopStartedAt
		p.loopStartedAt = time.Now()
	}
}

func (p *Profiler) profileStartedAt() time.Time {
	p.mtx.RLock()
	defer p.mtx.RUnlock()

	return p.loopStartedAt
}

type combinedStack [doubleStackDepth]uint64

type PID uint64

type Profile struct {
	captureTime time.Time

	samples map[combinedStack]*profile.Sample

	allLocations    []*profile.Location
	userLocations   map[uint32][]*profile.Location
	kernelLocations []*profile.Location

	userMappings  []*profile.Mapping
	kernelMapping *profile.Mapping
}

type stackCountKey struct {
	PID           uint32
	UserStackID   int32
	KernelStackID int32
}

func (p *Profiler) profileLoop(ctx context.Context) error {
	var (
		processMappings = maps.NewMapping(p.pidMappingFileCache)
		kernelMapping   = &profile.Mapping{
			File: "[kernel.kallsyms]",
		}

		allSamples      = map[PID]map[combinedStack]*profile.Sample{}
		sampleLocations = map[PID][]*profile.Location{}
		allLocations    = map[PID][]*profile.Location{}
		kernelLocations = map[PID][]*profile.Location{}
		userLocations   = map[PID]map[uint32][]*profile.Location{} // PID -> []*profile.Location
		locationIndices = map[PID]map[[2]uint64]int{}              // [PID, Address] -> index in locations
	)

	processes := goprocess.FindAll()
	for _, ps := range processes {
		level.Debug(p.logger).Log("msg", "attaching profiler to processes", "pid", ps.PID, "path", ps.Path)
	}

	it := p.bpfMaps.counts.Iterator()
	for it.Next() {
		keyBytes := it.Key()

		var key stackCountKey
		if err := binary.Read(bytes.NewBuffer(keyBytes), p.byteOrder, &key); err != nil {
			return fmt.Errorf("read stack count key: %w", err)
		}

		pid := PID(key.PID)
		i := sort.Search(len(processes), func(i int) bool {
			return PID(processes[i].PID) >= pid
		})
		if !(i < len(processes) && PID(processes[i].PID) == pid) {
			continue
		}

		stack := combinedStack{}
		userErr := p.bpfMaps.readUserStack(key.UserStackID, &stack)
		if userErr != nil {
			if errors.Is(userErr, errUnrecoverable) {
				return userErr
			}
			level.Debug(p.logger).Log("msg", "failed to read user stack", "err", userErr)
		}
		kernelErr := p.bpfMaps.readKernelStack(key.KernelStackID, &stack)
		if kernelErr != nil {
			if errors.Is(kernelErr, errUnrecoverable) {
				return kernelErr
			}
			level.Debug(p.logger).Log("msg", "failed to read kernel stack", "err", kernelErr)
		}
		if userErr != nil && kernelErr != nil {
			continue
		}

		value, err := p.bpfMaps.readStackCount(keyBytes)
		if err != nil {
			return fmt.Errorf("read value: %w", err)
		}
		if value == 0 {
			continue
		}

		_, ok := allSamples[pid]
		if !ok {
			allSamples[pid] = map[combinedStack]*profile.Sample{}
		}

		sample, ok := allSamples[pid][stack]
		if ok {
			sample.Value[0] += int64(value)
			continue
		}

		sampleLocations[pid] = []*profile.Location{}
		if _, ok = userLocations[pid]; !ok {
			userLocations[pid] = map[uint32][]*profile.Location{}
		}
		if _, ok = locationIndices[pid]; !ok {
			locationIndices[pid] = map[[2]uint64]int{}
		}

		// Collect Kernel stack trace samples.
		for _, addr := range stack[stackDepth:] {
			if addr != uint64(0) {
				key := [2]uint64{0, addr}
				// PID 0 not possible so we'll use it to identify the kernel.
				locationIndex, ok := locationIndices[pid][key]
				if !ok {
					locationIndex = len(allLocations[pid])
					l := &profile.Location{
						ID:      uint64(locationIndex + 1),
						Address: addr,
						Mapping: kernelMapping,
					}
					allLocations[pid] = append(allLocations[pid], l)
					kernelLocations[pid] = append(kernelLocations[pid], l)
					locationIndices[pid][key] = locationIndex
				}
				sampleLocations[pid] = append(
					sampleLocations[pid],
					allLocations[pid][locationIndex],
				)
			}
		}

		// Collect User stack trace samples.
		for _, addr := range stack[:stackDepth] {
			if addr != uint64(0) {
				k := [2]uint64{uint64(key.PID), addr}
				locationIndex, ok := locationIndices[pid][k]
				if !ok {
					locationIndex = len(allLocations[pid])

					m, err := processMappings.PIDAddrMapping(key.PID, addr)
					if err != nil {
						if !errors.Is(err, maps.ErrNotFound) {
							level.Warn(p.logger).Log("msg", "failed to get process mapping", "err", err)
						}
					}

					l := &profile.Location{
						ID: uint64(locationIndex + 1),
						// Try to normalize the address for a symbol for position independent code.
						Address: p.normalizeAddress(m, key.PID, addr),
						Mapping: m,
					}

					allLocations[pid] = append(allLocations[pid], l)
					userLocations[pid][key.PID] = append(userLocations[pid][key.PID], l)
					locationIndices[pid][k] = locationIndex
				}
				sampleLocations[pid] = append(
					sampleLocations[pid],
					allLocations[pid][locationIndex],
				)
			}
		}

		sample = &profile.Sample{
			Value:    []int64{int64(value)},
			Location: sampleLocations[pid],
		}
		allSamples[pid][stack] = sample
	}
	if it.Err() != nil {
		// TODO(kakkoyun): What happened now?
		// return fmt.Errorf("failed iterator: %w", it.Err())
		level.Warn(p.logger).Log("msg", "failed iterator", "err", it.Err())
	}

	var mappedFiles []maps.ProcessMapping
	mappings, mappedFiles := processMappings.AllMappings()

	if p.debugInfoUploader != nil {
		// Upload debug information of the discovered object files.
		go func() {
			var objFiles []*objectfile.MappedObjectFile
			for _, mf := range mappedFiles {
				objFile, err := p.objFileCache.ObjectFileForProcess(mf.PID, mf.Mapping)
				if err != nil {
					continue
				}
				objFiles = append(objFiles, objFile)
			}
			p.debugInfoUploader.EnsureUploaded(ctx, objFiles)
		}()
	}

	for pid, samples := range allSamples {
		prof := &Profile{
			captureTime:     p.profileStartedAt(),
			samples:         samples,
			allLocations:    allLocations[pid],
			kernelLocations: kernelLocations[pid],
			userLocations:   userLocations[pid],
			userMappings:    mappings,
			kernelMapping:   kernelMapping,
		}
		pprof, err := p.pprofProfile(prof)
		if err != nil {
			return fmt.Errorf("failed to build profile: %w", err)
		}

		labels := map[string]string{}
		labels["__name__"] = "tiny_profiler_cpu"
		labels["node"] = p.node
		labels["pid"] = fmt.Sprintf("%d", pid)
		ps, ok, _ := goprocess.Find(int(pid))
		if ok {
			labels["exec"] = ps.Exec
			labels["path"] = ps.Path
			labels["build_version"] = ps.BuildVersion
		}
		if err := p.profileWriter.Write(ctx, labels, pprof); err != nil {
			level.Error(p.logger).Log("msg", "failed to write profile", "err", err)
		}

		if err := p.bpfMaps.clean(); err != nil {
			level.Warn(p.logger).Log("msg", "failed to clean BPF maps", "err", err)
		}
	}

	return nil
}

// normalizeProfile calculates the base addresses of a position-independent binary and normalizes captured locations accordingly.
func (p *Profiler) normalizeAddress(m *profile.Mapping, pid uint32, addr uint64) uint64 {
	if m == nil {
		return addr
	}

	logger := log.With(p.logger, "pid", pid, "buildID", m.BuildID)
	if m.Unsymbolizable() {
		level.Debug(logger).Log("msg", "mapping is unsymbolizable")
		return addr
	}

	objFile, err := p.objFileCache.ObjectFileForProcess(pid, m)
	if err != nil {
		level.Debug(logger).Log("msg", "failed to open object file", "err", err)
		return addr
	}

	// Transform the address by normalizing Kernel memory offsets.
	normalizedAddr, err := objFile.ObjAddr(addr)
	if err != nil {
		level.Debug(logger).Log("msg", "failed to get normalized address from object file", "err", err)
		return addr
	}

	return normalizedAddr
}

// bumpMemlockRlimit increases the current memlock limit to a value more reasonable for the profiler's needs.
func (p *Profiler) bumpMemlockRlimit() error {
	rLimit := syscall.Rlimit{
		Cur: uint64(defaultRLimit),
		Max: uint64(defaultRLimit),
	}

	// RLIMIT_MEMLOCK is 0x8.
	if err := syscall.Setrlimit(unix.RLIMIT_MEMLOCK, &rLimit); err != nil {
		return fmt.Errorf("failed to increase rlimit: %w", err)
	}

	rLimit = syscall.Rlimit{}
	if err := syscall.Getrlimit(unix.RLIMIT_MEMLOCK, &rLimit); err != nil {
		return fmt.Errorf("failed to get rlimit: %w", err)
	}
	level.Debug(p.logger).Log("msg", "increased max memory locked rlimit", "limit", humanize.Bytes(rLimit.Cur))

	return nil
}
