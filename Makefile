MODULE_DIR := ai-studio-cli
BINARY     := ai-studio-cli

# Build into bin/, NOT the repo root.
#
# The module lives in a directory with the same name as the binary, so
# `go build -o ../ai-studio-cli` from inside it resolves to the module directory
# itself. `-o` pointing at an existing directory makes Go write the executable
# *inside* it — so the binary landed at ai-studio-cli/ai-studio-cli, and
# `./ai-studio-cli` at the repo root was still the directory. Running it gave
# "Is a directory", exit 126, which make reports as exit 2. That was the real
# cause of the CI build job failing, not the licence notices.
BIN_DIR     := bin
BINARY_PATH := $(BIN_DIR)/$(BINARY)

.PHONY: build build-release run test fmt vet notices vendor-ui compliance clean

# ── Build ────────────────────────────────────────────────────────────────────

build:
	@mkdir -p $(BIN_DIR)
	cd $(MODULE_DIR) && go build -o ../$(BINARY_PATH) .
	@echo "Built ./$(BINARY_PATH)"
	@test -f $(BINARY_PATH) || (echo "FAIL: $(BINARY_PATH) is not a regular file" && exit 1)

run:
	cd $(MODULE_DIR) && go run .

test:
	cd $(MODULE_DIR) && go test ./...

fmt:
	cd $(MODULE_DIR) && gofmt -w .

vet:
	cd $(MODULE_DIR) && go vet ./...

# ── Licence compliance ───────────────────────────────────────────────────────
#
# A Go binary statically links its dependencies, so shipping a release binary
# means distributing their code. The notices have to travel inside the binary —
# there is nothing else alongside it. See scripts/generate-notices.sh.
#
# THE NOTICES ARE GENERATED, NOT COMMITTED.
#
# The first version of this treated the generated inventory as a committed
# artifact with a CI drift check. That was wrong twice over: it needed network
# access and a Go toolchain just to produce a reviewable branch, and it put a
# regenerate-and-commit step on the critical path of every dependency bump —
# a step whose only failure mode is a red build with a misleading message.
#
# What actually has to be true is narrower: no RELEASED binary may ship without
# its notices. So generation happens in CI and in the release flow, where the
# network is, and `make build-release` is the gate. A locally built binary may
# carry the placeholder; `ai-studio-cli licenses` says so plainly rather than
# printing an empty page.

notices:
	./scripts/generate-notices.sh

# Build with real notices embedded. What CI and the release workflow run.
#
# The steps are sequenced inside the recipe rather than declared as
# prerequisites (`build-release: notices build`). Prerequisites may run in
# parallel under `make -j`, which would race the build against the generator and
# could embed the placeholder in a binary that then passes the check by luck.
# Order matters here, so it is made explicit.
build-release:
	$(MAKE) notices
	$(MAKE) build
	@echo
	@./$(BINARY_PATH) licenses > /dev/null 2>&1 \
		|| (echo "FAIL: the built binary cannot print its notices." \
		    && echo "      Expected 'make notices' to have replaced the placeholder." \
		    && exit 1)
	@echo "Built ./$(BINARY_PATH) with $$(./$(BINARY_PATH) licenses | grep -ci copyright) embedded copyright notices."

vendor-ui:
	@# Fonts and Chart.js for the embedded bench UI. Committed, not fetched at
	@# build time: `go build` cannot run npm, and go:embed needs the files
	@# present in the source tree.
	./scripts/vendor-ui-assets.sh

