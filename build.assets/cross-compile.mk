# Makefile for building cross-compilers with crosstool-ng, building
# the third-party C library dependencies for Teleport.

ARCH ?= $(shell go env GOARCH)

repo_root = $(abspath $(dir $(firstword $(MAKEFILE_LIST)))/..)
BUILDDIR = $(repo_root)/build

# THIRDPARTY_DIR is the root of where third-party libraries and programs are
# downloaded, built and installed.
THIRDPARTY_DIR = $(BUILDDIR)/thirdparty

# THIRDPARTY_DLDIR holds downloaded tarballs of third-party libraries and
# programs so we don't have to keep downloading them. It is safe to delete.
THIRDPARTY_DLDIR = $(THIRDPARTY_DIR)/download

# THIRDPARTY_PREFIX is the root of where architecture-specific third-party
# libraries are installed. Each architecture has its own directory as libraries
# are architecture-specific. THIRDPARTY_SRCDIR is the directory where the source
# for third-party is extracted and built. Each architecture has its own
# extracted source as the build is done within the source tree.
THIRDPARTY_PREFIX = $(THIRDPARTY_DIR)/$(ARCH)
THIRDPARTY_SRCDIR = $(THIRDPARTY_PREFIX)/src

# THIRDPARTY_HOST_PREFIX is the root of where host-specific third-party
# programs are installed, such as ct-ng and the compilers it builds. These
# run on the host that is running the build, regardless of that host
# architecture. THIRDPARTY_HOST_SRCDIR is the directory where the source
# for host-specific third-party applications is extracted and built.
THIRDPARTY_HOST_PREFIX = $(THIRDPARTY_DIR)/host
THIRDPARTY_HOST_SRCDIR = $(THIRDPARTY_HOST_PREFIX)/src

# -----------------------------------------------------------------------------
# tp-src-dir and tp-src-host-dir expand to the source directory for a third-
# party source directory which has the version of the source appended. It
# is used with `$(call ...)`, like `$(call tp-src-dir,zlib)` or
# `$(call tp-src-host-dir,ctng)`.
tp-src-dir = $(THIRDPARTY_SRCDIR)/$1-$($1_VERSION)
tp-src-host-dir = $(THIRDPARTY_HOST_SRCDIR)/$1-$($1_VERSION)

# -----------------------------------------------------------------------------
# crosstool-ng
#
# crosstool-ng is a host tool - it runs on the build host. It is installed in
# $(THIRDPARTY_HOST_PREFIX).

ctng_VERSION = 1.26.0
ctng_GIT_REF = crosstool-ng-$(ctng_VERSION)
ctng_GIT_REF_HASH = 334f6d6479096b20e80fd39e35f404319bc251b5
ctng_GIT_REPO = https://github.com/crosstool-ng/crosstool-ng
ctng_SRCDIR = $(call tp-src-host-dir,ctng)

.PHONY: tp-build-ctng
tp-build-ctng: fetch-git-ctng
	cd $(ctng_SRCDIR) && ./bootstrap
	cd $(ctng_SRCDIR) && ./configure --prefix=$(THIRDPARTY_HOST_PREFIX)
	make -C $(ctng_SRCDIR) -j$(shell nproc)
	make -C $(ctng_SRCDIR) install

# -----------------------------------------------------------------------------
# Crosstool-ng compilers
#
# We use crosstool-ng, installed in $(THIRDPARTY_HOST_PREFIX) to build a
# compiler and glibc for each of the architectures: amd64, arm64, 386 and arm.
# These architecture names are as Go names them. The architecture of the
# toolchain to build is specified by the $(ARCH) variable.

CTNG_BUILDDIR = $(THIRDPARTY_PREFIX)/ctng
$(CTNG_BUILDDIR):
	mkdir -p $@

# Run a ctng command, copying the architecture-specific config into a build directory
# and saving it again after if it has changed. Useful to reconfigure and to build
# ctng. e.g.:
# make ARCH=amd64 ctng-menuconfig
# make ARCH=amd64 ctng-build
CTNG_DEFCONFIG = $(CTNG_BUILDDIR)/defconfig
CTNG_CONFIG = $(CTNG_BUILDDIR)/.config

# Create a defconfig if it does not exist
$(repo_root)/build.assets/ct-ng-configs/$(ARCH).defconfig:
	touch $@

