SHELL := /bin/bash

# environment:
ALL_ARCH ?= amd64 arm64
ARCH_UNAME := $(shell uname -m)
ifeq ($(ARCH_UNAME), x86_64)
	ARCH ?= amd64
else
	ARCH ?= arm64
endif
ifeq ($(ARCH), amd64)
	LINUX_ARCH ?= x86_64=x86
else
	LINUX_ARCH ?= aarch64=arm64
endif

# tools:
CC ?= gcc
CLANG ?= clang
GO ?= go
CMD_LLC ?= llc
CMD_CC ?= $(CLANG)
CMD_DOCKER ?= docker
CMD_GIT ?= git
CMD_EMBEDMD ?= embedmd
DOCKER_BUILDER ?= parca-dev/cross-builder

# inputs and outputs:
OUT_DIR ?= dist
GO_SRC := $(shell find . -type f -name '*.go')
OUT_BIN := $(OUT_DIR)/tiny-profiler
OUT_DOCKER ?= ghcr.io/kakkoyun/tiny-profiler

VMLINUX := profiler/vmlinux.h
BPF_SRC := profiler/cpu.bpf.c
OUT_BPF_DIR := profiler
OUT_BPF := $(OUT_BPF_DIR)/cpu.bpf.o

BPF_HEADERS := 3rdparty/include
BPF_BUNDLE := $(OUT_DIR)/tiny-profiler.bpf.tar.gz

LIBBPF_SRC := 3rdparty/libbpf/src
LIBBPF_HEADERS := $(OUT_DIR)/libbpf/$(ARCH)/usr/include
LIBBPF_OBJ := $(OUT_DIR)/libbpf/$(ARCH)/libbpf.a

# CGO build flags:
PKG_CONFIG ?= pkg-config
CGO_CFLAGS_STATIC =-I$(abspath $(LIBBPF_HEADERS))
CGO_CFLAGS ?= $(CGO_CFLAGS_STATIC)
CGO_LDFLAGS_STATIC = -fuse-ld=ld $(abspath $(LIBBPF_OBJ))
CGO_LDFLAGS ?= $(CGO_LDFLAGS_STATIC)

CGO_EXTLDFLAGS =-extldflags=-static
CGO_CFGLAGS_DYN =-I. -I/usr/include/
CGO_LDFLAGS_DYN =-fuse-ld=ld -lelf -lz -lbpf

# libbpf build flags: (CFLAGS = -g -O2 -Wall -fpie)
CFLAGS ?= -g -O2 -Werror -Wall -std=gnu89 # default CFLAGS
LDFLAGS ?= -fuse-ld=lld

# version:
KERN_RELEASE ?= $(shell uname -r)
KERN_BLD_PATH ?= $(if $(KERN_HEADERS),$(KERN_HEADERS),/lib/modules/$(KERN_RELEASE)/build)
KERN_SRC_PATH ?= $(if $(KERN_HEADERS),$(KERN_HEADERS),$(if $(wildcard /lib/modules/$(KERN_RELEASE)/source),/lib/modules/$(KERN_RELEASE)/source,$(KERN_BLD_PATH)))
ifeq ($(GITHUB_BRANCH_NAME),)
	BRANCH := $(shell git rev-parse --abbrev-ref HEAD)-
else
	BRANCH := $(GITHUB_BRANCH_NAME)-
endif
ifeq ($(GITHUB_SHA),)
	COMMIT := $(shell git describe --no-match --dirty --always --abbrev=8)
else
	COMMIT := $(shell echo $(GITHUB_SHA) | cut -c1-8)
endif
VERSION ?= $(if $(RELEASE_TAG),$(RELEASE_TAG),$(shell $(CMD_GIT) describe --tags 2>/dev/null || echo '$(BRANCH)$(COMMIT)'))

# build:
.PHONY: all
all: bpf build

.PHONY: build
build: $(OUT_BPF) $(OUT_BIN) $(OUT_BIN_DEBUG_INFO)

.PHONY: go/deps
go/deps:
	$(GO) mod tidy

$(OUT_DIR):
	mkdir -p $@

GO_ENV := CGO_ENABLED=1 GOOS=linux GOARCH=$(ARCH) CC="$(CMD_CC)"
CGO_ENV := CGO_CFLAGS="$(CGO_CFLAGS)" CGO_LDFLAGS="$(CGO_LDFLAGS)"
GO_BUILD_FLAGS := -tags osusergo,netgo -mod=readonly -trimpath -v

