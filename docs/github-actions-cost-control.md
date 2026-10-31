# GitHub Actions: $0 Cost Control

This document is the operating contract for CI in this repository. It explains
what was changed to keep GitHub Actions at **$0**, what is enforced
automatically, and the one account-level switch that no workflow file can set.

---

## 1. The short version

| Layer | Control | Who enforces it |
|---|---|---|
| Account | Spending limit **$0** (and/or no payment method) | Repository owner, GitHub UI |
| Repository | Public visibility, standard `ubuntu-latest` runners | Repo settings |
| Workflow | No macOS/Windows/larger runners, bounded timeouts, no cron | `cost-guard` job |
| Workflow | One runner per cross-compile instead of a 5-runner matrix | `ci.yml` |
| Workflow | PR Docker builds are single-arch, no push, no QEMU | `docker.yml` |
| Workflow | Superseded runs are cancelled | `concurrency` |
| Workflow | Docs-only pushes skip CI entirely | `paths-ignore` |
| Workflow | No paid third-party service is required | Codecov is best-effort |

**On a public repository with standard GitHub-hosted runners, Actions minutes
and artifact storage are free.** The changes below remove every path that
could turn this into billable usage if the repository is ever made private.

---

## 2. The hard stop you must enable once (not in YAML)

GitHub bills nothing unless the account is allowed to spend. Set the cap:

1. **Personal account:** GitHub → *Settings* → *Billing and plans* →
   *Spending limits* → set **Actions and Packages** to **$0**.
2. **Organization:** *Organization settings* → *Billing* → *Spending limits* →
   **$0** for Actions and Packages.
3. Optional but strongest: keep **no payment method** on the account. Without
   one GitHub cannot charge for overages at all.

With a $0 limit, once included minutes/storage are exhausted, GitHub
**stops** the jobs instead of invoicing you. That is the financial backstop;
the workflow changes below ensure you never get near it.

Keep the repository **public**. That is what makes standard-runner minutes and
public package storage free.

---

## 3. What the workflows now enforce

### 3.1 `cost-guard` job (`.github/scripts/cost-guard.rb`)

Runs on every CI invocation and fails the build if a workflow:

- uses anything other than `ubuntu-latest` (or `self-hosted`) — this blocks
  `macos-*` (10× minutes), `windows-*` (2× minutes), and larger runners;
- declares a `schedule:` trigger — cron jobs spend minutes unattended;
- uses `pull_request_target` — fork code would run with secrets;
- requests `permissions: write-all`;
- has a job without `timeout-minutes` (a hung job bills until the 6-hour cap);
- sets a timeout above 30 minutes;
- installs tooling with `@latest`, `@master`, or `@main`.

Run it locally before pushing:

```bash
ruby .github/scripts/cost-guard.rb
```

### 3.2 `ci.yml`

- `permissions: contents: read` for the whole workflow.
- `concurrency` with `cancel-in-progress: true`: pushing three commits to a PR
  runs one pipeline, not three.
- `paths-ignore` on `push`: markdown/docs-only merges do not run the pipeline.
  Pull requests are intentionally **not** path-filtered so required checks
  always report and never leave a PR stuck.
- `timeout-minutes` on every job (5–20).
- The five-target build matrix was replaced by **one** runner that
  cross-compiles all targets. This removes four runner startups plus all CI
  artifact uploads; release binaries are still produced by `release.yml`.
- `go test` already compiles benchmark packages, so the two separate benchmark
  build steps were removed.
- Codecov upload runs only on pushes, is `continue-on-error`, and cannot fail
  the build. No Codecov token is required for the public repository.
- `gosec` and `govulncheck` are pinned to exact versions instead of installed
  with `@latest`, so their runtime and output are reproducible.
- `build` runs only after `test` and `lint` pass, so broken code does not
  consume build minutes.

### 3.3 `docker.yml`

- Pull requests build **linux/amd64 only**, do not start QEMU, and do not push.
  QEMU-emulated `arm64` builds were the most expensive part of the workflow.