# Copy the defconfig into the build dir
$(CTNG_DEFCONFIG): $(repo_root)/build.assets/ct-ng-configs/$(ARCH).defconfig | $(CTNG_BUILDDIR)
	cp $^ $@

# Create an expanded config from the defconfig
$(CTNG_CONFIG): $(CTNG_DEFCONFIG)
	cd $(CTNG_BUILDDIR) && $(THIRDPARTY_HOST_PREFIX)/bin/ct-ng defconfig

# Run `ct-ng menuconfig` on the arch-specific config from the defconfig in build.assets
# and copy it back when finished with menuconfig
ctng-menuconfig: $(CTNG_CONFIG) | $(CTNG_BUILDDIR)
	cd $(CTNG_BUILDDIR) && $(THIRDPARTY_HOST_PREFIX)/bin/ct-ng menuconfig
	cd $(CTNG_BUILDDIR) && $(THIRDPARTY_HOST_PREFIX)/bin/ct-ng savedefconfig
	cp $(CTNG_BUILDDIR)/defconfig $(repo_root)/build.assets/ct-ng-configs/$(ARCH).defconfig

# Build the toolchain with the config in the defconfig for the architecture. We need to
# clear out some env vars because ct-ng does not want them set. We export a couple of
# vars because we reference them in the config.
ctng-build: $(CTNG_CONFIG) | $(CTNG_BUILDDIR)
	$(eval undefine LIBRARY_PATH)
	$(eval undefine C_INCLUDE_PATH)
	$(eval undefine PKG_CONFIG_PATH)
	$(eval export THIRDPARTY_HOST_PREFIX)
	$(eval export THIRDPARTY_DLDIR)
	cd $(CTNG_BUILDDIR) && $(THIRDPARTY_HOST_PREFIX)/bin/ct-ng build


# =============================================================================
# Environment setup for building with ctng toolchain
#
# If we have a ctng cross compiler for the target arch, use it unless
# USE_CTNG is `no` or empty.

CTNG_TARGET = $(CTNG_TARGET_$(ARCH))
CTNG_TARGET_amd64 = x86_64-glibc217-linux-gnu
CTNG_TARGET_arm64 = aarch64-glibc217-linux-gnu
CTNG_TARGET_386 = i686-glibc217-linux-gnu
CTNG_TARGET_arm = armv7-glibc217-linux-gnueabi

# The crosstool/toolchain architecture is a little different to the Go
# architecture. It's possible that this is just libpam specific as that
# is all that currently uses this var.
CTNG_ARCH = $(CTNG_ARCH_$(ARCH))
CTNG_ARCH_amd64 = amd64
CTNG_ARCH_arm64 = arm64
CTNG_ARCH_arm = arm
CTNG_ARCH_386 = i686

ifneq ($(strip $(CTNG_TARGET)),)
ifneq ($(filter-out $(USE_CTNG),no),)

PATH := $(THIRDPARTY_HOST_PREFIX)/$(CTNG_TARGET)/bin:$(PATH)
CC = $(CTNG_TARGET)-gcc
CXX = $(CTNG_TARGET)-g++
LD = $(CTNG_TARGET)-ld
export CC CXX LD PATH

# Set vars used by the toolchain to find include directories and libraries and
# for pkg-config to find third-party configs.
C_INCLUDE_PATH = $(THIRDPARTY_PREFIX)/include
LIBRARY_PATH = $(THIRDPARTY_PREFIX)/lib
PKG_CONFIG_PATH = $(THIRDPARTY_PREFIX)/lib/pkgconfig
export C_INCLUDE_PATH LIBRARY_PATH PKG_CONFIG_PATH

endif
endif

# =============================================================================
# Building Teleport

.PHONY: build-teleport
build-teleport:
	$(MAKE) -C .. OS=linux ARCH=$(ARCH) FIDO2=yes PIV=yes release

# =============================================================================
# Third-party libraries needed to build Teleport.
#
# We build these libraries ourself and statically link them into the Teleport
# binary as we need them build with PIE (Position Independent Executable) mode
# so as to make use of ASLR (Address Space Layout Randomization). We cannot
# rely on a host OS/packager to have built them this way.

THIRDPARTY_LIBS = zlib zstd libelf libbpf libtirpc libpam