go_env := GOOS=linux GOARCH=$(ARCH:x86_64=amd64) CC=$(CMD_CLANG) CGO_CFLAGS="-I $(abspath $(LIBBPF_HEADERS))" CGO_LDFLAGS="$(abspath $(LIBBPF_OBJ))"
$(OUT_BIN): bpf libbpf $(LIBBPF_HEADERS) $(LIBBPF_OBJ) $(filter-out *_test.go,$(GO_SRC)) $(BPF_BUNDLE) go/deps | $(OUT_DIR)
	find dist -exec touch -t 202101010000.00 {} +
	$(GO_ENV) $(CGO_ENV) $(GO) build $(SANITIZERS) $(GO_BUILD_FLAGS) --ldflags="$(CGO_EXTLDFLAGS)" -o $@ .

# bpf build:
bpf_bundle_dir := $(OUT_DIR)/tiny-profiler.bpf
$(BPF_BUNDLE): $(BPF_SRC) $(LIBBPF_HEADERS)/bpf $(BPF_HEADERS)
	mkdir -p $(bpf_bundle_dir)
	cp $$(find $^ -type f) $(bpf_bundle_dir)

.PHONY: bpf
bpf: $(OUT_BPF)

$(OUT_BPF): $(BPF_SRC) $(LIBBPF_HEADERS) $(LIBBPF_OBJ) $(BPF_HEADERS) | $(OUT_DIR)
	mkdir -p $(OUT_BPF_DIR)
	$(CMD_CC) -S \
		-D__BPF_TRACING__ \
		-D__KERNEL__ \
		-D__TARGET_ARCH_$(LINUX_ARCH) \
		-I $(LIBBPF_HEADERS)/bpf \
		-I $(BPF_HEADERS) \
		-Wno-address-of-packed-member \
		-Wno-compare-distinct-pointer-types \
		-Wno-deprecated-declarations \
		-Wno-gnu-variable-sized-type-not-at-end \
		-Wno-pointer-sign \
		-Wno-pragma-once-outside-header \
		-Wno-unknown-warning-option \
		-Wno-unused-value \
		-Wdate-time \
		-Wunused \
		-Wall \
		-fno-stack-protector \
		-fno-jump-tables \
		-fno-unwind-tables \
		-fno-asynchronous-unwind-tables \
		-xc \
		-nostdinc \
		-target bpf \
		-O2 -emit-llvm -c -g $< -o $(@:.o=.ll)
	$(CMD_LLC) -march=bpf -filetype=obj -o $@ $(@:.o=.ll)
	rm $(@:.o=.ll)

# libbpf build:
.PHONY: libbpf
libbpf: $(LIBBPF_HEADERS) $(LIBBPF_OBJ)

check_%:
	@command -v $* >/dev/null || (echo "missing required tool $*" ; false)

libbpf_compile_tools = $(CMD_LLC) $(CMD_CC)
.PHONY: libbpf_compile_tools
$(libbpf_compile_tools): % : check_%

$(LIBBPF_SRC):
	test -d $(LIBBPF_SRC) || git submodule update --init --remote

$(LIBBPF_HEADERS) $(LIBBPF_HEADERS)/bpf $(LIBBPF_HEADERS)/linux: | $(OUT_DIR) libbpf_compile_tools $(LIBBPF_SRC)
	$(MAKE) -C $(LIBBPF_SRC) CC="$(CMD_CC)" CFLAGS="$(CFLAGS)" LDFLAGS="$(LDFLAGS)" install_headers install_uapi_headers DESTDIR=$(abspath $(OUT_DIR))/libbpf/$(ARCH)

$(LIBBPF_OBJ): | $(OUT_DIR) libbpf_compile_tools $(LIBBPF_SRC)
	$(MAKE) -C $(LIBBPF_SRC) CC="$(CMD_CC)" CFLAGS="$(CFLAGS)" LDFLAGS="$(LDFLAGS)" OBJDIR=$(abspath $(OUT_DIR))/libbpf/$(ARCH) BUILD_STATIC_ONLY=1

$(VMLINUX):
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > $@

# static analysis:
lint: go/lint vet

.PHONY: go/lint
go/lint:
	golangci-lint run

.PHONY: vet
vet: $(GO_SRC) $(LIBBPF_HEADERS) $(LIBBPF_OBJ)
	$(GO_ENV) $(CGO_ENV) $(GO) vet -v $(shell $(GO) list ./...)