- Multi-architecture builds (`linux/amd64,linux/arm64`) and registry pushes
  happen only on `main` and `v*` tags.
- The integration test no longer `needs` the image-build job. It builds its own
  Compose images, so the previous dependency only serialized the two jobs and
  doubled wall-clock time.
- `permissions` is read-only except `packages: write` on the publishing job.
- Buildx cache is scoped per service so `api` and `proxy` do not evict each
  other's cache.
- Cache writes use `mode=min` on PRs and `mode=max` only on publish runs.

### 3.4 `release.yml`

- The deprecated `actions/cache@v3` step was removed; `actions/setup-go`
  already caches modules and build output.
- One job cross-compiles every release target, with `CGO_ENABLED=0` and
  `-trimpath` for reproducible binaries.
- Hard 20-minute timeout; concurrency never cancels a release mid-upload.

### 3.5 `dependabot.yml`

Dependabot itself is free, but every PR it opens triggers CI + Docker. Updates
are now grouped (Go minor/patch, Go major, Docker, Actions minor/patch, Actions
major) with lower PR limits, so weekly maintenance is roughly 3 pipeline runs
instead of up to 10.

---

## 4. Estimated minute usage

These are estimates for a typical PR push; check
*Settings → Billing → Actions* for the authoritative number.

| Workflow | Before | After |
|---|---|---|
| CI (`test`, `lint`, `build`, `security`) | ~12–21 min | ~9–12 min |
| Docker (2 services + integration) | ~14–26 min | ~8–11 min |
| **Per push to a PR** | **~26–47 min** | **~17–23 min** |

Additional savings not visible in that table: cancelled superseded runs,
docs-only pushes skipping both workflows, no CI artifact storage, and fewer
Dependabot-triggered pipelines.

On a public repository all of the above bills at **$0**. If the repository is
made private, the Free plan includes 2,000 minutes/month; the optimized
pipeline leaves substantial headroom, and the $0 spending limit still blocks
overage.

---

## 5. Things that are *not* free (deliberately avoided)

| Service | Status here |
|---|---|
| macOS runners | Blocked by `cost-guard` (10× minute multiplier) |
| Windows runners | Blocked by `cost-guard` (2× minute multiplier) |
| Larger runners | Blocked by `cost-guard` |
| Scheduled/cron workflows | Blocked by `cost-guard` |
| Private GHCR packages | Not used for private repos; public images are free |
| Paid coverage/SAST SaaS | Not used; Codecov is optional and free for OSS |
| Paid marketplace actions | Not used |

### If the repository becomes private

1. Set the spending limit to **$0** (section 2) *before* changing visibility.
2. GHCR storage/transfer for **private** packages becomes billable. Either keep
   packages public or disable the Docker push for private builds.
3. Run `ruby .github/scripts/cost-guard.rb` and confirm the CI pipeline still
   fits within the 2,000 included minutes.
4. For guaranteed $0 under heavy load, switch `runs-on` to a
   `self-hosted` runner. `cost-guard` permits that label; your own hardware is
   then the only cost.

---

## 6. Honest limitations

- No workflow file can force an account-level spending cap. If an owner raises
  the limit or enables a paid plan, billing becomes possible. The `$0` spending
  limit is the only true financial guarantee.
- Public-repository free usage is GitHub's current policy, not a contract; it
  can change. The guardrails above still keep usage small if it does.
- `self-hosted` runners are free to GitHub but cost whatever your own machine
  costs; they are excluded from the "free runner" claim.
- Third-party action versions are pinned to major tags and bumped by
  Dependabot. Review those PRs; a malicious action could abuse runner time.

## 7. Change checklist for contributors

Before opening a PR that touches `.github/`:

- [ ] `ruby .github/scripts/cost-guard.rb` passes.
- [ ] No new `schedule:` trigger.
- [ ] No `macos-*`, `windows-*`, or larger-runner labels.
- [ ] Every job has `timeout-minutes` ≤ 30.
- [ ] New tool installs are pinned to exact versions.
- [ ] New artifacts have a short `retention-days`.
- [ ] `permissions` grants only what the job needs.