.PHONY: thirdparty-build-libs
thirdparty-build-libs: $(addprefix tp-build-,$(THIRDPARTY_LIBS))

# -----------------------------------------------------------------------------
# zlib

zlib_VERSION = 1.3.1
zlib_GIT_REF = v$(zlib_VERSION)
zlib_GIT_REF_HASH = 51b7f2abdade71cd9bb0e7a373ef2610ec6f9daf
zlib_GIT_REPO = https://github.com/madler/zlib
zlib_SRCDIR = $(call tp-src-dir,zlib)

.PHONY: tp-build-zlib
tp-build-zlib: fetch-git-zlib
	cd $(zlib_SRCDIR) && \
		./configure --prefix="$(THIRDPARTY_PREFIX)" --static
	$(MAKE) -C $(zlib_SRCDIR) CFLAGS+=-fPIE -j$(shell nproc)
	$(MAKE) -C $(zlib_SRCDIR) install

# -----------------------------------------------------------------------------
# zstd

zstd_VERSION = 1.5.6
zstd_GIT_REF = v$(zstd_VERSION)
zstd_GIT_REF_HASH = 794ea1b0afca0f020f4e57b6732332231fb23c70
zstd_GIT_REPO = https://github.com/facebook/zstd
zstd_SRCDIR = $(call tp-src-dir,zstd)

.PHONY: tp-build-zstd
tp-build-zstd: fetch-git-zstd
	$(MAKE) -C $(zstd_SRCDIR) CPPFLAGS_STATICLIB+=-fPIE -j$(shell nproc)
	$(MAKE) -C $(zstd_SRCDIR) install PREFIX=$(THIRDPARTY_PREFIX)

# -----------------------------------------------------------------------------
# libelf

libelf_VERSION = 0.191
libelf_GIT_REF = v$(libelf_VERSION)
libelf_GIT_REF_HASH = b80c36da9d70158f9a38cfb9af9bb58a323a5796
libelf_GIT_REPO = https://github.com/arachsys/libelf
libelf_SRCDIR = $(call tp-src-dir,libelf)

.PHONY: tp-build-libelf
tp-build-libelf: fetch-git-libelf
	$(MAKE) -C $(libelf_SRCDIR) CFLAGS+=-fPIE -j$(shell nproc) libelf.a
	$(MAKE) -C $(libelf_SRCDIR) install-headers install-static PREFIX=$(THIRDPARTY_PREFIX)

# -----------------------------------------------------------------------------
# libbpf

libbpf_VERSION = 1.4.0
libbpf_GIT_REF = v$(libbpf_VERSION)
libbpf_GIT_REF_HASH = 20ea95b4505c477af3b6ff6ce9d19cee868ddc5d
libbpf_GIT_REPO = https://github.com/libbpf/libbpf
libbpf_SRCDIR = $(call tp-src-dir,libbpf)

.PHONY: tp-build-libbpf
tp-build-libbpf: fetch-git-libbpf
	$(MAKE) -C $(libbpf_SRCDIR)/src \
		BUILD_STATIC_ONLY=y EXTRA_CFLAGS=-fPIE PREFIX=$(THIRDPARTY_PREFIX) LIBSUBDIR=lib V=1 \
		install install_uapi_headers

# -----------------------------------------------------------------------------
# libtirpc

libtirpc_VERSION = 1.3.4
libtirpc_SHA1 = 63c800f81f823254d2706637bab551dec176b99b
libtirpc_DOWNLOAD_URL = https://zenlayer.dl.sourceforge.net/project/libtirpc/libtirpc/$(libtirpc_VERSION)/libtirpc-$(libtirpc_VERSION).tar.bz2
libtirpc_STRIP_COMPONENTS = 1
libtirpc_SRCDIR = $(call tp-src-dir,libtirpc)

.PHONY: tp-build-libtirpc
tp-build-libtirpc: fetch-https-libtirpc
	cd $(libtirpc_SRCDIR) && \
		CFLAGS=-fPIE ./configure --prefix=$(THIRDPARTY_PREFIX) \
		--disable-gssapi $(if $(CTNG_TARGET),--host=$(CTNG_TARGET))
	make -C $(libtirpc_SRCDIR) -j$(shell nproc)
	make -C $(libtirpc_SRCDIR) install

