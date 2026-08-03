# Licence review — corespan/aistudio-cli

**Repo:** `corespan/aistudio-cli`
**Reviewed:** 2 August 2026
**Author:** ManojDev
**Context:** third in the series, after the 31 July review of `aistudio-server`
and the 2 August audit of `aistudio-app`.

---

## Summary

Seven findings. Two blockers, two high, two medium, one low.

**This is the strictest distribution model of the three.** `aistudio-server`
conveys container images and the review had to argue about whether we ship
prebuilt artifacts. `aistudio-app` serves a JS bundle. This ships a **compiled
Go binary that statically links every dependency**.

That distinction is the whole story. There is no `node_modules`, no
`site-packages`, no image layer beside the artifact for third-party notices to
live in. The binary *is* the distribution. If the notices are not inside it,
they are not accompanying anything — and they were not.

| | Count | Findings |
| --- | --- | --- |
| ✅ Fixed | 6 | C1, C2, C3, C4, C5, C6 |
| 🔵 Open — product decision | 1 | C7 |

### A correction to the first version of this fix

The first attempt treated the generated licence inventory as a **committed**
artifact, with a CI job diffing the committed copy against a fresh
`go-licenses` run. Three CI jobs went red, and the diagnosis was more
interesting than "unfinished work": the design was wrong.

- The environment this work was done in cannot reach `proxy.golang.org`,
  `go.dev` or GitHub — only the npm registry. So the inventory could not be
  generated, and a *reviewable branch could not exist* without a Go toolchain
  and network access. That is a bad property for a compliance artifact.
- Worse, it put a regenerate-and-commit step on the critical path of **every
  dependency bump**, whose only failure mode is a red build with a message that
  reads like a mistake rather than a routine step.

The obligation is narrower than the design assumed. Nothing requires the
inventory to live in git. What must be true is that **no released binary ships
without its notices**. So generation now happens where the network is — in CI
and in the release flow — and `make build-release` is the gate: it generates,
builds, and fails if the resulting binary cannot print them.

A fresh checkout embeds a placeholder, and `ai-studio-cli licenses` says so
plainly instead of printing an empty page that reads like "no dependencies".
`make compliance` reports it as the normal state rather than an error, so the
static checks run anywhere with no toolchain.

I deliberately did not hand-write the inventory to paper over the gap. I know
roughly what cobra and viper are licensed under, but writing licence facts I
could not verify into a compliance document is precisely what makes such
documents worthless.

---

## C1. A statically linked binary that carries no attribution

**Blocker · Licence · largest exposure**

**Technical.** `go build` links every module in the build graph into a single
executable. Releases publish that executable. The graph includes cobra
(Apache-2.0), viper, pflag, fsnotify, afero, cast, mapstructure, go-toml,
locafero, conc, gotenv, mousetrap, `golang.org/x/{term,sys,text}` and
`yaml.v3` — a mix of MIT, BSD-3 and Apache-2.0, all of which condition
redistribution on carrying the copyright notice. Apache-2.0 §4(d) additionally
requires reproducing any NOTICE file.

There was no inventory, no notices, and no way for a recipient of the binary to
discover what was in it.

**Plain.** When you hand someone the `ai-studio-cli` binary you are handing
them a dozen other projects' code, welded into one file. Their licences all say
the same easy thing: keep our name on it. Nothing did.

**Fix.** The idiomatic Go answer, and the one kubectl, docker and gh all use —
compile the notices in and add a subcommand:

```
ai-studio-cli licenses           # everything embedded in this binary
ai-studio-cli licenses cobra     # filter to one dependency
```

- `internal/notices` holds the text, embedded with `go:embed`. Its own package,
  so `go build` fails loudly if the file is deleted rather than silently
  producing an unattributed binary.
- `scripts/generate-notices.sh` runs `go-licenses` over the **real build
  graph** — only what is actually linked, not everything in `go.sum`, which
  also lists test and tooling modules that never reach a user.
- The script appends the embedded web UI's notices too. Those are not Go
  modules, so `go-licenses` cannot see them, but they are equally compiled in.
- The release workflow publishes the same text as a release asset, so it can be
  read without running the binary, and puts `LICENSE`, `NOTICE` and the notices
  inside the tarball alongside the executable.

