PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
BINARY = qsg

.PHONY: build install uninstall clean

build:
	cargo build --release

install:
	install -Dm755 target/release/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

clean:
	cargo clean