# -----------------------------------------------------------------------------
# libpam

libpam_VERSION = 1.6.1
libpam_GIT_REF = v$(libpam_VERSION)
libpam_GIT_REF_HASH = 9438e084e2b318bf91c3912c0b8ff056e1835486
libpam_GIT_REPO = https://github.com/linux-pam/linux-pam
libpam_SRCDIR = $(call tp-src-dir,libpam)

.PHONY: tp-build-libpam
tp-build-libpam: fetch-git-libpam
	cd $(libpam_SRCDIR) && \
		./autogen.sh
	cd $(libpam_SRCDIR) && \
		CFLAGS=-fPIE ./configure --prefix=$(THIRDPARTY_PREFIX) \
		--disable-doc --disable-examples \
		$(if $(CTNG_ARCH),--host=$(CTNG_ARCH))
	make -C $(libpam_SRCDIR) -j$(shell nproc)
	make -C $(libpam_SRCDIR) install

# -----------------------------------------------------------------------------
# Helpers

tp-clean-%:
	-rm -rf $(call tp-src-dir,$*)
	-$(if $(tp-download-url),rm $(tp-download-filename))

# Create top-level directories when required
$(THIRDPARTY_SRCDIR) $(THIRDPARTY_HOST_SRCDIR) $(THIRDPARTY_DLDIR):
	mkdir -p $@

# vars for fetch-git-%. `$*` represents the `%` match.
tp-ref = $($*_GIT_REF)
tp-git-repo = $($*_GIT_REPO)
tp-ref-hash = $($*_GIT_REF_HASH)
tp-git-src-dir = $($*_SRCDIR)
define tp-git-fetch-cmd
	git -C "$(THIRDPARTY_SRCDIR)" \
		-c advice.detachedHead=false clone --depth=1 \
		--branch=$(tp-ref) $(tp-git-repo) "$(tp-git-src-dir)"
endef

# Fetch source via git.
fetch-git-%: | $(THIRDPARTY_SRCDIR)
	$(if $(wildcard $(tp-git-src-dir)),,$(tp-git-fetch-cmd))
	@if [ "$$(git -C "$(tp-git-src-dir)" rev-parse HEAD)" != "$(tp-ref-hash)" ]; then \
		echo "Found unexpected HEAD commit for $(1)"; \
		echo "Expected: $(tp-ref-hash)"; \
		echo "Got: $$(git -C "$(tp-git-src-dir)" rev-parse HEAD)"; \
		exit 1; \
	fi

# vars for fetch-https-%. `$*` represents the `%` match.
tp-download-url = $($*_DOWNLOAD_URL)
tp-sha1 = $($*_SHA1)
tp-download-filename = $(THIRDPARTY_DLDIR)/$(notdir $(tp-download-url))
tp-strip-components = $($*_STRIP_COMPONENTS)
tp-https-download-cmd = curl -fsSL --output "$(tp-download-filename)" "$(tp-download-url)"
tp-https-src-dir = $(call tp-src-dir,$*)
define tp-https-extract-tar-cmd
	@echo "$(tp-sha1)  $(tp-download-filename)" | sha1sum --check
	mkdir -p "$(tp-https-src-dir)"
	tar -x -a \
		--file="$(tp-download-filename)" \
		--directory="$(tp-https-src-dir)" \
		--strip-components="$(tp-strip-components)"
endef

# Fetch source tarball via https
fetch-https-%: | $(THIRDPARTY_SRCDIR) $(THIRDPARTY_DLDIR)
	$(if $(wildcard $(tp-download-filename)),,$(tp-https-download-cmd))
	$(if $(wildcard $(tp-https-src-dir)),,$(tp-https-extract-tar-cmd))

diagnose:
	@echo repo_root = $(repo_root)
	@echo THIRDPARTY_DIR = $(THIRDPARTY_DIR)
	@echo THIRDPARTY_SRCDIR = $(THIRDPARTY_SRCDIR)
	@echo THIRDPARTY_DLDIR = $(THIRDPARTY_DLDIR)
	@echo THIRDPARTY_PREFIX = $(THIRDPARTY_PREFIX)
	@echo CTNG_TARGET = $(CTNG_TARGET)
	@echo PATH = $(PATH)

