package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	oklogrun "github.com/oklog/run"
	"github.com/parca-dev/parca-agent/pkg/agent"
	"github.com/parca-dev/parca-agent/pkg/debuginfo"
	profilestorepb "github.com/parca-dev/parca/gen/proto/go/parca/profilestore/v1alpha1"
	parcadebuginfo "github.com/parca-dev/parca/pkg/debuginfo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kakkoyun/tiny-profiler/profiler"
)

var (
	version string
	commit  string
	date    string
	goArch  string
)

type flags struct {
	LogLevel    string `kong:"enum='error,warn,info,debug',help='Log level.',default='info'"`
	HTTPAddress string `kong:"help='Address to bind HTTP server to.',default=':6060'"`

	Node              string        `kong:"default='localhost',help='Name node the process is running on. Used to identify the process.'"`
	ProfilingDuration time.Duration `kong:"help='The agent profiling duration to use. Leave this empty to use the defaults.',default='10s'"`

	LocalStoreDirectory string `kong:"help='The local directory to store the profiling data.',default='./tmp/profiles'"`

	// Optional remote Parca Server connection parameters.
	RemoteStoreAddress                string `kong:"help='gRPC address to send profiles and symbols to.'"`
	RemoteStoreBearerToken            string `kong:"help='Bearer token to authenticate with store.'"`
	RemoteStoreBearerTokenFile        string `kong:"help='File to read bearer token from to authenticate with store.'"`
	RemoteStoreInsecure               bool   `kong:"help='Send gRPC requests via plaintext instead of TLS.'"`
	RemoteStoreInsecureSkipVerify     bool   `kong:"help='Skip TLS certificate verification.'"`
	RemoteStoreDebugInfoUploadDisable bool   `kong:"help='Disable debuginfo collection and upload.',default='false'"`
}

var logger log.Logger

func main() {
	flags := flags{}
	kong.Parse(&flags)

	logger = newLogger(flags.LogLevel, logFormatLogfmt, "tiny-profiler")
	if err := setBuildInfo(); err != nil {
		level.Error(logger).Log("err", err)
	}

	logger = log.With(logger, "node", flags.Node)
	if flags.RemoteStoreAddress != "" {
		logger = log.With(logger, "store", flags.RemoteStoreAddress)
	}
	logger.Log("msg", "tiny profiler starting...")

	mux := http.NewServeMux()
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewBuildInfoCollector(),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := run(logger, reg, mux, &flags, ctx); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}

func run(logger log.Logger, reg prometheus.Registerer, mux *http.ServeMux, flags *flags, ctx context.Context) error {
	var (
		g    oklogrun.Group
		opts []profiler.Option
	)

	if flags.LocalStoreDirectory != "" {
		opts = append(opts, profiler.WithProfileWriter(profiler.NewFileWriter(flags.LocalStoreDirectory)))
	}

	if len(flags.RemoteStoreAddress) > 0 {
		conn, err := grpcConn(reg, flags)
		defer conn.Close()

		if err != nil {
			level.Error(logger).Log("err", err)
			os.Exit(1)
		}

		profileStoreClient := profilestorepb.NewProfileStoreServiceClient(conn)
		opts = append(opts, profiler.WithProfileWriter(profiler.NewRemoteProfileWriter(profileStoreClient)))

		debugInfoClient := debuginfo.NewNoopClient()
		if !flags.RemoteStoreDebugInfoUploadDisable {
			level.Info(logger).Log("msg", "debug information collection is enabled")
			debugInfoClient = parcadebuginfo.NewDebugInfoClient(conn)
			opts = append(opts, profiler.WithDebugInfoUploader(debuginfo.New(logger, debugInfoClient)))
		}

		batchWriteClient := agent.NewBatchWriteClient(logger, profileStoreClient, 10*time.Second)

		ctx, cancel := context.WithCancel(ctx)
		g.Add(func() error {
			level.Debug(logger).Log("msg", "starting Parca batch write client")
			return batchWriteClient.Run(ctx)
		}, func(error) {
			cancel()
		})
	}

	{
		profiler := profiler.NewProfiler(logger, flags.Node, flags.ProfilingDuration, opts...)

		ctx, cancel := context.WithCancel(ctx)
		g.Add(func() error {
			return profiler.Run(ctx)
		}, func(error) {
			cancel()
		})
	}

	{
		ln, err := net.Listen("tcp", flags.HTTPAddress)
		if err != nil {
			return err
		}
		g.Add(func() error {
			return http.Serve(ln, mux)
		}, func(error) {
			ln.Close()
		})
	}

	g.Add(oklogrun.SignalHandler(ctx, os.Interrupt, os.Kill))

	return g.Run()
}

