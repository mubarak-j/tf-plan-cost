# Contributing

Thanks for helping. Bug reports, fixes and new resource pricers are all welcome. For a larger change, open an issue first so we can agree on the approach.

## Setup

You need Go 1.27+. Terraform 1.6+ is only needed for the examples and deploy configs.

```sh
git clone https://github.com/mubarak-j/tf-plan-cost && cd tf-plan-cost
go test -race ./...    # unit, golden and end-to-end tests; no AWS or GitHub access needed
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
```

The tests run offline:
- AWS prices are replayed from `testdata/prices.json`.
- GitHub is faked by `internal/github/githubtest`.
- GitHub Actions OIDC tokens come from a fake issuer in `internal/app/oidctest`.

## Layout

| Path | What it does |
|---|---|
| `cmd/tf-plan-cost` | CLI commands: `diff`, `github-comment`, `strip`, `upload`, `serve` |
| `internal/plan` | Parses `terraform show -json` and resolves regions; `Strip` removes secrets before upload |
| `internal/pricing` | Price client, cache and the estimator that prices both sides of each change |
| `internal/pricing/resources` | One pricer per resource type |
| `internal/render` | Markdown comment and JSON report |
| `internal/github` | PR comments, Check Runs, GitHub App authentication |
| `internal/app` | App server: webhooks, plan uploads, OIDC verification |
| `action.yml`, `action/` | The GitHub Action and its release-binary download |
| `deploy/aws` | Terraform for hosting the App on Lambda |
| `examples/` | Workflow, OIDC role, config, and a demo stack |

## Adding a resource type

1. **Write a pricer** in `internal/pricing/resources`: a `func(ctx, pricing.Resource, pricing.PriceClient) (*pricing.Estimate, error)`. Then register it in `Registry()` in `resources.go`. The existing pricers (`ec2.go`, `rds.go`, `network.go`, `eks.go`) show the patterns.
2. **Follow the house rules:**
   - **Exact queries only.** Build Price List filters that match exactly one on-demand price. If a value is unknown until apply, or the match is ambiguous, return `pricing.Unpriced("reason ...")`. Never guess.
   - **Fixed charges only.** Price hourly or provisioned charges at 730 hours a month. Mention usage-based charges (requests, GB processed) in `Notes`, with their rate when AWS publishes one.
   - **Unset attributes:** fall back to AWS's default only for attributes that qualify a price (the `...Or` helpers on `Attrs`). Attributes that determine the price must be known.
3. **Test it** in `internal/pricing/resources` with the `pricingtest` fake: the queries it makes, the monthly amount, and the cases where it isn't priced.
4. **Add it to a fixture plan** under `testdata/plans/` (or regenerate `create.json` from `testdata/terraform/create`). Then record real prices and update the golden output:
   ```sh
   AWS_PROFILE=... go test -tags integration ./cmd/tf-plan-cost -run TestRecordPrices
   go test ./cmd/tf-plan-cost -update
   ```
   Check the golden diff by hand: the amounts should match the AWS pricing pages.
5. **Update the docs:** the "Supported resources" table in the README.

Recording prices needs AWS credentials allowed to call `pricing:GetProducts`. If you don't have them, open the PR anyway and say so; a maintainer can record them.

## Pull requests

- Keep them focused, with tests for what changes.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat(pricing): ...`, `fix(app): ...`, `docs: ...`). Release notes are built from them.
- CI must pass: tests, lint, Terraform checks, the goreleaser dry run and the end-to-end Action runs. The end-to-end jobs need this repository's AWS role, so they're skipped for pull requests from forks.

## Releases (maintainers)

Push a version tag from `main`:

```sh
git tag -a v1.2.3 -m "tf-plan-cost v1.2.3" && git push origin v1.2.3
```

`.github/workflows/release.yml` builds the Linux binaries, publishes the release with checksums and provenance attestations, and moves `v1`. To deploy the hosted App, run `deploy/aws/build.sh && terraform apply` (see [`deploy/aws`](deploy/aws/README.md)).

## Security issues

Please report them privately; see [SECURITY.md](SECURITY.md).