compliance:
	@echo "── LICENSE ─────────────────────────────────────────────────────────"
	@test $$(wc -c < LICENSE) -ge 10000 \
		|| (echo "  FAIL: LICENSE is only $$(wc -c < LICENSE) bytes — not the full Apache-2.0 text" && exit 1)
	@grep -aq "3. Grant of Patent License" LICENSE || (echo "  FAIL: LICENSE missing section 3" && exit 1)
	@# `grep && (echo; exit 1) || true` swallows the failure — the || catches the
	@# subshell's own exit. Use if/then/fi so the recipe actually fails.
	@if grep -aq "name of copyright owner" LICENSE; then \
		echo "  FAIL — LICENSE still has the placeholder copyright line"; exit 1; \
	fi
	@echo "  ok — $$(wc -c < LICENSE) bytes, sections present, copyright filled in"
	@echo "── Required files ──────────────────────────────────────────────────"
	@for f in NOTICE $(MODULE_DIR)/internal/benchui/ui/vendor/NOTICE \
	          $(MODULE_DIR)/internal/notices/THIRD-PARTY-NOTICES.txt; do \
		test -f $$f && echo "  ok — $$f" || (echo "  FAIL — missing $$f" && exit 1); \
	done
	@echo "── Embedded notices ────────────────────────────────────────────────"
	@# Informational here, fatal in `make build-release`. A checkout carrying
	@# the placeholder is the normal state — generating requires network and a
	@# Go toolchain, and demanding that of everyone who wants to run the static
	@# checks buys nothing. What must never happen is a RELEASE shipping it, and
	@# that is enforced where releases are built.
	@if grep -q "NOTICES-NOT-GENERATED" $(MODULE_DIR)/internal/notices/THIRD-PARTY-NOTICES.txt; then \
		echo "  placeholder present — normal for a fresh checkout."; \
		echo "    Real notices are generated by 'make notices', which CI and the"; \
		echo "    release workflow run. 'make build-release' fails without them."; \
	else \
		echo "  ok — generated notices are present ($$(grep -ci copyright $(MODULE_DIR)/internal/notices/THIRD-PARTY-NOTICES.txt) copyright lines)"; \
	fi
	@echo "── Bench UI makes no third-party requests ──────────────────────────"
	@# CDN assets break on air-gapped GPU nodes — the target deployment — and
	@# Google Fonts discloses the operator's IP.
	@#
	@# Matches only real loading positions (src=/href= followed by an absolute
	@# URL), not any mention of a hostname. A bare hostname grep flags the
	@# comment in index.html that explains why the CDN links were removed, which
	@# is a good way to get the check deleted.
	@if grep -rnE '(src|href)[[:space:]]*=[[:space:]]*["'"'"']https?://' \
		$(MODULE_DIR)/internal/benchui/ui/index.html \
		$(MODULE_DIR)/internal/benchui/ui/index.css \
		$(MODULE_DIR)/internal/benchui/ui/app.js 2>/dev/null; then \
		echo "  FAIL — bench UI loads an asset from an absolute URL (see above)"; exit 1; \
	fi
	@# CSS url() and @import, which have no src=/href= prefix.
	@if grep -rnE '(url\(|@import)[[:space:]]*["'"'"']?https?://' \
		$(MODULE_DIR)/internal/benchui/ui/index.css 2>/dev/null; then \
		echo "  FAIL — bench UI CSS loads from an absolute URL (see above)"; exit 1; \
	fi
	@echo "  ok — no third-party asset references"
	@echo "── Vendored UI assets present ──────────────────────────────────────"
	@test $$(ls $(MODULE_DIR)/internal/benchui/ui/vendor/fonts/*.woff2 2>/dev/null | wc -l) -eq 6 \
		|| (echo "  FAIL — expected 6 vendored fonts; run 'make vendor-ui'" && exit 1)
	@test -f $(MODULE_DIR)/internal/benchui/ui/vendor/js/chart.umd.js \
		|| (echo "  FAIL — Chart.js missing; run 'make vendor-ui'" && exit 1)
	@echo "  ok — fonts and Chart.js vendored with their licences"
	@echo ""
	@echo "Compliance checks passed."
	@echo "Release gate (needs network + Go): 'make build-release'."

clean:
	rm -rf $(BIN_DIR)
	cd $(MODULE_DIR) && go clean