This survives the case that matters: someone copies the binary onto an isolated
GPU node with no network and no checkout. The notices go with it.

**Generated, not committed.** See the correction in the summary. `make notices`
runs in CI and in the release flow, where the network is; `make build-release`
generates, builds and fails if the binary cannot print its notices. A fresh
checkout carries a placeholder, which `ai-studio-cli licenses` reports rather
than printing an empty page.

Nothing needs to be remembered on a dependency bump: the next CI run regenerates
against the new module graph automatically. The guard that matters — a release
never shipping without attribution — sits in the release workflow, which is the
only place it can actually be enforced.

---

## C2. Bench UI loads fonts from Google

**High · Privacy + Function**

**Technical.** `internal/benchui/ui/index.html` loaded Inter and JetBrains Mono
from `fonts.googleapis.com`. Same finding as `aistudio-server` #9, but the
consequence is worse here and the irony is sharper: this UI is compiled into the
binary with `go:embed` specifically so it needs no external files — and then
phoned out for fonts on every page load.

`ai-studio-cli bench-ui` runs **on GPU nodes**, which are routinely air-gapped,
and serves the dashboard on localhost. The CDN reference does not degrade there,
it simply fails.

The privacy half also applies: LG München I, 3 O 17493/20 (20 Jan 2022) held
that disclosing a visitor's IP to Google via a font request is a GDPR breach
absent consent. Here the disclosed IP is the operator's own machine.

**Plain.** You embedded the dashboard into the binary so it would work
anywhere, then had it download fonts from Google — which fails on exactly the
isolated machines this tool is built for.

**Fix.** `scripts/vendor-ui-assets.sh` fetches Inter and JetBrains Mono from
npm, copies their OFL-1.1 licence texts alongside, generates the `@font-face`
CSS, and writes `vendor/NOTICE`. Latin subset, only the four Inter and two
JetBrains weights `index.css` actually references.

Unlike `aistudio-app`, these binaries **are committed**. `go build` cannot run
npm and `go:embed` needs the files present at compile time — making a working
build depend on a prior npm run would break `go install` for everyone.

CI regenerates into a scratch directory and diffs, so a hand-edited asset is
caught, and asserts the compiled binary contains no `fonts.googleapis.com`
string.

---

## C3. Vendored Chart.js with a banner instead of a licence

**High · Licence**

**Technical.** `internal/benchui/ui/chart.umd.min.js` was a 205 KB copy fetched
from jsdelivr, carrying only:

```
/*!
 * Chart.js v4.4.7 ... (c) 2024 Chart.js Contributors
 * Released under the MIT License
 */
```

A banner naming a licence is not the licence. MIT requires *"the above
copyright notice **and this permission notice**"* to be included; the
permission notice — the paragraph granting the rights — was absent. There was
no `LICENSE` file anywhere near it.

Provenance was also a CDN rather than a package registry, with jsdelivr's own
"Do NOT use SRI with dynamically generated files" warning still embedded in the
header.

**Plain.** The copy of Chart.js in your repo says "MIT" at the top but doesn't
include the actual licence, which is what MIT asks you to include.

**Fix.** Replaced with the npm `chart.js@4.4.7` artifact — verified
byte-identical to the vendored copy apart from the jsdelivr banner, so this is
provably the same code from a canonical source — with `LICENSE-chartjs.md`
beside it and an entry in `vendor/NOTICE`.

---

## C4. install.sh: unpinned, unverified, `sudo mv`

**Blocker · Supply chain**

**Technical.** The installer resolved `releases/latest` at run time, downloaded
a tarball over curl, and `sudo mv`d the contents into `/usr/local/bin`. No
checksum, no signature, no version pinning, no verification that the tarball
even contained the expected binary. It extracted into `/tmp` under fixed names,
so concurrent runs clobbered each other and failures left files behind.

This is `aistudio-server` finding 2, but sharper on two counts. That installer
at least pinned a tag; this one installed *whatever is newest at this instant*,
so it could not be used in a reproducible provisioning flow — which is what
this tool is for. And the payload here goes into `/usr/local/bin` with sudo,
rather than into a docker-compose directory.

**Plain.** `curl | sudo` with nothing checked. Anything that could interfere
with the download — a compromised release asset, a proxy, a bad redirect —
resulted in an unverified binary being installed with root privileges. And two
people running the same command a week apart got different software with no way
to tell.

