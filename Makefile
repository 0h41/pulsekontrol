# Check needed executables
EXECUTABLES = go patch strip
K := $(foreach exec,$(EXECUTABLES), \
  $(if $(shell which $(exec)),some string,$(error "No $(exec) in PATH")))

# Get git informations
git_tag := $(shell git describe --tags 2>/dev/null || echo "Unknown")
git_commit := $(shell git rev-parse --short HEAD || echo "Unknown")

# Get current date
current_date := $(shell date --utc +"%Y-%m-%d:T%H:%M:%SZ")

# Linker flags
ld_flags = '-X "github.com/0h41/pulsekontrol/src.commit=${git_commit}" \
	-X "github.com/0h41/pulsekontrol/src.version=${git_tag}" \
	-X "github.com/0h41/pulsekontrol/src.buildTime=${current_date}"'

.PHONY: all pulsekontrol clean distclean

all: pulsekontrol

pulsekontrol:
	go mod vendor
	patch -p 0 < go-midi.patch
	go build -ldflags=${ld_flags}
	strip $@

clean:
	rm pulsekontrol

distclean: clean
	rm -rf vendor