const (
	logFormatLogfmt = "logfmt"
	LogFormatJSON   = "json"
)

func newLogger(logLevel, logFormat, debugName string) log.Logger {
	var (
		logger log.Logger
		lvl    level.Option
	)

	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		panic("unexpected log level")
	}

	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	if logFormat == LogFormatJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	}

	logger = level.NewFilter(logger, lvl)

	if debugName != "" {
		logger = log.With(logger, "name", debugName)
	}

	return log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
}

type buildInfo struct {
	GoArch, GoOs, VcsRevision, VcsTime string
	VcsModified                        bool
}

func setBuildInfo() error {
	buildInfo, err := fetchBuildInfo()
	if err != nil {
		return err
	}

	if commit == "" {
		commit = buildInfo.VcsRevision
	}
	if date == "" {
		date = buildInfo.VcsTime
	}
	if goArch == "" {
		goArch = buildInfo.GoArch
	}

	return nil
}

func fetchBuildInfo() (*buildInfo, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("can't read the build info")
	}

	buildInfo := buildInfo{}

	for _, setting := range bi.Settings {
		key := setting.Key
		value := setting.Value

		switch key {
		case "GOARCH":
			buildInfo.GoArch = value
		case "GOOS":
			buildInfo.GoOs = value
		case "vcs.revision":
			buildInfo.VcsRevision = value
		case "vcs.time":
			buildInfo.VcsTime = value
		case "vcs.modified":
			buildInfo.VcsModified = value == "true"
		}
	}

	level.Debug(logger).Log("msg", "tiny-profiler initialized",
		"version", version,
		"commit", commit,
		"date", date,
		"arch", goArch,
	)

	return &buildInfo, nil
}

func grpcConn(reg prometheus.Registerer, flags *flags) (*grpc.ClientConn, error) {
	met := grpc_prometheus.NewClientMetrics()
	met.EnableClientHandlingTimeHistogram()
	reg.MustRegister(met)

	opts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(parcadebuginfo.MaxMsgSize),
			grpc.MaxCallRecvMsgSize(parcadebuginfo.MaxMsgSize),
		),
		grpc.WithUnaryInterceptor(
			met.UnaryClientInterceptor(),
		),
		grpc.WithStreamInterceptor(
			met.StreamClientInterceptor(),
		),
	}
	if flags.RemoteStoreInsecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		config := &tls.Config{
			//nolint:gosec
			InsecureSkipVerify: flags.RemoteStoreInsecureSkipVerify,
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(config)))
	}

	if flags.RemoteStoreBearerToken != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&perRequestBearerToken{
			token:    flags.RemoteStoreBearerToken,
			insecure: flags.RemoteStoreInsecure,
		}))
	}

	if flags.RemoteStoreBearerTokenFile != "" {
		b, err := ioutil.ReadFile(flags.RemoteStoreBearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read bearer token from file: %w", err)
		}
		opts = append(opts, grpc.WithPerRPCCredentials(&perRequestBearerToken{
			token:    strings.TrimSpace(string(b)),
			insecure: flags.RemoteStoreInsecure,
		}))
	}

	return grpc.Dial(flags.RemoteStoreAddress, opts...)
}

type perRequestBearerToken struct {
	token    string
	insecure bool
}

func (t *perRequestBearerToken) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *perRequestBearerToken) RequireTransportSecurity() bool {
	return !t.insecure
}
