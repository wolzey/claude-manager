REPO_DIR     := $(shell pwd)
PLUGIN_DIR   := $(REPO_DIR)/plugins/claude-manager
BIN          := $(PLUGIN_DIR)/bin/cmgr
SHIM_BIN     := $(HOME)/.local/bin/cmgr
MARKETPLACE  := wolzey
PLUGIN_NAME  := claude-manager

.PHONY: build install install-plugin uninstall-plugin clean test help

help:
	@echo "claude-manager — build / install targets:"
	@echo "  make build              build the cmgr binary into plugins/claude-manager/bin/"
	@echo "  make install-plugin     build, register the marketplace, install the plugin,"
	@echo "                          symlink cmgr onto PATH, allowlist Bash(cmgr *)"
	@echo "  make uninstall-plugin   uninstall the plugin and remove the marketplace"
	@echo "  make install            alias for install-plugin"
	@echo "  make clean              rm -rf the built binary"

build:
	@mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/cmgr
	@echo "built: $(BIN)"

install: install-plugin

install-plugin: build
	@# 1. register marketplace (idempotent — `add` errors quietly if present)
	@if claude plugin marketplace list 2>/dev/null | grep -q "^$(MARKETPLACE)\b"; then \
		echo "marketplace $(MARKETPLACE) already registered"; \
	else \
		claude plugin marketplace add $(REPO_DIR); \
	fi
	@# 2. install plugin from the local marketplace
	@if claude plugin list 2>/dev/null | grep -q "$(PLUGIN_NAME)@$(MARKETPLACE)"; then \
		echo "plugin $(PLUGIN_NAME)@$(MARKETPLACE) already installed"; \
	else \
		claude plugin install $(PLUGIN_NAME)@$(MARKETPLACE); \
	fi
	@# 3. shim onto PATH
	@mkdir -p $(dir $(SHIM_BIN))
	@ln -sf $(BIN) $(SHIM_BIN)
	@echo "shim:    $(SHIM_BIN) → $(BIN)"
	@# 4. allowlist Bash(cmgr *)
	@python3 scripts/allow-cmgr.py
	@echo
	@echo "→ restart your interactive \`claude\` session to load the plugin."

uninstall-plugin:
	@claude plugin uninstall $(PLUGIN_NAME)@$(MARKETPLACE) || true
	@claude plugin marketplace remove $(MARKETPLACE) || true
	@[ -L "$(SHIM_BIN)" ] && rm -f "$(SHIM_BIN)" || true
	@python3 scripts/allow-cmgr.py --remove || true
	@echo "uninstalled."

clean:
	rm -f $(BIN)

test:
	go vet ./...
	go build ./...
