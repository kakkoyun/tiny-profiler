![Build](https://github.com/kakkoyun/tiny-profiler/actions/workflows/build.yml/badge.svg)
![Container](https://github.com/kakkoyun/tiny-profiler/actions/workflows/container.yml/badge.svg)
[![Apache 2 License](https://img.shields.io/badge/license-Apache%202-blue.svg)](LICENSE)

# tiny-profiler

A Proof-of-concept CPU profiler written in Go using eBPF

## Configuration

Flags:

[embedmd]:# (dist/help.txt)
```txt
Usage: tiny-profiler

Flags:
  -h, --help                      Show context-sensitive help.
      --log-level="info"          Log level.
      --http-address=":8080"      Address to bind HTTP server to.
      --node="localhost"          Name node the process is running on. Used to
                                  identify the process.
      --profiling-duration=10s    The agent profiling duration to use. Leave
                                  this empty to use the defaults.
      --local-store-directory="./tmp/profiles"
                                  The local directory to store the profiling
                                  data.
      --remote-store-address=STRING
                                  gRPC address to send profiles and symbols to.
      --remote-store-bearer-token=STRING
                                  Bearer token to authenticate with store.
      --remote-store-bearer-token-file=STRING
                                  File to read bearer token from to authenticate
                                  with store.
      --remote-store-insecure     Send gRPC requests via plaintext instead of
                                  TLS.
      --remote-store-insecure-skip-verify
                                  Skip TLS certificate verification.
      --remote-store-debug-info-upload-disable
                                  Disable debuginfo collection and upload.
```

## License

User-space code: Apache 2

Kernel-space code (eBPF profiler): GNU General Public License, version 2