**Fix.**

- Accepts a version: `./install.sh v1.2.3`. Defaults to latest, but resolves it
  once, prints it, and tells you how to pin it.
- Downloads and verifies `SHA256SUMS`; a mismatch aborts with a report link.
  Checks a detached GPG signature when present.
- Private `mktemp -d` with a cleanup trap.
- Confirms the tarball actually contains the binary before installing.
- Skips sudo entirely when `$INSTALL_DIR` is writable, and says so when it does
  need it.
- `set -euo pipefail` instead of bare `set -e`.

The missing-checksum path is a warning rather than a hard failure only because
existing releases predate `SHA256SUMS`. Once every supported release publishes
one, make it fatal — there is a comment in the script marking the spot.

`.github/workflows/release.yml` now produces what the installer expects:
refuses to build from a lightweight tag, gates on compliance, verifies the
built binary prints real notices before publishing, and attaches
`SHA256SUMS` plus the notices as assets.

---

## C5. LICENSE copyright placeholder never filled in

**Medium · Licence**

**Technical.** `LICENSE` is the complete Apache-2.0 text (11,558 bytes), so
detection and scanning work — unlike `aistudio-server` finding 1. But the
appendix still read `Copyright [yyyy] [name of copyright owner]`, the template
text meant to be replaced. Identical to `aistudio-app` finding A2.

The file is CRLF-encoded, which is why the obvious `sed` fixes elsewhere in this
series did not apply; the replacement preserves the existing line endings rather
than reformatting the licence text and burying the one-line change in a
whole-file diff.

**Fix.** `Copyright 2026 CoreSpan AI`. `NOTICE` added, covering the copyright,
the embedded third-party software, the trademark statement, and — specific to
this repo — a section distinguishing what the CLI *distributes* from what it
merely *installs on your behalf*. See C6.

`make compliance` fails if the placeholder returns.

---

## C6. No statement distinguishing distributed from installed software

**Medium · Licence · specific to this repo**

**Technical.** `ai-studio-cli` provisions NVIDIA drivers and the CUDA userspace,
Docker or Podman, vLLM, and pulls container images at run time
(`internal/provision/assets/driversInstallation.sh`,
`installVllmDeps.sh`, `cmd/vllm.go`). Nothing said whose terms govern that.

The distinction matters and cuts in our favour, which is exactly why it should
be written down. CoreSpan does not convey any of it: the operator downloads it,
from the publisher, onto their own machine. The NVIDIA CUDA EULA question that
is the largest open item for `aistudio-server` — where we *do* build and
distribute images containing CUDA — **does not arise here**, because this tool
only automates the operator fetching it themselves.

Absent a statement, a reader could reasonably assume our Apache-2.0 grant
extends to the drivers the tool installs. It does not, and neither does any
warranty.

**Fix.** A dedicated section in `NOTICE` and in the README's Licensing section,
stating plainly what is distributed, what is merely installed, and that
accepting the NVIDIA terms is between the operator and NVIDIA.

---

## C7. "AI Studio" name collision

**Low · Branding — unchanged across all three repos**

Same as `aistudio-server` #10 and `aistudio-app` A6. Apache-2.0 §6 grants no
trademark rights; "AI Studio" collides with Google AI Studio and Azure AI
Studio.

One consideration this repo adds: the binary name `ai-studio-cli` is what
appears in `/usr/local/bin`, in shell history, in provisioning scripts and in
customer runbooks. Of the three surfaces, this is the one with the most
inertia — renaming a deployed CLI means every operator's muscle memory and
every automation script that references it. If a rename is coming, it is
cheapest here first, not last.

Trademark statement added to `NOTICE`. Clearance search still the suggested
next step.

---

## What did not apply

| Server finding | Status here |
| --- | --- |
| 1 — LICENSE not the Apache text | Not applicable; text is complete (C5 is the placeholder only). |
| 3 — "open source" vs private gate | Not applicable. No gated registry; the CLI pulls public images and public drivers. |
| 5 — model licences | Not applicable directly. The CLI runs vLLM against models the operator supplies; C6 covers the general statement. |
| 6 — container image inventory | **Does not arise.** We build no images here. The CUDA EULA question that blocks aistudio-server has no counterpart. |
| 7 — unpinned dependencies | **Already satisfied.** `go.sum` pins every module by cryptographic hash — a stronger guarantee than either of the other two repos had. `go.mod` also pins the toolchain at 1.25.0. Nothing to fix. |
| 8 — repo dependency inventory | Superseded by C1, which is the same obligation but harder, because the artifact is a linked binary rather than a source tree the user builds. |

