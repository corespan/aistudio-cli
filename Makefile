MODULE_DIR := ai-studio-cli
BINARY     := ai-studio-cli

.PHONY: build run test fmt vet notices notices-check vendor-ui compliance clean

# ── Build ────────────────────────────────────────────────────────────────────

build:
	cd $(MODULE_DIR) && go build -o ../$(BINARY) .
	@echo "Built ./$(BINARY)"

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

notices:
	./scripts/generate-notices.sh

notices-check:
	./scripts/generate-notices.sh --check

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
	@echo "── Embedded notices are real ───────────────────────────────────────"
	@# A binary shipping the placeholder ships no attribution at all. For a
	@# statically linked artifact this is the check that matters most.
	@if grep -q "NOTICES-NOT-GENERATED" $(MODULE_DIR)/internal/notices/THIRD-PARTY-NOTICES.txt; then \
		echo "  FAIL — placeholder still embedded. Run 'make notices'."; exit 1; \
	fi
	@echo "  ok — generated notices are embedded"
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
	@echo "Not covered here (needs network + Go): 'make notices-check'."

clean:
	rm -f $(BINARY)
	cd $(MODULE_DIR) && go clean
