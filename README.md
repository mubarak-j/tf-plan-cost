# tf-plan-cost

[![CI](https://github.com/mubarak-j/tf-plan-cost/actions/workflows/ci.yml/badge.svg)](https://github.com/mubarak-j/tf-plan-cost/actions/workflows/ci.yml)

**See what a Terraform change will cost before you merge it.**

`tf-plan-cost` reads a Terraform plan and works out how much the change adds to or removes from your monthly AWS bill, resource by resource. It gets prices from the official [AWS Price List API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/price-changes.html) instead of a hardcoded table. When it can't price something exactly, it says so instead of guessing.

```
$ tf-plan-cost diff plan.json
```

> ### tf-plan-cost: **+$242.24/mo** (was $323.57 → now $565.81)
>
> | Resource | Action | Before | After | Δ/mo |
> |---|---|---|---|---|
> | `aws_eks_node_group.default` | ~ 3 × m6i.large → 5 × m6i.large | $210.24 | $350.40 | +$140.16 |
> | `aws_instance.api[0]` | ~ m6i.large → m6i.xlarge | $72.48 | $142.56 | +$70.08 |
> | `aws_ebs_volume.data` | ~ gp3 100 GB → gp3 500 GB | $8.00 | $40.00 | +$32.00 |

Use it as a **GitHub Action** that comments on pull requests (and can fail when a change costs too much), or as a **CLI**.

> [!NOTE]
> **Status:** the CLI and GitHub Action work. A **GitHub App** mode, which posts a Check Run without AWS credentials in your CI, is next.

## Architecture

```mermaid
flowchart LR
    subgraph ci["Your CI (holds your cloud credentials)"]
        tf["terraform plan -out=tfplan<br/>terraform show -json tfplan > plan.json"]
    end

    subgraph tpc["tf-plan-cost"]
        parse["internal/plan<br/>parse plan JSON,<br/>resolve each resource's region"]
        est["internal/pricing<br/>Estimator: price before and after,<br/>sum totals and delta"]
        res["internal/pricing/resources<br/>one pricer per resource type"]
        cache["Cache<br/>memory + disk, 7-day TTL"]
        render["internal/render<br/>Markdown or JSON"]
    end

    api[("AWS Price List API<br/>pricing:GetProducts")]

    tf -- plan.json --> parse --> est
    est <--> res
    res -- price queries --> cache -- cache miss --> api
    est -- report --> render --> out["PR comment / stdout"]
```

1. **Parse** (`internal/plan`). Reads `terraform show -json` output: every managed resource change with its `before` and `after` values, and which values are unknown until apply. Each resource's region is taken from, in order:
   - its own `region` attribute (AWS provider v6+);
   - its provider configuration (a literal, or a root `var.*`, including provider aliases and modules);
   - the `--region` flag.
2. **Price** (`internal/pricing`). The estimator prices each side of every change (`before` for deletes, `after` for creates, both for updates and replacements). Unchanged resources are priced too, so the totals reflect the whole stack, not just the diff.
3. **Look up** (`internal/pricing/resources`). Each pricer turns resource attributes into exact Price List queries, for example `instanceType=m6i.large, regionCode=us-east-1, operatingSystem=Linux, tenancy=Shared`. A query must match exactly one on-demand price. Zero or several matches mark the resource as *not priced*, never as a guess.
4. **Render** (`internal/render`). Writes a Markdown comment, with a hidden `<!-- tf-plan-cost -->` marker so later runs can update the same PR comment, or JSON for scripts. Amounts are decimal strings to avoid floating-point rounding.

Cloud credentials for `terraform plan` stay in your own CI. `tf-plan-cost` only needs read access to public AWS prices.

## GitHub Action

Add one step after `terraform plan`. It prices the plan and then creates or updates a single comment on the pull request. It also writes the same report to the job summary.

```yaml
permissions:
  contents: read
  pull-requests: write # create/update the cost comment
  id-token: write      # OIDC for AWS credentials

steps:
  # ... checkout, AWS credentials, terraform init/plan ...
  - run: terraform show -json tfplan > plan.json
  - uses: mubarak-j/tf-plan-cost@main # pin to @v1 once released
    with:
      plan: plan.json
```

A complete workflow is in [`examples/workflows/terraform-cost.yml`](examples/workflows/terraform-cost.yml).

**AWS credentials.** The step needs credentials allowed to call `pricing:GetProducts` (see [Usage](#1-give-it-aws-credentials)). [`examples/aws-oidc-role`](examples/aws-oidc-role) is Terraform for an OIDC role limited to that one action, so no keys are stored in GitHub.

**Cost limit.** To fail the step when a pull request adds more than a set amount per month, add `.tf-plan-cost.yml` to the repository root:

```yaml
fail_above_monthly_usd: 500
```

The comment still posts and shows a caution. Decreases never fail. Unknown keys are rejected, so a typo can't silently turn the limit off.

| Input | Default | Description |
|---|---|---|
| `plan` | *(required)* | Path to `terraform show -json` output |
| `region` | `$AWS_REGION` | Region for resources whose region isn't in the plan |
| `comment` | `true` | Create or update the PR comment |
| `comment-id` | `default` | Separate comment per Terraform root, e.g. `prod` |
| `comment-on-no-change` | `false` | Comment even when nothing changes (an existing comment is always updated) |
| `config` | `.tf-plan-cost.yml` | Limit config; a missing file means no limit |
| `github-token` | `${{ github.token }}` | Needs `pull-requests: write` |

| Output | Example |
|---|---|
| `delta-monthly-usd` | `242.24` |
| `total-before-monthly-usd` / `total-after-monthly-usd` | `323.57` / `565.81` |
| `exceeded` | `false` |
| `comment-url` | link to the comment |

**Behavior:**
- **Monorepos:** run the step once per Terraform root with a different `comment-id`. Each root then gets its own comment.
- **Updating:** a later run finds the comment by a hidden marker and edits it. Only comments written by the same token's user are matched, so a person quoting the marker is never edited.
- **Fork PRs and push events:** commenting is skipped with a warning, because the token is read-only or there is no PR. The report still goes to the job summary.
- **No resource changes:** nothing is posted, unless an earlier comment exists. That comment is updated to "no cost change".

**Notes:**
- Until the first release, the action builds `tf-plan-cost` from source on each run. It uses the runner's Go when one is installed (any Go 1.21+ fetches the right version itself), so your `PATH` is unchanged. It installs Go only when none is present.
- On GitHub Enterprise Server, the API URL is read from `GITHUB_API_URL`.

## Install

**From source** (Go 1.27+):

```sh
go install github.com/mubarak-j/tf-plan-cost/cmd/tf-plan-cost@latest
```

Or clone the repository and run `go build ./cmd/tf-plan-cost`.

**Container image:**

```sh
docker build -t tf-plan-cost .
```

The image is distroless (`static-debian12:nonroot`) and contains only the binary and CA certificates.

Prebuilt binaries and a published image are planned for the first release.

## Usage

### 1. Give it AWS credentials

The AWS Price List API requires signed requests, even though prices are public. Any credentials from the standard AWS chain work (environment variables, an SSO profile, OIDC in CI, an instance role), as long as they allow one action:

```json
{
  "Version": "2012-10-17",
  "Statement": [{ "Effect": "Allow", "Action": "pricing:GetProducts", "Resource": "*" }]
}
```

Locally, an SSO profile is the easiest option:

```sh
aws sso login --profile my-profile
export AWS_PROFILE=my-profile
```

### 2. Export your plan as JSON

```sh
terraform plan -out=tfplan
terraform show -json tfplan > plan.json
```

### 3. Run it

```sh
tf-plan-cost diff plan.json                 # Markdown to stdout
tf-plan-cost diff plan.json --format json   # machine-readable report
terraform show -json tfplan | tf-plan-cost diff -   # read from stdin
```

| Flag | Default | Description |
|---|---|---|
| `--region` | `$AWS_REGION` or `$AWS_DEFAULT_REGION` | Region for resources whose region isn't in the plan |
| `--format` | `markdown` | `markdown` or `json` |
| `--no-cache` | `false` | Don't read or write the price cache |
| `--cache-dir` | user cache dir (`~/Library/Caches/tf-plan-cost/prices` on macOS, `~/.cache/tf-plan-cost/prices` on Linux) | Where cached prices are stored |

`tf-plan-cost github-comment --report report.json` posts a JSON report (from `diff --format json`) as a PR comment. It is what the Action runs, and it reads the standard `GITHUB_*` environment variables. Run `tf-plan-cost github-comment -h` for its flags.

**Exit codes:**
- `0`: success, including when some resources are listed as not priced.
- `1`: a real error, such as an unreadable plan or missing AWS credentials.
- `2`: invalid usage.
- `3`: (`github-comment` only) the cost increase is above `fail_above_monthly_usd`.

### Reading the output

- **Table:** every created, updated, deleted or replaced resource that could be priced, largest change first.
- **Notes:** charges that depend on usage and are left out, with their rate where AWS publishes one. For example: `data processing at $0.045/GB not included`.
- **Not priced:** changed resources that couldn't be priced, and why. A warning banner appears when a *supported* resource couldn't be priced, because its real cost is unknown.
- **Footer:** the assumptions used and the date of the oldest price.

## Supported resources

| Resource | What's priced | What's noted but not priced |
|---|---|---|
| `aws_instance` | Linux on-demand compute (shared or dedicated tenancy), root and inline EBS volumes | Root volume when its size comes from the AMI |
| `aws_ebs_volume` | Storage for gp2, gp3, io1, io2, st1, sc1 and standard; gp3 IOPS and throughput above baseline; io1 IOPS; io2 tiered IOPS | Magnetic I/O requests |
| `aws_db_instance` | Instance and storage for PostgreSQL, MySQL and MariaDB, Single-AZ or Multi-AZ; io1/io2 IOPS; gp3 IOPS above baseline | gp3 throughput above baseline |
| `aws_nat_gateway` | Hourly charge (public or private) | Data processing per GB |
| `aws_lb` / `aws_alb` | Hourly charge for application, network and gateway load balancers | Capacity units (LCU/NLCU/GLCU) |
| `aws_eks_cluster` | Control plane (standard support) | Extended support rate, Auto Mode per-node fees |
| `aws_eks_node_group` | `desired_size` × on-demand price of the first instance type | Node root volumes; other instance types in the list |

All other resource types are listed under **Not priced** as "resource type not supported yet". Many of them, such as IAM, security groups and S3 bucket settings, cost nothing on their own.

## Known limitations

**Pricing assumptions**
- Prices are **on-demand list prices**: no Savings Plans, Reserved Instances, EDP or other discounts, credits or Free Tier. Your actual bill may be lower.
- **Linux** only. The plan doesn't say which OS an AMI runs, so Windows or RHEL instances are priced as Linux. Windows EKS node groups are marked not priced.
- **730 hours a month**, and every resource is assumed to run all month. Auto Scaling, schedules and partial months are not modeled.
- **Usage-based charges are excluded**: data transfer, NAT data processing, load balancer capacity units, requests, snapshots and backups. For network-heavy workloads these can exceed the fixed costs shown.
- Prices are cached for **7 days**. Use `--no-cache` to force fresh prices.

**Not priced (listed with a reason, never guessed)**
- Spot instances and spot node groups.
- Dedicated hosts.
- Instances and node groups whose instance type comes from a launch template.
- RDS engines other than PostgreSQL, MySQL and MariaDB (Oracle, SQL Server, Aurora).
- Regional NAT gateways.
- Values that are only known after apply. For example, `instance_type = some_resource.attr` or a volume size taken from a snapshot.

**Interpreting unset attributes**
- When you leave an optional attribute unset, Terraform shows it as "known after apply" because the provider fills in a default.
- For attributes that only qualify a price, `tf-plan-cost` assumes the AWS default: on-demand rather than spot, shared tenancy, zonal NAT gateway, gp3 baseline IOPS and throughput, Single-AZ, and extended EKS support.
- Attributes that determine the price itself (instance type, size, io1/io2 IOPS) must be known, or the resource is marked not priced.

**Scope**
- AWS only, and only the resource types listed above.
- Regions must be known statically or passed with `--region`. A provider region set from an environment variable in CI, a local value or a module variable can't be read from the plan. AWS provider v6 avoids this, because it records each resource's region in the plan.
- The plan JSON contains your resource attributes, which can include sensitive values. `tf-plan-cost` only sends price queries (service, region and product attributes) to AWS, never the plan itself.

## Development

```sh
go test -race ./...                       # unit and golden tests (no AWS access needed)
go test ./cmd/tf-plan-cost -update        # regenerate golden output after an intended change
AWS_PROFILE=... go test -tags integration ./cmd/tf-plan-cost -run TestRecordPrices   # re-record live prices
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
```

- **Fixtures:** test plans live in `testdata/plans/`. `create.json` is real Terraform output, generated from `testdata/terraform/create`.
- **Recorded prices:** `testdata/prices.json` holds prices recorded from the live API, so tests run offline and stay deterministic.

## License

[Apache 2.0](LICENSE)