---

## What CI now enforces

| Check | Catches |
| --- | --- |
| LICENSE complete, no placeholder copyright | C5 recurring |
| NOTICE, vendor NOTICE, embedded notices present | C1, C2, C3 |
| Embedded notices are not the placeholder | C1 — a binary shipping no attribution |
| `make build-release` — generates notices, builds, fails if the binary cannot print them | C1: a release shipping no attribution |
| **Built binary**: `licenses` prints 10+ notices, incl. OFL and Chart.js | C1, C2, C3 — the check that actually matters |
| **Compiled binary** contains no `fonts.googleapis.com` string | C2, asserted against the artifact not the source |
| No `src=`/`href=`/`url()` absolute URLs in the bench UI | C2 creeping back |
| Vendored assets reproduce from a clean run | Hand-edited binaries |
| Every font `fonts.css` references exists | A weight shipping as a rule with no file |
| Release: tag must be annotated; compliance gate before publish | C4 |

The two build-output checks are the substantive ones. Everything else confirms
files exist in a repository; only those confirm the notices are inside the
artifact a user receives.

---

## Open items

| # | Item | Owner |
| --- | --- | --- |
| C4 | Cut a release through the new workflow so `SHA256SUMS` exists; then make the missing-checksum branch fatal in install.sh | Engineering |
| C4 | Generate a CoreSpan signing key and sign `SHA256SUMS` | Engineering |
| C7 | Trademark clearance on "AI Studio" — cheapest to rename here first | Product + Counsel |
| — | Mixed line endings: 9 of 33 tracked files are CRLF, including `.go` files, which `gofmt` will fight. Worth one normalisation commit of its own. | Engineering |

---

## Files added or changed

**Added**

```
NOTICE                                              Copyright, embedded software, installed-vs-distributed, trademarks
.gitattributes                                      Scoped LF rules; binary assets protected
Makefile                                            build/test/vet plus notices, vendor-ui, compliance
docs/LICENCE-REVIEW-RESPONSE.md                     This document
.github/workflows/compliance.yml                    CI enforcement
.github/workflows/release.yml                       Signed, checksummed, notice-gated releases
scripts/generate-notices.sh                         go-licenses over the real build graph
scripts/vendor-ui-assets.sh                         Fonts + Chart.js with licences
ai-studio-cli/cmd/licenses.go                       `ai-studio-cli licenses`
ai-studio-cli/internal/notices/notices.go           go:embed carrier
ai-studio-cli/internal/notices/THIRD-PARTY-NOTICES.txt   Placeholder — run `make notices`
ai-studio-cli/internal/benchui/ui/vendor/           Fonts, Chart.js, licences, NOTICE
```

**Changed**

```
LICENSE                                     Copyright placeholder → CoreSpan AI (CRLF preserved)
README.md                                   Licensing section
install.sh                                  Version pinning, checksum + signature, private tmp, no blind sudo
ai-studio-cli/internal/benchui/ui/index.html   Google Fonts + local chart.umd.min.js → vendor/
```

**Removed**

```
ai-studio-cli/internal/benchui/ui/chart.umd.min.js   jsdelivr copy, replaced by vendor/js/chart.umd.js + licence
```

---

## Across the three repos

Same underlying obligation, three different answers, because the artifact
differs each time:

| | Artifact | Where notices must live |
| --- | --- | --- |
| `aistudio-server` | Container images + source | SBOM per image; `THIRD-PARTY-NOTICES.md` in repo |
| `aistudio-app` | JS bundle served to browsers | `third-party-licences.txt` served with the app, linked from the footer |
| `aistudio-cli` | Statically linked binary | Compiled into the binary; `ai-studio-cli licenses` |

The recurring lesson is that "we have a LICENSE file" answers a different
question from "does the attribution reach the person receiving the software".
In all three cases the second answer was no, and in all three the fix was to
put the notices where the artifact goes rather than where the source lives.