.PHONY: format
format: go/fmt c/fmt

.PHONY: c/fmt
c/fmt:
	clang-format -i --style=LLVM $(BPF_SRC)

.PHONY: bpf/fmt
bpf/fmt:
	$(MAKE) -C bpf format

.PHONY: go/fmt
go/fmt:
	$(GO) fmt $(shell $(GO) list ./...)

# test:
.PHONY: test
test: $(GO_SRC) $(LIBBPF_HEADERS) $(LIBBPF_OBJ) $(OUT_BPF)
	$(GO_ENV) $(CGO_ENV) $(GO) test $(SANITIZERS) -v $(shell $(GO) list ./...)

# clean:
.PHONY: mostlyclean
mostlyclean:
	-rm -rf $(OUT_BIN) $(bpf_bundle_dir) $(OUT_BPF) $(BPF_BUNDLE)

.PHONY: clean
clean:
	rm -f $(OUT_BPF)
	-FILE="$(docker_builder_file)" ; \
	if [ -r "$$FILE" ] ; then \
		$(CMD_DOCKER) rmi "$$(< $$FILE)" ; \
	fi
	-rm -rf dist $(OUT_DIR)
	$(MAKE) -C $(LIBBPF_SRC) clean

# container:
.PHONY: container
container: $(OUT_DIR)
	podman build \
		--platform linux/amd64,linux/arm64 \
		--timestamp 0 \
		--manifest $(OUT_DOCKER):$(VERSION) .

.PHONY: container-dev
container-dev:
	docker build -t kakkoyun/tiny-profiler:dev --build-arg=GOLANG_BASE=golang:1.18.3-bullseye --build-arg=DEBIAN_BASE=debian:bullseye-slim .

.PHONY: sign-container
sign-container:
	crane digest $(OUT_DOCKER):$(VERSION)
	cosign sign --force -a GIT_HASH=$(COMMIT) -a GIT_VERSION=$(VERSION) $(OUT_DOCKER)@$(shell crane digest $(OUT_DOCKER):$(VERSION))

.PHONY: push-container
push-container:
	podman manifest push --all $(OUT_DOCKER):$(VERSION) docker://$(OUT_DOCKER):$(VERSION)

.PHONY: push-local-container
push-local-container:
	podman push $(OUT_DOCKER):$(VERSION) docker-daemon:docker.io/$(OUT_DOCKER):$(VERSION)

# test cross-compile release pipeline:
GOLANG_CROSS_VERSION := v1.18.3

.PHONY: $(DOCKER_BUILDER)
$(DOCKER_BUILDER): Dockerfile.cross-builder | $(OUT_DIR) check_$(CMD_DOCKER)
 	# Build an image on top of goreleaser/goreleaser-cross:${GOLANG_CROSS_VERSION} with the necessary dependencies.
	$(CMD_DOCKER) build -t $(DOCKER_BUILDER):$(GOLANG_CROSS_VERSION) --build-arg=GOLANG_CROSS_VERSION=$(GOLANG_CROSS_VERSION) - < $<

.PHONY: release-dry-run
release-dry-run: $(DOCKER_BUILDER) bpf libbpf
	$(CMD_DOCKER) run \
		--rm \
		--privileged \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v "$(PWD):/__w/tiny-profiler/tiny-profiler" \
		-v "$(GOPATH)/pkg/mod":/go/pkg/mod \
		-w /__w/tiny-profiler/tiny-profiler \
		$(DOCKER_BUILDER):$(GOLANG_CROSS_VERSION) \
		release --rm-dist --auto-snapshot --skip-validate --skip-publish --debug

.PHONY: release-build
release-build: $(DOCKER_BUILDER) bpf libbpf
	$(CMD_DOCKER) run \
		--rm \
		--privileged \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v "$(PWD):/__w/tiny-profiler/tiny-profiler" \
		-v "$(GOPATH)/pkg/mod":/go/pkg/mod \
		-w /__w/tiny-profiler/tiny-profiler \
		$(DOCKER_BUILDER):$(GOLANG_CROSS_VERSION) \
		build --rm-dist --skip-validate --snapshot --debug

# docs:
$(OUT_DIR)/help.txt: $(OUT_BIN)
	$(OUT_BIN) --help > $@

README.md: $(OUT_DIR)/help.txt
	$(CMD_EMBEDMD) -w README.md
